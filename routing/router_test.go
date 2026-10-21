package routing

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/mythologyli/zju-connect/configs"
)

func TestPolicyMatchesOnlySelectedDestinations(t *testing.T) {
	p, err := newPolicy(configs.RoutingConfig{Domains: []string{"Exact.Fudan.edu.cn"}, DomainSuffixes: []string{"lab.fudan.edu.cn"}, IPCIDRs: []string{"10.20.26.253/32", "fd00::/64"}})
	if err != nil {
		t.Fatal(err)
	}
	for addr, want := range map[string]bool{
		"EXACT.fudan.edu.cn.": true, "child.exact.fudan.edu.cn": false,
		"lab.fudan.edu.cn": true, "a.lab.fudan.edu.cn": true, "evillab.fudan.edu.cn": false,
		"lab.fudan.edu.cn.evil.example": false, "10.20.26.253": true, "10.20.26.254": false,
		"fd00::1": true, "2001:db8::1": false, "::ffff:10.20.26.253": true,
	} {
		if got := p.match(addr); got != want {
			t.Errorf("match(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestVPNOfflineClosesOnlyCampusConnections(t *testing.T) {
	r, err := New(configs.RoutingConfig{Domains: []string{"campus.invalid"}, UpstreamProxy: "socks5://127.0.0.1:7902"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	campus, campusPeer := net.Pipe()
	defer campusPeer.Close()
	ordinary, ordinaryPeer := net.Pipe()
	defer ordinaryPeer.Close()
	r.SetVPN(func(context.Context, string, string) (net.Conn, error) { return campus, nil })
	r.upstreamDial = func(context.Context, string, string) (net.Conn, error) { return ordinary, nil }
	c, err := r.Dial(context.Background(), "tcp", "campus.invalid:443")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	o, err := r.Dial(context.Background(), "tcp", "public.invalid:443")
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	r.SetVPN(nil)
	campusPeer.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, err = campusPeer.Read(make([]byte, 1)); err == nil {
		t.Fatal("campus connection not closed")
	} else if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatal("campus connection left hanging")
	}
	go ordinaryPeer.Write([]byte{1})
	o.SetReadDeadline(time.Now().Add(time.Second))
	if _, err = o.Read(make([]byte, 1)); err != nil {
		t.Fatalf("ordinary connection closed with VPN: %v", err)
	}
}

func TestRouterPreservesDomainsAndDoesNotFallbackForCampus(t *testing.T) {
	r, err := New(configs.RoutingConfig{Domains: []string{"campus.invalid"}, UpstreamProxy: "socks5://127.0.0.1:7902"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	calls := 0
	r.upstreamDial = func(_ context.Context, network, addr string) (net.Conn, error) {
		calls++
		if addr != "public.invalid:443" || network != "tcp" {
			t.Fatalf("ordinary domain changed: %s %s", network, addr)
		}
		return nil, errors.New("upstream unavailable")
	}
	if _, err := r.Dial(context.Background(), "tcp", "campus.invalid:443"); !errors.Is(err, ErrVPNUnavailable) {
		t.Fatalf("offline campus = %v", err)
	}
	if _, err := r.Dial(context.Background(), "tcp", "public.invalid:443"); err == nil || calls != 1 {
		t.Fatalf("ordinary offline routing calls=%d error=%v", calls, err)
	}
	denied := errors.New("ACL denied")
	r.SetVPN(func(context.Context, string, string) (net.Conn, error) { return nil, denied })
	if _, err := r.Dial(context.Background(), "udp", "campus.invalid:443"); !errors.Is(err, denied) || calls != 1 {
		t.Fatalf("campus failure fell back: %v, calls=%d", err, calls)
	}
	r.SetVPN(nil)
	if _, err := r.Dial(context.Background(), "tcp", "campus.invalid:443"); !errors.Is(err, ErrVPNUnavailable) {
		t.Fatal(err)
	}
}
