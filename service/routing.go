package service

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/mythologyli/zju-connect/client"
	"github.com/mythologyli/zju-connect/configs"
	"github.com/mythologyli/zju-connect/log"
	"github.com/things-go/go-socks5"
	"github.com/things-go/go-socks5/statute"
)

// preserveDomain deliberately leaves DNS to the selected outbound.
type preserveDomain struct{}

func (preserveDomain) Resolve(ctx context.Context, _ string) (context.Context, net.IP, error) {
	return ctx, nil, nil
}

type RoutingProxies struct {
	ctx         context.Context
	cancel      context.CancelFunc
	socks, http net.Listener
	httpServer  *http.Server
	httpProxy   *httpProxy
	mu          sync.Mutex
	conns       map[net.Conn]struct{}
	wg          sync.WaitGroup
	once        sync.Once
}

// StartRoutingProxies binds synchronously, before authentication starts. It never
// changes system proxies, routes, TUN devices or other applications' settings.
func StartRoutingProxies(ctx context.Context, c configs.Config, dial client.DialContextFunc) (*RoutingProxies, error) {
	ctx, cancel := context.WithCancel(ctx)
	s := &RoutingProxies{ctx: ctx, cancel: cancel, conns: make(map[net.Conn]struct{})}
	var err error
	if c.SocksBind != "" {
		s.socks, err = net.Listen("tcp", c.SocksBind)
		if err != nil {
			cancel()
			return nil, err
		}
	}
	if c.HTTPBind != "" {
		s.http, err = net.Listen("tcp", c.HTTPBind)
		if err != nil {
			if s.socks != nil {
				s.socks.Close()
			}
			cancel()
			return nil, err
		}
	}
	if s.socks != nil {
		auth := []socks5.Authenticator{socks5.NoAuthAuthenticator{}}
		if c.SocksUser != "" && c.SocksPasswd != "" {
			auth = []socks5.Authenticator{socks5.UserPassAuthenticator{Credentials: socks5.StaticCredentials{c.SocksUser: c.SocksPasswd}}}
		}
		server := socks5.NewServer(socks5.WithAuthMethods(auth), socks5.WithResolver(preserveDomain{}),
			socks5.WithConnectHandle(s.connect(dial)), socks5.WithAssociateHandle(s.associate(dial)),
			socks5.WithLogger(socks5.NewLogger(log.NewLogger("[ROUTER SOCKS5] "))))
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			for {
				conn, err := s.socks.Accept()
				if err != nil {
					return
				}
				s.mu.Lock()
				if ctx.Err() != nil {
					s.mu.Unlock()
					conn.Close()
					return
				}
				s.conns[conn] = struct{}{}
				s.wg.Add(1)
				s.mu.Unlock()
				go func() {
					defer s.wg.Done()
					defer conn.Close()
					defer func() { s.mu.Lock(); delete(s.conns, conn); s.mu.Unlock() }()
					conn.SetDeadline(time.Now().Add(10 * time.Second))
					_ = server.ServeConn(conn)
				}()
			}
		}()
	}
	if s.http != nil {
		s.httpProxy = newHTTPProxyWithDial(dial)
		// Never inherit HTTP_PROXY/HTTPS_PROXY and bypass the routing decision.
		s.httpProxy.client.Transport.(*http.Transport).Proxy = nil
		s.httpServer = newHTTPServer(s.http.Addr().String(), s.httpProxy)
		s.httpServer.BaseContext = func(net.Listener) context.Context { return ctx }
		s.wg.Add(1)
		go func() { defer s.wg.Done(); _ = s.httpServer.Serve(s.http) }()
	}
	go func() { <-ctx.Done(); s.Close() }()
	return s, nil
}

func (s *RoutingProxies) SocksAddr() string {
	if s.socks == nil {
		return ""
	}
	return s.socks.Addr().String()
}
func (s *RoutingProxies) HTTPAddr() string {
	if s.http == nil {
		return ""
	}
	return s.http.Addr().String()
}
func (s *RoutingProxies) Close() error {
	s.once.Do(func() {
		s.cancel()
		if s.socks != nil {
			s.socks.Close()
		}
		if s.httpServer != nil {
			s.httpServer.Close()
			s.httpProxy.close()
		}
		if s.http != nil {
			s.http.Close()
		}
		s.mu.Lock()
		for conn := range s.conns {
			conn.Close()
		}
		s.mu.Unlock()
		s.wg.Wait()
	})
	return nil
}

func (s *RoutingProxies) connect(dial client.DialContextFunc) socks5.Handler {
	return func(_ context.Context, w io.Writer, req *socks5.Request) error {
		if c, ok := w.(net.Conn); ok {
			c.SetDeadline(time.Time{})
		}
		ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
		target, err := dial(ctx, "tcp", req.DestAddr.String())
		cancel()
		if err != nil {
			_ = socks5.SendReply(w, statute.RepHostUnreachable, nil)
			return err
		}
		defer target.Close()
		if err = socks5.SendReply(w, statute.RepSuccess, target.LocalAddr()); err != nil {
			return err
		}
		done := make(chan struct{}, 2)
		go func() { io.Copy(target, req.Reader); done <- struct{}{} }()
		go func() { io.Copy(w, target); done <- struct{}{} }()
		<-done
		target.Close()
		if c, ok := w.(io.Closer); ok {
			c.Close()
		}
		<-done
		return nil
	}
}

