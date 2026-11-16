package standalonetun

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
	tun "github.com/mythologyli/sing-tun"
	N "github.com/sagernet/sing/common/network"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
)

type memoryTUN struct {
	ctx    context.Context
	client *channel.Endpoint
}

func (d memoryTUN) Read(b []byte) (int, error) {
	p := d.client.ReadContext(d.ctx)
	if p == nil {
		return 0, io.EOF
	}
	defer p.DecRef()
	v := p.ToView()
	defer v.Release()
	return copy(b[tun.PacketOffset:], v.AsSlice()) + tun.PacketOffset, nil
}
func (d memoryTUN) Write(b []byte) (int, error) {
	p := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(append([]byte(nil), b...))})
	defer p.DecRef()
	d.client.InjectInbound(ipv4.ProtocolNumber, p)
	return len(b), nil
}
func (d memoryTUN) Close() error                               { return nil }
func (d memoryTUN) CreateVectorisedWriter() N.VectorisedWriter { return nil }

func TestTUNPacketsReachSharedRouterAndDNS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := stack.New(stack.Options{NetworkProtocols: []stack.NetworkProtocolFactory{ipv4.NewProtocol}, TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol}})
	defer client.Close()
	link := channel.New(256, 1400, "")
	defer link.Close()
	if err := client.CreateNIC(1, link); err != nil {
		t.Fatal(err)
	}
	if err := client.AddProtocolAddress(1, tcpip.ProtocolAddress{Protocol: ipv4.ProtocolNumber, AddressWithPrefix: tcpip.AddressWithPrefix{Address: tcpip.AddrFrom4([4]byte{192, 0, 2, 2}), PrefixLen: 24}}, stack.AddressProperties{}); err != nil {
		t.Fatal(err)
	}
	client.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}})
	called := make(chan string, 8)
	h := &handler{ctx: ctx, names: newNames(), dial: func(_ context.Context, network, target string) (net.Conn, error) {
		called <- network + " " + target
		left, right := net.Pipe()
		go func() {
			defer right.Close()
			b := make([]byte, 128)
			for {
				n, e := right.Read(b)
				if e != nil {
					return
				}
				if _, e = right.Write(b[:n]); e != nil {
					return
				}
			}
		}()
		return left, nil
	}}
	server, err := startStack(ctx, memoryTUN{ctx, link}, h)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	for _, network := range []string{"tcp", "udp"} {
		remote := tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{203, 0, 113, 4}), Port: 443}
		var conn net.Conn
		if network == "tcp" {
			conn, err = gonet.DialContextTCP(ctx, client, remote, ipv4.ProtocolNumber)
		} else {
			conn, err = gonet.DialUDP(client, nil, &remote, ipv4.ProtocolNumber)
		}
		if err != nil {
			t.Fatal(err)
		}
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		if _, err = conn.Write([]byte("echo")); err != nil {
			t.Fatal(err)
		}
		b := make([]byte, 4)
		if _, err = io.ReadFull(conn, b); err != nil || string(b) != "echo" {
			t.Fatalf("%s relay: %q %v", network, b, err)
		}
		conn.Close()
		select {
		case target := <-called:
			if target != network+" 203.0.113.4:443" {
				t.Fatal(target)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	for _, network := range []string{"tcp", "udp"} {
		remote := tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{198, 18, 0, 2}), Port: 53}
		var conn net.Conn
		if network == "tcp" {
			conn, err = gonet.DialContextTCP(ctx, client, remote, ipv4.ProtocolNumber)
		} else {
			conn, err = gonet.DialUDP(client, nil, &remote, ipv4.ProtocolNumber)
		}
		if err != nil {
			t.Fatal(err)
		}
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		query := new(dns.Msg)
		query.SetQuestion("campus.invalid.", dns.TypeA)
		raw, _ := query.Pack()
		if network == "tcp" {
			raw = append([]byte{byte(len(raw) >> 8), byte(len(raw))}, raw...)
		}
		if _, err = conn.Write(raw); err != nil {
			t.Fatal(err)
		}
		var reply []byte
		if network == "tcp" {
			size := make([]byte, 2)
			_, err = io.ReadFull(conn, size)
			if err == nil {
				reply = make([]byte, int(binary.BigEndian.Uint16(size)))
				_, err = io.ReadFull(conn, reply)
			}
		} else {
			reply = make([]byte, 4096)
			var n int
			n, err = conn.Read(reply)
			reply = reply[:n]
		}
		conn.Close()
		if err != nil {
			t.Fatal(network, err)
		}
		var answer dns.Msg
		if err = answer.Unpack(reply); err != nil || len(answer.Answer) != 1 {
			t.Fatal(network, "DNS failed", err)
		}
	}
}
