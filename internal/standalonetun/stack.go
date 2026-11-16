package standalonetun

import (
	"context"
	"errors"
	"net"
	"time"

	tun "github.com/mythologyli/sing-tun"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

// Keep TUN forwarding in userspace; no host TCP listeners or firewall rules.
func startStack(ctx context.Context, device tun.Tun, h *handler) (*stack.Stack, error) {
	s := stack.New(stack.Options{NetworkProtocols: []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol}})
	ep := channel.New(256, 1400, "")
	if err := s.CreateNIC(1, ep); err != nil {
		s.Close()
		ep.Close()
		return nil, errors.New(err.String())
	}
	s.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}, {Destination: header.IPv6EmptySubnet, NIC: 1}})
	s.SetSpoofing(1, true)
	s.SetPromiscuousMode(1, true)
	tcpForwarder := tcp.NewForwarder(s, 0, 1024, func(r *tcp.ForwarderRequest) {
		var queue waiter.Queue
		endpoint, err := r.CreateEndpoint(&queue)
		if err != nil {
			r.Complete(true)
			return
		}
		r.Complete(false)
		conn := gonet.NewTCPConn(&queue, endpoint)
		metadata := M.Metadata{Source: M.SocksaddrFromNet(conn.RemoteAddr()), Destination: M.SocksaddrFromNet(conn.LocalAddr())}
		go func() {
			if err := h.NewConnection(ctx, conn, metadata); err != nil {
				h.NewError(ctx, err)
			}
		}()
	})
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, tcpForwarder.HandlePacket)
	udpForwarder := udp.NewForwarder(s, func(r *udp.ForwarderRequest) bool {
		var queue waiter.Queue
		endpoint, err := r.CreateEndpoint(&queue)
		if err != nil {
			return false
		}
		conn := gonet.NewUDPConn(&queue, endpoint)
		destination := M.SocksaddrFromNet(conn.LocalAddr())
		go func() {
			defer conn.Close()
			if err := h.NewPacketConnection(ctx, &packetConn{Conn: conn, destination: destination}, M.Metadata{}); err != nil {
				h.NewError(ctx, err)
			}
		}()
		return true
	})
	s.SetTransportProtocolHandler(udp.ProtocolNumber, udpForwarder.HandlePacket)
	go func() {
		defer ep.Close()
		packet := make([]byte, 65535+tun.PacketOffset)
		for {
			n, err := device.Read(packet)
			if err != nil {
				if h.failed != nil {
					h.failed(err)
				}
				return
			}
			if n <= tun.PacketOffset {
				continue
			}
			data := packet[tun.PacketOffset:n]
			protocol := ipv4.ProtocolNumber
			if data[0]>>4 == 6 {
				protocol = ipv6.ProtocolNumber
			} else if data[0]>>4 != 4 {
				continue
			}
			p := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(append([]byte(nil), data...))})
			ep.InjectInbound(protocol, p)
			p.DecRef()
		}
	}()
	go func() {
		for {
			p := ep.ReadContext(ctx)
			if p == nil {
				return
			}
			data := p.ToView()
			_, err := device.Write(data.AsSlice())
			data.Release()
			p.DecRef()
			if err != nil {
				if h.failed != nil {
					h.failed(err)
				}
				return
			}
		}
	}()
	return s, nil
}

type packetConn struct {
	net.Conn
	destination M.Socksaddr
}

func (c *packetConn) ReadPacket(b *buf.Buffer) (M.Socksaddr, error) {
	c.SetReadDeadline(time.Now().Add(2 * time.Minute))
	_, err := b.ReadOnceFrom(c.Conn)
	return c.destination, err
}
func (c *packetConn) WritePacket(b *buf.Buffer, _ M.Socksaddr) error {
	defer b.Release()
	_, err := c.Write(b.Bytes())
	return err
}
