// Package routing selects campus VPN destinations before any DNS lookup.
package routing

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mythologyli/zju-connect/configs"
)

type DialFunc func(context.Context, string, string) (net.Conn, error)

var ErrVPNUnavailable = errors.New("selected campus destination unavailable: VPN is not ready")

type policy struct {
	domains  map[string]bool
	suffixes []string
	prefixes []netip.Prefix
}

func normalizeDomain(s string) string { return strings.ToLower(strings.TrimSuffix(s, ".")) }

func validDomain(s string) bool {
	if s == "" || len(s) > 253 || net.ParseIP(s) != nil {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func newPolicy(c configs.RoutingConfig) (policy, error) {
	p := policy{domains: make(map[string]bool)}
	for _, domain := range append(append([]string{}, c.Domains...), c.DomainSuffixes...) {
		if !validDomain(normalizeDomain(domain)) {
			return p, fmt.Errorf("routing: invalid domain %q (use an ASCII hostname, without URL or wildcard)", domain)
		}
	}
	for _, domain := range c.Domains {
		p.domains[normalizeDomain(domain)] = true
	}
	for _, domain := range c.DomainSuffixes {
		p.suffixes = append(p.suffixes, normalizeDomain(domain))
	}
	for _, cidr := range c.IPCIDRs {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			return p, fmt.Errorf("routing: invalid IP CIDR %q: %w", cidr, err)
		}
		p.prefixes = append(p.prefixes, prefix.Masked())
	}
	return p, nil
}

func (p policy) match(host string) bool {
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap()
		for _, prefix := range p.prefixes {
			if prefix.Contains(ip) {
				return true
			}
		}
		return false
	}
	host = normalizeDomain(host)
	if p.domains[host] {
		return true
	}
	for _, suffix := range p.suffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

// Validate refuses configurations that could silently bypass the router or recurse.
func Validate(c configs.Config) error {
	if !c.Routing.Enabled {
		return nil
	}
	if c.Protocol != "atrust" {
		return errors.New("routing currently requires protocol = atrust")
	}
	if c.BindInterface == "" && !c.AutoDetectInterface {
		return errors.New("routing requires bind_interface or auto_detect_interface to keep VPN control connections outside Clash TUN")
	}
	if (c.SocksUser == "") != (c.SocksPasswd == "") {
		return errors.New("routing requires both socks_user and socks_passwd, or neither")
	}
	if c.TUNMode || c.AddRoute || c.DNSHijack || c.FakeIP || c.ProxyAll || c.TCPTunnelMode || c.DialDirectProxy != "" || c.DisableServerConfig || c.ShadowsocksURL != "" || c.DNSServerBind != "" || len(c.PortForwardingList) != 0 || len(c.CustomProxyDomain) != 0 {
		return errors.New("routing is incompatible with TUN, routes, DNS hijack, fake IP, proxy_all, TCP-only mode, dial_direct_proxy, disabled server ACL, shadowsocks, DNS listener, forwarding or custom_proxy_domain")
	}
	if c.SocksBind == "" && c.HTTPBind == "" {
		return errors.New("routing requires a SOCKS or HTTP listener")
	}
	if _, err := newPolicy(c.Routing); err != nil {
		return err
	}
	u, err := newUpstream(c.Routing.UpstreamProxy)
	if err != nil {
		return err
	}
	_, upstreamPort, _ := net.SplitHostPort(u.address)
	for _, bind := range []string{c.SocksBind, c.HTTPBind} {
		if bind == "" {
			continue
		}
		host, port, err := net.SplitHostPort(bind)
		n, portErr := strconv.Atoi(port)
		if err != nil || portErr != nil || n < 1 || n > 65535 || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			return errors.New("routing listeners must bind a literal loopback IP and port")
		}
		if strconv.Itoa(n) == upstreamPort {
			return errors.New("routing upstream port must differ from both local proxy listener ports")
		}
	}
	return nil
}

type Router struct {
	policy         atomic.Pointer[policy]
	upstreamDial   DialFunc
	mu             sync.Mutex
	vpn            DialFunc
	vpnCtx         context.Context
	vpnCancel      context.CancelFunc
	vpnGeneration  uint64
	control        DialFunc
	controlTargets map[string]bool
	blockedPorts   map[string]bool
	ctx            context.Context
	cancel         context.CancelFunc
	conns          map[*routedConn]struct{}
}

