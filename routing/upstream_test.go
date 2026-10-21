package routing

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/things-go/go-socks5/statute"
)

func testSOCKS(t *testing.T, password string) (string, <-chan string, <-chan struct{}) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	seen := make(chan string, 4)
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(3 * time.Second))
		head := make([]byte, 2)
		if _, err = io.ReadFull(c, head); err != nil {
			return
		}
		methods := make([]byte, int(head[1]))
		io.ReadFull(c, methods)
		method := byte(0)
		if password != "" {
			method = 2
		}
		c.Write([]byte{5, method})
		if method == 2 {
			io.ReadFull(c, head)
			user := make([]byte, int(head[1]))
			io.ReadFull(c, user)
			io.ReadFull(c, head[:1])
			pass := make([]byte, int(head[0]))
			io.ReadFull(c, pass)
			if string(user) != "user" || string(pass) != password {
				c.Write([]byte{1, 1})
				return
			}
			c.Write([]byte{1, 0})
		}
		req, err := statute.ParseRequest(c)
		if err != nil {
			return
		}
		if req.Command == statute.CommandConnect {
			seen <- req.DstAddr.String()
			as, _ := statute.ParseAddrSpec("127.0.0.1:12345")
			c.Write((statute.Reply{Version: 5, BndAddr: as}).Bytes())
			io.Copy(c, c)
			return
		}
		udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			return
		}
		defer udp.Close()
		as, _ := statute.ParseAddrSpec(udp.LocalAddr().String())
		c.Write((statute.Reply{Version: 5, BndAddr: as}).Bytes())
		go func() {
			buf := make([]byte, 65535)
			for {
				n, peer, err := udp.ReadFromUDP(buf)
				if err != nil {
					return
				}
				packet, err := statute.ParseDatagram(buf[:n])
				if err != nil {
					return
				}
				seen <- packet.DstAddr.String()
				udp.WriteToUDP(buf[:n], peer)
			}
		}()
		io.Copy(io.Discard, c)
	}()
	url := "socks5://" + ln.Addr().String()
	if password != "" {
		url = "socks5://user:" + password + "@" + ln.Addr().String()
	}
	return url, seen, closed
}

func TestUpstreamTCPUDPDomainIPv6AndAuth(t *testing.T) {
	for _, network := range []string{"tcp", "udp"} {
		for _, target := range []string{"public.invalid:443", "[2001:db8::1]:443"} {
			for _, password := range []string{"", "secret"} {
				t.Run(network+target+password, func(t *testing.T) {
					url, seen, closed := testSOCKS(t, password)
					u, err := newUpstream(url)
					if err != nil {
						t.Fatal(err)
					}
					c, err := u.Dial(context.Background(), network, target)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					c.SetDeadline(time.Now().Add(2 * time.Second))
					if _, err = c.Write([]byte("payload")); err != nil {
						t.Fatal(err)
					}
					buf := make([]byte, 7)
					if _, err = io.ReadFull(c, buf); err != nil || string(buf) != "payload" {
						t.Fatalf("echo %q: %v", buf, err)
					}
					select {
					case got := <-seen:
						if got != target {
							t.Errorf("target=%s, want %s", got, target)
						}
					case <-time.After(time.Second):
						t.Fatal("no target received")
					}
					c.Close()
					select {
					case <-closed:
					case <-time.After(time.Second):
						t.Fatal("SOCKS TCP/control connection leaked")
					}
				})
			}
		}
	}
}

func TestUpstreamCancellationDuringHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	closed := make(chan struct{})
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		io.Copy(io.Discard, c)
		close(closed)
	}()
	u, _ := newUpstream("socks5://" + ln.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if c, err := u.Dial(ctx, "udp", "public.invalid:443"); err == nil {
		c.Close()
		t.Fatal("handshake ignored cancellation")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("cancelled handshake leaked connection")
	}
}
