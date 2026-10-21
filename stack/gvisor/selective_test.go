package gvisor

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/mythologyli/zju-connect/client"
)

type selectiveClient struct {
	client.Client
	conn io.ReadWriteCloser
}

func (c selectiveClient) IP() (net.IP, error)                    { return net.IPv4(10, 0, 0, 1).To4(), nil }
func (c selectiveClient) NewL3Conn() (io.ReadWriteCloser, error) { return c.conn, nil }

type blockedPacketConn struct {
	done, started   chan struct{}
	once, startOnce sync.Once
}

func (c *blockedPacketConn) Read([]byte) (int, error) { <-c.done; return 0, io.EOF }
func (c *blockedPacketConn) Write([]byte) (int, error) {
	c.startOnce.Do(func() { close(c.started) })
	<-c.done
	return 0, net.ErrClosed
}
func (c *blockedPacketConn) Close() error { c.once.Do(func() { close(c.done) }); return nil }

func TestSelectiveStackDoesNotBlockUDPOnL3Write(t *testing.T) {
	transport := &blockedPacketConn{done: make(chan struct{}), started: make(chan struct{})}
	defer transport.Close()
	s, err := NewStack(selectiveClient{conn: transport})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failed, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.DialUDP(ctx, &net.UDPAddr{IP: net.IPv4(10, 0, 0, 2), Port: 1234})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	written := make(chan struct{})
	go func() { c.Write([]byte("hello")); close(written) }()
	select {
	case <-written:
	case <-time.After(200 * time.Millisecond):
		cancel()
		<-written
		t.Fatal("UDP write waited for L3 transport")
	}
	select {
	case <-transport.started:
	case <-time.After(time.Second):
		t.Fatal("no L3 writer")
	}
	cancel()
	select {
	case <-failed:
	case <-time.After(time.Second):
		t.Fatal("L3 writer ignored cancellation")
	}
}

func TestSelectiveStackReportsDisconnectInsteadOfPanicking(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	s, err := NewStack(selectiveClient{conn: a})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failed, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
	select {
	case err := <-failed:
		if err == nil {
			t.Fatal("missing tunnel failure")
		}
	case <-time.After(time.Second):
		t.Fatal("tunnel failure not reported")
	}
}
