package service

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/mythologyli/zju-connect/configs"
	"github.com/things-go/go-socks5/statute"
)

func TestRoutingUDPDomainAndAssociationClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	seen := make(chan string, 1)
	s, err := StartRoutingProxies(ctx, configs.Config{SocksBind: "127.0.0.1:0"}, func(_ context.Context, network, addr string) (net.Conn, error) {
		seen <- network + " " + addr
		a, b := net.Pipe()
		go func() { defer b.Close(); io.Copy(b, b) }()
		return a, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	control, err := net.Dial("tcp", s.SocksAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	control.SetDeadline(time.Now().Add(2 * time.Second))
	control.Write([]byte{5, 1, 0})
	io.ReadFull(control, make([]byte, 2))
	as, _ := statute.ParseAddrSpec("0.0.0.0:0")
	control.Write((statute.Request{Version: 5, Command: 3, DstAddr: as}).Bytes())
	rep, err := statute.ParseReply(control)
	if err != nil || rep.Response != 0 {
		t.Fatalf("associate %v %v", rep, err)
	}
	remote, _ := net.ResolveUDPAddr("udp", rep.BndAddr.String())
	udp, err := net.DialUDP("udp", nil, remote)
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	udp.SetDeadline(time.Now().Add(time.Second))
	p, _ := statute.NewDatagram("public.invalid:443", []byte("hello"))
	udp.Write(p.Bytes())
	buf := make([]byte, 1024)
	n, err := udp.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := statute.ParseDatagram(buf[:n])
	if err != nil || string(reply.Data) != "hello" {
		t.Fatalf("UDP reply %v %v", reply, err)
	}
	if got := <-seen; got != "udp public.invalid:443" {
		t.Fatal(got)
	}
	control.Close()
	s.Close()
}

func TestRoutingHTTPDoesNotUseEnvironmentProxy(t *testing.T) {
	seen := make(chan string, 1)
	s, err := StartRoutingProxies(context.Background(), configs.Config{HTTPBind: "127.0.0.1:0"}, func(_ context.Context, _, addr string) (net.Conn, error) {
		seen <- addr
		a, b := net.Pipe()
		go func() {
			defer b.Close()
			buf := make([]byte, 4096)
			b.Read(buf)
			io.WriteString(b, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nOK")
		}()
		return a, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	u, _ := url.Parse("http://" + s.HTTPAddr())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(u)}, Timeout: 2 * time.Second}
	resp, err := client.Get("http://public.invalid/path")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := <-seen; got != "public.invalid:80" {
		t.Fatalf("HTTP dialed %s", got)
	}
}

func TestRoutingSlowCampusUDPDoesNotBlockOrdinaryUDP(t *testing.T) {
	for _, blockWrite := range []bool{false, true} {
		name := "blocked dial"
		if blockWrite {
			name = "blocked write"
		}
		t.Run(name, func(t *testing.T) { testRoutingSlowCampusUDP(t, blockWrite) })
	}
}

type blockedUDPTestConn struct {
	net.Conn
	peer                 net.Conn
	started, cancelled   chan struct{}
	startOnce, closeOnce sync.Once
}

func (c *blockedUDPTestConn) Write(p []byte) (int, error) {
	c.startOnce.Do(func() { close(c.started) })
	return c.Conn.Write(p)
}
func (c *blockedUDPTestConn) Close() error {
	c.closeOnce.Do(func() { c.Conn.Close(); c.peer.Close(); close(c.cancelled) })
	return nil
}

func testRoutingSlowCampusUDP(t *testing.T, blockWrite bool) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	s, err := StartRoutingProxies(context.Background(), configs.Config{SocksBind: "127.0.0.1:0"}, func(ctx context.Context, _, addr string) (net.Conn, error) {
		if addr == "campus.invalid:443" {
			if blockWrite {
				a, b := net.Pipe()
				return &blockedUDPTestConn{Conn: a, peer: b, started: started, cancelled: cancelled}, nil
			}
			close(started)
			<-ctx.Done()
			close(cancelled)
			return nil, ctx.Err()
		}
		a, b := net.Pipe()
		go func() { defer b.Close(); io.Copy(b, b) }()
		return a, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	control, err := net.Dial("tcp", s.SocksAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	control.SetDeadline(time.Now().Add(2 * time.Second))
	control.Write([]byte{5, 1, 0})
	io.ReadFull(control, make([]byte, 2))
	as, _ := statute.ParseAddrSpec("0.0.0.0:0")
	control.Write((statute.Request{Version: 5, Command: 3, DstAddr: as}).Bytes())
	rep, err := statute.ParseReply(control)
	if err != nil {
		t.Fatal(err)
	}
	remote, _ := net.ResolveUDPAddr("udp", rep.BndAddr.String())
	udp, err := net.DialUDP("udp", nil, remote)
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	udp.SetDeadline(time.Now().Add(time.Second))
	p, _ := statute.NewDatagram("campus.invalid:443", []byte("campus"))
	udp.Write(p.Bytes())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("campus dial not started")
	}
	p, _ = statute.NewDatagram("public.invalid:443", []byte("ordinary"))
	udp.Write(p.Bytes())
	buf := make([]byte, 1024)
	n, readErr := udp.Read(buf)
	control.Close()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("control close did not cancel pending campus dial")
	}
	if readErr != nil {
		t.Fatalf("ordinary UDP blocked behind campus dial: %v", readErr)
	}
	reply, err := statute.ParseDatagram(buf[:n])
	if err != nil || string(reply.Data) != "ordinary" {
		t.Fatalf("unexpected reply %v %v", reply, err)
	}
}