func New(c configs.RoutingConfig) (*Router, error) {
	p, err := newPolicy(c)
	if err != nil {
		return nil, err
	}
	u, err := newUpstream(c.UpstreamProxy)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	router := &Router{upstreamDial: u.Dial, ctx: ctx, cancel: cancel, conns: make(map[*routedConn]struct{}), controlTargets: make(map[string]bool), blockedPorts: make(map[string]bool)}
	router.policy.Store(&p)
	return router, nil
}

// SetVPN publishes a fully initialized strict VPN dialer. nil marks VPN offline.
func (r *Router) SetVPN(dial DialFunc) {
	r.mu.Lock()
	if r.vpnCancel != nil {
		r.vpnCancel()
	}
	r.vpn = dial
	r.vpnGeneration++
	if dial != nil {
		r.vpnCtx, r.vpnCancel = context.WithCancel(r.ctx)
	}
	var conns []*routedConn
	for conn := range r.conns {
		if conn.campus {
			conns = append(conns, conn)
		}
	}
	r.mu.Unlock()
	for _, conn := range conns {
		conn.Close()
	}
}

// SetControl reserves exact VPN/login endpoints for the bound physical underlay.
// Call once before serving; application traffic has no general DIRECT fallback.
func (r *Router) SetControl(dial DialFunc, targets []string, listenerAddresses []string) {
	r.control = dial
	for _, target := range targets {
		r.controlTargets[normalizeDomain(target)] = true
	}
	for _, addr := range listenerAddresses {
		_, port, err := net.SplitHostPort(addr)
		if err == nil {
			r.blockedPorts[port] = true
		}
	}
}

func (r *Router) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	if r.ctx.Err() != nil {
		return nil, net.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if network != "tcp" && network != "udp" {
		return nil, fmt.Errorf("routing: unsupported network %q", network)
	}
	host, port, err := net.SplitHostPort(addr)
	n, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || n < 1 || n > 65535 || host == "" {
		return nil, fmt.Errorf("routing: invalid destination %q", addr)
	}
	if ip := net.ParseIP(host); (ip != nil && ip.IsLoopback() || normalizeDomain(host) == "localhost") && r.blockedPorts[port] {
		return nil, errors.New("routing: refusing connection to own listener")
	}
	r.mu.Lock()
	dial := r.vpn
	vpnCtx, generation := r.vpnCtx, r.vpnGeneration
	r.mu.Unlock()
	campus := false
	if network == "tcp" && r.controlTargets[normalizeDomain(addr)] && r.control != nil {
		dial = r.control
	} else if r.policy.Load().match(host) {
		campus = true
		if dial == nil {
			return nil, ErrVPNUnavailable
		}
	} else {
		dial = r.upstreamDial
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(r.ctx, cancel)
	stopVPN := func() bool { return true }
	if campus {
		stopVPN = context.AfterFunc(vpnCtx, cancel)
	}
	conn, err := dial(ctx, network, addr)
	stop()
	stopVPN()
	cancel()
	if err != nil {
		return nil, err
	}
	tracked := &routedConn{Conn: conn, router: r, campus: campus}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ctx.Err() != nil {
		conn.Close()
		return nil, net.ErrClosed
	}
	if campus && generation != r.vpnGeneration {
		conn.Close()
		return nil, ErrVPNUnavailable
	}
	r.conns[tracked] = struct{}{}
	return tracked, nil
}

func (r *Router) Close() error {
	r.cancel()
	r.SetVPN(nil)
	r.mu.Lock()
	conns := make([]*routedConn, 0, len(r.conns))
	for conn := range r.conns {
		conns = append(conns, conn)
	}
	r.mu.Unlock()
	for _, conn := range conns {
		conn.Close()
	}
	return nil
}

type routedConn struct {
	net.Conn
	router *Router
	campus bool
	once   sync.Once
}

func (c *routedConn) Close() error {
	var err error
	c.once.Do(func() {
		// aTrust CloseWrite may wait for an upload lock; abort its I/O first.
		_ = c.Conn.SetDeadline(time.Now())
		err = c.Conn.Close()
		c.router.mu.Lock()
		delete(c.router.conns, c)
		c.router.mu.Unlock()
	})
	return err
}