const routingUDPIdleTimeout = 2 * time.Minute
const routingUDPMaxFlows = 128

type udpFlow struct {
	packets chan []byte
	cancel  context.CancelFunc
	header  []byte
}

// Each TCP association owns a bounded set of UDP flows. Pin the client endpoint,
// preserve domain headers, expire idle flows and close them with the control TCP.
func (s *RoutingProxies) associate(dial client.DialContextFunc) socks5.Handler {
	return func(_ context.Context, w io.Writer, req *socks5.Request) error {
		if c, ok := w.(net.Conn); ok {
			c.SetDeadline(time.Time{})
		}
		local, ok := req.LocalAddr.(*net.TCPAddr)
		if !ok {
			return errors.New("invalid SOCKS local address")
		}
		peer, ok := req.RemoteAddr.(*net.TCPAddr)
		if !ok {
			return errors.New("invalid SOCKS peer")
		}
		udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: local.IP})
		if err != nil {
			return err
		}
		defer udp.Close()
		ctx, cancel := context.WithCancel(s.ctx)
		defer cancel()
		stop := context.AfterFunc(ctx, func() { udp.Close() })
		defer stop()
		if err = socks5.SendReply(w, statute.RepSuccess, udp.LocalAddr()); err != nil {
			return err
		}
		go func() { io.Copy(io.Discard, req.Reader); cancel() }()
		var mu sync.Mutex
		var wg sync.WaitGroup
		flows := make(map[string]*udpFlow)
		defer func() {
			cancel()
			mu.Lock()
			for _, f := range flows {
				f.cancel()
			}
			mu.Unlock()
			wg.Wait()
		}()
		var clientAddr *net.UDPAddr
		buf := make([]byte, 65535)
		for {
			n, src, err := udp.ReadFromUDP(buf)
			if err != nil {
				return nil
			}
			if !src.IP.Equal(peer.IP) || req.DestAddr.Port != 0 && src.Port != req.DestAddr.Port || clientAddr != nil && src.String() != clientAddr.String() {
				continue
			}
			if n < 3 || buf[0] != 0 || buf[1] != 0 || buf[2] != 0 {
				continue
			}
			packet, err := statute.ParseDatagram(buf[:n])
			if err != nil {
				continue
			}
			if clientAddr == nil {
				clientAddr = src
			}
			addr := packet.DstAddr.String()
			mu.Lock()
			flow := flows[addr]
			full := len(flows) >= routingUDPMaxFlows
			mu.Unlock()
			if flow == nil {
				if full {
					continue
				}
				flowCtx, flowCancel := context.WithCancel(ctx)
				flow = &udpFlow{packets: make(chan []byte, 8), cancel: flowCancel, header: packet.Header()}
				mu.Lock()
				flows[addr] = flow
				mu.Unlock()
				wg.Add(1)
				go func(addr string, f *udpFlow, dst *net.UDPAddr) {
					defer wg.Done()
					defer f.cancel()
					defer func() { mu.Lock(); delete(flows, addr); mu.Unlock() }()
					relayRoutingUDPFlow(flowCtx, dial, addr, f, udp, dst)
				}(addr, flow, clientAddr)
			}
			select {
			case flow.packets <- append([]byte{}, packet.Data...):
			default:
			}
		}
	}
}

// One slow target must not block another target on the same UDP association.
func relayRoutingUDPFlow(ctx context.Context, dial client.DialContextFunc, addr string, f *udpFlow, relay *net.UDPConn, dst *net.UDPAddr) {
	dialCtx, cancelDial := context.WithTimeout(ctx, 10*time.Second)
	conn, err := dial(dialCtx, "udp", addr)
	cancelDial()
	if err != nil {
		log.DebugPrintf("Router UDP %s: %v", addr, err)
		return
	}
	closeConn := func() { conn.SetDeadline(time.Now()); conn.Close() }
	stop := context.AfterFunc(ctx, closeConn)
	done := make(chan struct{})
	conn.SetReadDeadline(time.Now().Add(routingUDPIdleTimeout))
	go func() {
		defer close(done)
		defer f.cancel()
		payload := make([]byte, 65535)
		for {
			n, err := conn.Read(payload)
			if err != nil {
				return
			}
			conn.SetReadDeadline(time.Now().Add(routingUDPIdleTimeout))
			if len(f.header)+n > 65507 {
				continue
			}
			reply := append(append([]byte{}, f.header...), payload[:n]...)
			if _, err = relay.WriteToUDP(reply, dst); err != nil {
				return
			}
		}
	}()
	defer func() { f.cancel(); closeConn(); <-done; stop() }()
	for {
		select {
		case <-ctx.Done():
			return
		case packet := <-f.packets:
			conn.SetReadDeadline(time.Now().Add(routingUDPIdleTimeout))
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err = conn.Write(packet); err != nil {
				return
			}
		}
	}
}
