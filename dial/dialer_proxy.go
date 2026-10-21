package dial

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mythologyli/zju-connect/internal/socksclient"
	"github.com/mythologyli/zju-connect/log"
	"github.com/things-go/go-socks5/statute"
)

const (
	maxHTTPProxyResponseHeader = 64 << 10
	httpProxyHandshakeTimeout  = 10 * time.Second
)

type bufferedProxyConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedProxyConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func (d *Dialer) dialDirectWithoutProxy(ctx context.Context, network, addr string) (net.Conn, error) {
	goDialer := &net.Dialer{}
	goDial := goDialer.DialContext
	log.Printf("%s -> DIRECT", addr)
	return goDial(ctx, network, addr)
}

// usedAddr maybe ip:port or hostname:port, it doesn't matter
func (d *Dialer) dialDirectWithHTTPProxy(ctx context.Context, usedAddr string) (net.Conn, error) {
	goDialer := &net.Dialer{}
	goDial := goDialer.DialContext

	log.Printf("%s -> PROXY[%s]", usedAddr, d.dialDirectHTTPProxy)
	conn, err := goDial(ctx, "tcp", d.dialDirectHTTPProxy)
	if err != nil {
		return nil, err
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = conn.Close()
		}
	}()

	deadline := time.Now().Add(httpProxyHandshakeTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	stopCancel := context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Now())
	})
	defer stopCancel()

	request := "CONNECT " + usedAddr + " HTTP/1.1\r\nHost: " + usedAddr + "\r\n\r\n"
	if n, err := io.WriteString(conn, request); err != nil {
		return nil, err
	} else if n != len(request) {
		return nil, io.ErrShortWrite
	}

	bufferedConn, err := readHTTPProxyConnectResponse(conn)
	if err != nil {
		return nil, err
	}
	if !stopCancel() {
		return nil, ctx.Err()
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	succeeded = true
	return bufferedConn, nil
}

func readHTTPProxyConnectResponse(conn net.Conn) (net.Conn, error) {
	reader := bufio.NewReader(conn)
	var header bytes.Buffer
	for {
		fragment, err := reader.ReadSlice('\n')
		if header.Len()+len(fragment) > maxHTTPProxyResponseHeader {
			return nil, fmt.Errorf("HTTP proxy response header exceeds %d bytes", maxHTTPProxyResponseHeader)
		}
		_, _ = header.Write(fragment)
		if bytes.HasSuffix(header.Bytes(), []byte("\r\n\r\n")) {
			break
		}
		if err != nil && !errors.Is(err, bufio.ErrBufferFull) {
			return nil, fmt.Errorf("read HTTP proxy response: %w", err)
		}
	}

	request := &http.Request{Method: http.MethodConnect}
	response, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(header.Bytes())), request)
	if err != nil {
		return nil, fmt.Errorf("parse HTTP proxy response: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP proxy CONNECT failed: %s", response.Status)
	}

	return &bufferedProxyConn{Conn: conn, reader: reader}, nil
}

func (d *Dialer) dialDirectWithSocksProxy(ctx context.Context, network, usedAddr string, isIP bool) (net.Conn, error) {
	log.Printf("%s -> PROXY[%s]", usedAddr, d.dialDirectSocksProxy)
	// Legacy callers may pass an IPv6 literal without the enclosing brackets.
	if isIP && !strings.HasPrefix(usedAddr, "[") && strings.Count(usedAddr, ":") > 1 {
		portAt := strings.LastIndexByte(usedAddr, ':')
		usedAddr = net.JoinHostPort(usedAddr[:portAt], usedAddr[portAt+1:])
	}
	command := statute.CommandConnect
	if network != "tcp" {
		command = statute.CommandAssociate
	}
	conn, _, err := socksclient.Open(ctx, d.dialDirectSocksProxy, "", "", command, usedAddr)
	return conn, err
}
