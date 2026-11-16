package underlay

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
	"github.com/mythologyli/zju-connect/internal/platformdialer"
	"github.com/mythologyli/zju-connect/log"
)

// Public DNS-over-HTTPS resolvers, addressed by IP so they need no lookup.
// Some TUN setups (for example Clash strict-route on Windows) block port 53 on
// the physical adapter while HTTPS still works, which leaves the VPN server
// hostname unresolvable from the bound underlay.
var dohServers = []struct{ ip, host string }{
	{"223.5.5.5", "dns.alidns.com"},
	{"1.12.12.12", "doh.pub"},
}

// addressCache remembers the last real IPv4 address that reached each host, so
// a later DNS outage does not break session refreshes or reconnects.
type addressCache struct {
	mu    sync.Mutex
	hosts map[string]net.IP
	// resolverFailedAt marks a recent resolver failure; while it is fresh,
	// cached addresses are dialed first instead of waiting for DNS again.
	resolverFailedAt time.Time
}

const resolverRetryAfter = 5 * time.Minute

func (c *addressCache) resolverFailed(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resolverFailedAt = now
}

// preferCached returns the cached address when the resolver failed recently.
func (c *addressCache) preferCached(host string, now time.Time) net.IP {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.resolverFailedAt.IsZero() || now.Sub(c.resolverFailedAt) > resolverRetryAfter {
		return nil
	}
	return c.hosts[strings.ToLower(host)]
}

func (c *addressCache) get(host string) net.IP {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hosts[strings.ToLower(host)]
}

func (c *addressCache) put(host string, ip net.IP) {
	ip = ip.To4()
	if ip == nil || fakeIP(ip) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hosts == nil {
		c.hosts = map[string]net.IP{}
	}
	c.hosts[strings.ToLower(host)] = append(net.IP(nil), ip...)
}

func fakeIP(ip net.IP) bool {
	ipv4 := ip.To4()
	return ipv4 != nil && ipv4[0] == 198 && (ipv4[1] == 18 || ipv4[1] == 19)
}

// rememberRemote records the peer address of a successful dial to host.
func (c *addressCache) rememberRemote(host string, conn net.Conn) {
	if net.ParseIP(host) != nil || conn == nil {
		return
	}
	switch addr := conn.RemoteAddr().(type) {
	case *net.TCPAddr:
		c.put(host, addr.IP)
	case *net.UDPAddr:
		c.put(host, addr.IP)
	}
}

// fallbackDial reaches a hostname after the normal resolver failed: first the
// cached address, then DNS-over-HTTPS on the same interface.
func fallbackDial(ctx context.Context, cache *addressCache, network, address, interfaceName string, cause error) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) != nil || strings.HasSuffix(network, "6") {
		return nil, cause
	}
	dialer := &platformdialer.Dialer{InterfaceName: interfaceName}
	tried := map[string]bool{}
	try := func(ip net.IP, source string) (net.Conn, error) {
		if ip == nil || tried[ip.String()] {
			return nil, nil
		}
		tried[ip.String()] = true
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err != nil {
			return nil, err
		}
		log.Printf("Reached %s at %s using %s after the system resolver failed", host, ip, source)
		cache.put(host, ip)
		return conn, nil
	}
	var errs []error
	if conn, err := try(cache.get(host), "the last known address"); conn != nil || err != nil {
		if conn != nil {
			return conn, nil
		}
		errs = append(errs, err)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	ips, err := lookupDoH(ctx, host, interfaceName)
	if err != nil {
		errs = append(errs, err)
	}
	for _, ip := range ips {
		conn, err := try(ip, "DNS over HTTPS")
		if conn != nil {
			return conn, nil
		}
		if err != nil {
			errs = append(errs, err)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("%w; fallback resolution also failed: %w", cause, errors.Join(errs...))
}

var lookupDoH = dohLookup

func dohLookup(ctx context.Context, host, interfaceName string) ([]net.IP, error) {
	query := new(dns.Msg)
	query.SetQuestion(dns.Fqdn(host), dns.TypeA)
	query.Id = 0
	raw, err := query.Pack()
	if err != nil {
		return nil, err
	}
	var errs []error
	for _, server := range dohServers {
		ips, err := dohQuery(ctx, server.ip, server.host, interfaceName, raw)
		if err == nil && len(ips) > 0 {
			return ips, nil
		}
		if err == nil {
			err = fmt.Errorf("no IPv4 answer for %s", host)
		}
		errs = append(errs, fmt.Errorf("DoH %s: %w", server.host, err))
		if ctx.Err() != nil {
			break
		}
	}
	return nil, errors.Join(errs...)
}

func dohQuery(ctx context.Context, ip, serverName, interfaceName string, raw []byte) ([]net.IP, error) {
	dialer := &platformdialer.Dialer{InterfaceName: interfaceName}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip, "443"))
		},
		TLSClientConfig:     &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12},
		Proxy:               nil,
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: 5 * time.Second,
	}
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	url := "https://" + serverName + "/dns-query?dns=" + base64.RawURLEncoding.EncodeToString(raw)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/dns-message")
	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, err
	}
	return parseDNSAnswer(body)
}

func parseDNSAnswer(body []byte) ([]net.IP, error) {
	var reply dns.Msg
	if err := reply.Unpack(bytes.Clone(body)); err != nil {
		return nil, err
	}
	if reply.Rcode != dns.RcodeSuccess {
		return nil, fmt.Errorf("DNS rcode %s", dns.RcodeToString[reply.Rcode])
	}
	var ips []net.IP
	for _, answer := range reply.Answer {
		if a, ok := answer.(*dns.A); ok && !fakeIP(a.A) {
			ips = append(ips, a.A)
		}
	}
	return ips, nil
}
