package underlay

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// listenLoopback returns an address that accepts one TCP connection.
func listenLoopback(t *testing.T) (string, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	return "127.0.0.1", port
}

func stubResolution(t *testing.T, primary error, doh []net.IP, dohErr error) *int {
	t.Helper()
	originalDial, originalDoH := dialOnInterface, lookupDoH
	t.Cleanup(func() { dialOnInterface, lookupDoH = originalDial, originalDoH })
	dialOnInterface = func(ctx context.Context, network, address, _, _ string) (net.Conn, error) {
		if host, _, _ := net.SplitHostPort(address); net.ParseIP(host) == nil && primary != nil {
			return nil, primary
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	calls := 0
	lookupDoH = func(context.Context, string, string) ([]net.IP, error) {
		calls++
		return doh, dohErr
	}
	return &calls
}

func TestDialFallsBackToDoHWhenResolverIsBlocked(t *testing.T) {
	ip, port := listenLoopback(t)
	calls := stubResolution(t, errors.New("lookup timed out"), []net.IP{net.ParseIP(ip)}, nil)
	d := &Dialer{}
	conn, err := d.dialHost(context.Background(), "tcp", net.JoinHostPort("vpn.example.edu", port), "")
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if *calls != 1 {
		t.Fatalf("DoH calls = %d, want 1", *calls)
	}
	if got := d.addresses.get("VPN.example.edu"); !got.Equal(net.ParseIP(ip)) {
		t.Fatalf("cached address = %v", got)
	}
}

func TestDialUsesLastKnownAddressBeforeDoH(t *testing.T) {
	ip, port := listenLoopback(t)
	calls := stubResolution(t, errors.New("lookup timed out"), nil, errors.New("DoH unreachable"))
	d := &Dialer{}
	d.addresses.put("vpn.example.edu", net.ParseIP(ip))
	conn, err := d.dialHost(context.Background(), "tcp", net.JoinHostPort("vpn.example.edu", port), "")
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if *calls != 0 {
		t.Fatalf("DoH was queried although the cached address worked")
	}
}

func TestDialRemembersAddressAfterNormalSuccess(t *testing.T) {
	ip, port := listenLoopback(t)
	stubResolution(t, nil, nil, nil)
	originalDial := dialOnInterface
	dialOnInterface = func(ctx context.Context, network, address, iface, dns string) (net.Conn, error) {
		_, p, _ := net.SplitHostPort(address)
		return originalDial(ctx, network, net.JoinHostPort(ip, p), iface, dns)
	}
	d := &Dialer{}
	conn, err := d.dialHost(context.Background(), "tcp", net.JoinHostPort("vpn.example.edu", port), "")
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if got := d.addresses.get("vpn.example.edu"); !got.Equal(net.ParseIP(ip)) {
		t.Fatalf("cached address = %v", got)
	}
}

func TestFallbackReportsBothFailuresAndSkipsIPLiterals(t *testing.T) {
	primary := errors.New("lookup timed out")
	calls := stubResolution(t, primary, nil, errors.New("DoH unreachable"))
	d := &Dialer{}
	_, err := d.dialHost(context.Background(), "tcp", "vpn.example.edu:443", "")
	if !errors.Is(err, primary) || !strings.Contains(err.Error(), "DoH unreachable") {
		t.Fatalf("error = %v", err)
	}
	*calls = 0
	if _, err := d.dialHost(context.Background(), "tcp", "127.0.0.1:1", ""); err == nil {
		t.Fatal("expected refused dial")
	}
	if *calls != 0 {
		t.Fatal("DoH was queried for an IP literal")
	}
}

func TestAddressCacheAndDoHAnswersIgnoreFakeIPs(t *testing.T) {
	var c addressCache
	c.put("vpn.example.edu", net.ParseIP("198.18.0.27"))
	if c.get("vpn.example.edu") != nil {
		t.Fatal("cached a TUN fake IP")
	}
	reply := new(dns.Msg)
	reply.SetQuestion("vpn.example.edu.", dns.TypeA)
	reply.Response = true
	for _, a := range []string{"198.18.0.27", "175.186.248.36"} {
		reply.Answer = append(reply.Answer, &dns.A{Hdr: dns.RR_Header{Name: "vpn.example.edu.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.ParseIP(a)})
	}
	raw, _ := reply.Pack()
	ips, err := parseDNSAnswer(raw)
	if err != nil || len(ips) != 1 || ips[0].String() != "175.186.248.36" {
		t.Fatalf("answers = %v, %v", ips, err)
	}
}

func TestRecentResolverFailureSkipsTheSlowLookup(t *testing.T) {
	ip, port := listenLoopback(t)
	stubResolution(t, errors.New("lookup timed out"), []net.IP{net.ParseIP(ip)}, nil)
	primaryHostDials := 0
	stubbed := dialOnInterface
	dialOnInterface = func(ctx context.Context, network, address, iface, dns string) (net.Conn, error) {
		if host, _, _ := net.SplitHostPort(address); net.ParseIP(host) == nil {
			primaryHostDials++
		}
		return stubbed(ctx, network, address, iface, dns)
	}
	d := &Dialer{}
	for range 3 {
		conn, err := d.dialHost(context.Background(), "tcp", net.JoinHostPort("vpn.example.edu", port), "")
		if err != nil {
			t.Fatal(err)
		}
		conn.Close()
	}
	if primaryHostDials != 1 {
		t.Fatalf("resolver attempts = %d; later dials should use the known address", primaryHostDials)
	}
	d.addresses.resolverFailedAt = time.Now().Add(-2 * resolverRetryAfter)
	conn, err := d.dialHost(context.Background(), "tcp", net.JoinHostPort("vpn.example.edu", port), "")
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if primaryHostDials != 2 {
		t.Fatal("resolver was not retried after the failure aged out")
	}
}
