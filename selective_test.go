//go:build !tun

package main

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/mythologyli/zju-connect/configs"
	"golang.org/x/net/proxy"
)

// No VPN, real DNS, live ports or external network are used by this test.
func TestSelectiveListenersWorkDuringAndAfterFailedLogin(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pending := make(chan struct{})
	failLogin := make(chan struct{})
	done := make(chan error, 1)
	dial := func(context.Context, string, string) (net.Conn, error) {
		a, b := net.Pipe()
		go func() { defer b.Close(); io.Copy(b, b) }()
		return testSelectiveConn{a}, nil
	}
	go func() {
		done <- serveSelective(ctx, configs.Config{SocksBind: addr}, dial, func(context.Context) error { close(pending); <-failLogin; return errors.New("simulated login failure") })
	}()
	select {
	case <-pending:
	case err := <-done:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("login not started")
	}
	check := func() {
		t.Helper()
		d, _ := proxy.SOCKS5("tcp", addr, nil, proxy.Direct)
		c, err := d.Dial("tcp", "public.invalid:443")
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(time.Second))
		c.Write([]byte("hello"))
		buf := make([]byte, 5)
		if _, err = io.ReadFull(c, buf); err != nil || string(buf) != "hello" {
			t.Fatalf("normal traffic %q %v", buf, err)
		}
	}
	check()
	close(failLogin)
	check()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked")
	}
}

type testSelectiveConn struct{ net.Conn }

func (testSelectiveConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}
}
