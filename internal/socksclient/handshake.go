// Package socksclient owns the shared SOCKS5 TCP control handshake.
package socksclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/things-go/go-socks5/statute"
)

// Open establishes CONNECT or UDP ASSOCIATE. The caller owns the returned
// control connection and, for UDP, the separate datagram relay lifecycle.
func Open(ctx context.Context, address, user, password string, command byte, target string) (conn net.Conn, bind statute.AddrSpec, err error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	as, err := statute.ParseAddrSpec(target)
	if err != nil || len(as.FQDN) > 255 {
		return nil, bind, errors.New("invalid SOCKS5 target")
	}
	conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, bind, fmt.Errorf("SOCKS5 upstream unavailable: %w", err)
	}
	defer func() {
		if err != nil {
			conn.Close()
		}
	}()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	method := byte(0)
	if user != "" {
		method = 2
	}
	if _, err = conn.Write([]byte{5, 1, method}); err != nil {
		return
	}
	answer := make([]byte, 2)
	if _, err = io.ReadFull(conn, answer); err != nil {
		return
	}
	if answer[0] != 5 || answer[1] != method {
		err = errors.New("SOCKS5 upstream rejected authentication method")
		return
	}
	if method == 2 {
		auth := append([]byte{1, byte(len(user))}, []byte(user)...)
		auth = append(auth, byte(len(password)))
		auth = append(auth, []byte(password)...)
		if _, err = conn.Write(auth); err != nil {
			return
		}
		if _, err = io.ReadFull(conn, answer); err != nil {
			return
		}
		if answer[0] != 1 || answer[1] != 0 {
			err = errors.New("SOCKS5 upstream authentication failed")
			return
		}
	}
	if _, err = conn.Write((statute.Request{Version: 5, Command: command, DstAddr: as}).Bytes()); err != nil {
		return
	}
	var rep statute.Reply
	rep, err = statute.ParseReply(conn)
	if err != nil {
		return
	}
	if rep.Version != 5 || rep.Response != 0 || rep.Reserved != 0 {
		err = fmt.Errorf("SOCKS5 upstream request failed (reply %d)", rep.Response)
		return
	}
	if !stop() || ctx.Err() != nil {
		err = ctx.Err()
		if err == nil {
			err = context.Canceled
		}
		return
	}
	err = conn.SetDeadline(time.Time{})
	bind = rep.BndAddr
	return
}
