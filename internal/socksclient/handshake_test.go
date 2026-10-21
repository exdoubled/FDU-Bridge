package socksclient

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/things-go/go-socks5/statute"
)

func TestOpenRejectsAuthenticationDowngradeAndClosesConnection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	closed := make(chan error, 1)
	go func() {
		c, err := listener.Accept()
		if err != nil {
			closed <- err
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err = io.ReadFull(c, make([]byte, 3)); err == nil {
			c.Write([]byte{5, 0}) // The client required password authentication.
			_, err = statute.ParseRequest(c)
			if err == nil {
				c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80})
				_, err = io.Copy(io.Discard, c)
			}
		}
		closed <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, _, err := Open(ctx, listener.Addr().String(), "user", "secret", statute.CommandConnect, "public.invalid:443")
	if c != nil {
		t.Cleanup(func() { c.Close() })
	}
	if err == nil {
		t.Fatal("accepted an authentication method the client did not offer")
	}
	select {
	case err := <-closed:
		if e, ok := err.(net.Error); ok && e.Timeout() {
			t.Fatal("server timed out instead of observing a closed connection")
		}
	case <-time.After(time.Second):
		t.Fatal("failed handshake leaked its TCP connection")
	}
}
