// Package standalonetun delivers system TCP/UDP flows to the same router as the
// local proxies. The TUN device owns its routes and DNS settings.
package standalonetun

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"

	tun "github.com/mythologyli/sing-tun"
	"github.com/mythologyli/zju-connect/log"
	"github.com/mythologyli/zju-connect/routing"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

type Session struct {
	device   tun.Tun
	stack    *stack.Stack
	cancel   context.CancelFunc
	once     sync.Once
	closeErr error
	failure  chan error
}

func Start(parent context.Context, dial routing.DialFunc) (*Session, error) {
	ctx, cancel := context.WithCancel(parent)
	s := &Session{cancel: cancel, failure: make(chan error, 1)}
	options := tun.Options{Name: tun.CalculateInterfaceName("FDU-Bridge"), MTU: 1400,
		Inet4Address: []netip.Prefix{netip.MustParsePrefix("198.18.0.1/30")},
		Inet6Address: []netip.Prefix{netip.MustParsePrefix("fdfe:dcba:9876::1/126")},
		AutoRoute:    true, TableIndex: 1898, Logger: logger.NOP()}
	var err error
	s.device, err = tun.New(options)
	if err != nil {
		cancel()
		return nil, err
	}
	h := &handler{ctx: ctx, dial: dial, names: newNames(), failed: func(err error) {
		if ctx.Err() == nil {
			select {
			case s.failure <- err:
			default:
			}
			cancel()
		}
	}}
	s.stack, err = startStack(ctx, s.device, h)
	if err != nil {
		return nil, errors.Join(err, s.Close())
	}
	return s, nil
}

func (s *Session) Failure() <-chan error { return s.failure }

func (s *Session) Close() error {
	s.once.Do(func() {
		s.cancel()
		if s.stack != nil {
			s.stack.Close()
		}
		if s.device != nil {
			s.closeErr = errors.Join(s.closeErr, s.device.Close())
		}
	})
	return s.closeErr
}

type handler struct {
	ctx    context.Context
	dial   routing.DialFunc
	names  *names
	failed func(error)
}

func (h *handler) NewError(_ context.Context, err error) { log.DebugPrintf("TUN: %v", err) }

func (h *handler) NewConnection(_ context.Context, conn net.Conn, metadata M.Metadata) error {
	defer conn.Close()
	stop := context.AfterFunc(h.ctx, func() { conn.Close() })
	defer stop()
	if metadata.Destination.Port == 53 {
		return h.dnsTCP(conn)
	}
	target, err := h.names.target(metadata.Destination.String())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(h.ctx, 30*time.Second)
	remote, err := h.dial(ctx, "tcp", target)
	cancel()
	if err != nil {
		return err
	}
	defer remote.Close()
	done := make(chan struct{})
	go func() { io.Copy(remote, conn); remote.Close(); conn.Close(); close(done) }()
	io.Copy(conn, remote)
	remote.Close()
	conn.Close()
	<-done
	return nil
}

func (h *handler) dnsTCP(conn net.Conn) error {
	for {
		conn.SetDeadline(time.Now().Add(15 * time.Second))
		var size [2]byte
		if _, err := io.ReadFull(conn, size[:]); err != nil {
			return err
		}
		packet := make([]byte, int(binary.BigEndian.Uint16(size[:])))
		if _, err := io.ReadFull(conn, packet); err != nil {
			return err
		}
		reply, err := h.names.answer(packet)
		if err != nil {
			return err
		}
		binary.BigEndian.PutUint16(size[:], uint16(len(reply)))
		if _, err = conn.Write(append(size[:], reply...)); err != nil {
			return err
		}
	}
}

func (h *handler) NewPacketConnection(_ context.Context, conn N.PacketConn, _ M.Metadata) error {
	defer conn.Close()
	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	var wg sync.WaitGroup
	flows := map[string]net.Conn{}
	var writeMu sync.Mutex
	write := func(data []byte, destination M.Socksaddr) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WritePacket(buf.As(append([]byte(nil), data...)), destination)
	}
	defer func() {
		cancel()
		for _, remote := range flows {
			remote.Close()
		}
		wg.Wait()
	}()
	packet := buf.NewSize(65535)
	defer packet.Release()
	for {
		packet.Reset()
		destination, err := conn.ReadPacket(packet)
		if err != nil {
			return err
		}
		if destination.Port == 53 {
			reply, err := h.names.answer(packet.Bytes())
			if err == nil {
				err = write(reply, destination)
			}
			if err != nil {
				return err
			}
			continue
		}
		address := destination.String()
		remote := flows[address]
		if remote == nil {
			if len(flows) >= 128 {
				continue
			}
			target, err := h.names.target(address)
			if err != nil {
				continue
			}
			dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
			remote, err = h.dial(dialCtx, "udp", target)
			dialCancel()
			if err != nil {
				continue
			}
			flows[address] = remote
			wg.Add(1)
			go func(remote net.Conn, destination M.Socksaddr) {
				defer wg.Done()
				defer remote.Close()
				data := make([]byte, 65535)
				for {
					remote.SetReadDeadline(time.Now().Add(2 * time.Minute))
					n, err := remote.Read(data)
					if err != nil {
						return
					}
					if err = write(data[:n], destination); err != nil {
						return
					}
				}
			}(remote, destination)
		}
		remote.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err = remote.Write(packet.Bytes()); err != nil {
			remote.Close()
			delete(flows, address)
		}
	}
}
