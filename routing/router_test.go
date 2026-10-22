package routing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mythologyli/zju-connect/configs"
	"github.com/things-go/go-socks5/statute"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
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

const initialRules = `{"version":1,"domains":[],"domain_suffixes":[],"ip_cidrs":["10.20.26.253/32"]}`

func TestRuleEntrySwitchesRetainContentAndFilterEachType(t *testing.T) {
	data := `{"version":2,"domains":["Exact.Fudan.edu.cn"],"domain_suffixes":["lab.fudan.edu.cn"],"ip_cidrs":["10.20.26.253"],"disabled":{"domains":["exact.fudan.edu.cn"],"domain_suffixes":["lab.fudan.edu.cn"],"ip_cidrs":["10.20.26.253/32"]}}`
	rules, err := ParseRules([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(rules.Domains) != 1 || len(rules.DomainSuffixes) != 1 || len(rules.IPCIDRs) != 1 {
		t.Fatal("disabled content was deleted")
	}
	r, _ := New(configs.RoutingConfig{UpstreamProxy: "socks5://127.0.0.1:1"})
	defer r.Close()
	if err := r.ReplaceRules(rules); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"exact.fudan.edu.cn", "a.lab.fudan.edu.cn", "10.20.26.253"} {
		if r.policy.Load().match(host) {
			t.Fatalf("disabled rule matched %s", host)
		}
	}
	rules.Disabled = &DisabledRules{Domains: []string{}, DomainSuffixes: []string{}, IPCIDRs: []string{}}
	if err := r.ReplaceRules(rules); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"exact.fudan.edu.cn", "a.lab.fudan.edu.cn", "10.20.26.253"} {
		if !r.policy.Load().match(host) {
			t.Fatalf("re-enabled rule did not match %s", host)
		}
	}
	legacy, _ := ParseRules([]byte(initialRules))
	if err := r.ReplaceRules(legacy); err != nil || !r.policy.Load().match("10.20.26.253") {
		t.Fatal("legacy rules not enabled")
	}
}

func TestDisabledEntriesAreTypedAndDoNotOverrideOtherMatchingRules(t *testing.T) {
	rules, err := ParseRules([]byte(`{"version":2,"domains":["lab.fudan.edu.cn"],"domain_suffixes":["lab.fudan.edu.cn"],"ip_cidrs":["10.20.26.253/32","10.0.0.0/8"],"disabled":{"domains":["lab.fudan.edu.cn"],"domain_suffixes":[],"ip_cidrs":["10.20.26.253/32"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	r, _ := New(configs.RoutingConfig{UpstreamProxy: "socks5://127.0.0.1:1"})
	defer r.Close()
	if err := r.ReplaceRules(rules); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"lab.fudan.edu.cn", "a.lab.fudan.edu.cn", "10.20.26.253"} {
		if !r.policy.Load().match(host) {
			t.Fatalf("disabled entry overrode another enabled rule for %s", host)
		}
	}
}

func TestInvalidEntrySwitchMetadataIsRejected(t *testing.T) {
	for _, data := range []string{
		`{"version":2,"domains":[],"domain_suffixes":[],"ip_cidrs":[]}`,
		`{"version":2,"domains":[],"domain_suffixes":[],"ip_cidrs":[],"disabled":{}}`,
		`{"version":2,"domains":[],"domain_suffixes":[],"ip_cidrs":[],"disabled":{"domains":["missing.invalid"],"domain_suffixes":[],"ip_cidrs":[]}}`,
		`{"version":1,"domains":[],"domain_suffixes":[],"ip_cidrs":[],"disabled":{"domains":[],"domain_suffixes":[],"ip_cidrs":[]}}`,
	} {
		if _, err := ParseRules([]byte(data)); err == nil {
			t.Fatalf("accepted invalid switch metadata: %s", data)
		}
	}
}

func TestRuleDocumentValidationAndCanonicalization(t *testing.T) {
	rules, err := ParseRules([]byte(`{"version":1,"domains":[" Exact.Fudan.edu.cn. ","exact.fudan.edu.cn"],"domain_suffixes":["Lab.Fudan.edu.cn"],"ip_cidrs":["10.20.26.253/24","10.20.26.0/24","10.2.3.4"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(rules.Domains) != "[exact.fudan.edu.cn]" || fmt.Sprint(rules.DomainSuffixes) != "[lab.fudan.edu.cn]" || fmt.Sprint(rules.IPCIDRs) != "[10.20.26.0/24 10.2.3.4/32]" {
		t.Fatalf("unexpected normalized document: %+v", rules)
	}
	for _, data := range []string{
		`{}`, `{"version":1}`, `{"version":1,"domains":null,"domain_suffixes":[],"ip_cidrs":[]}`,
		`{"version":2,"domains":[],"domain_suffixes":[],"ip_cidrs":[]}`,
		`{"version":1,"domains":[],"domain_suffixes":[],"ip_cidrs":[],"unknown":true}`,
		`{"version":1,"domains":["https://fudan.edu.cn/a"],"domain_suffixes":[],"ip_cidrs":[]}`,
		`{"version":1,"domains":[],"domain_suffixes":["*.fudan.edu.cn"],"ip_cidrs":[]}`,
		`{"version":1,"domains":[],"domain_suffixes":[],"ip_cidrs":["::1/128"]}`,
		`{"version":1,"domains":[],"domain_suffixes":[],"ip_cidrs":["10.1.2.3/33"]}`,
		initialRules + `{}`,
	} {
		if _, err := ParseRules([]byte(data)); err == nil {
			t.Errorf("accepted invalid document %s", data)
		}
	}
	if _, err := ParseRules(make([]byte, 256*1024+1)); err == nil {
		t.Fatal("accepted oversized document")
	}
}

func writeTestRules(t *testing.T, path, content string) {
	t.Helper()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

func waitRuleStatus(t *testing.T, path string, predicate func(RuleFileStatus) bool) RuleFileStatus {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path + ".status.json")
		var status RuleFileStatus
		if err == nil && json.Unmarshal(data, &status) == nil && predicate(status) {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("status acknowledgement timed out")
	return RuleFileStatus{}
}

func TestRulesWatcherLoadsAcknowledgesAndKeepsLastValidRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	writeTestRules(t, path, initialRules)
	r, _ := New(configs.RoutingConfig{UpstreamProxy: "socks5://127.0.0.1:7902"})
	defer r.Close()
	watcher, err := watchRulesFile(context.Background(), path, r, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	digest := sha256.Sum256([]byte(initialRules))
	status := waitRuleStatus(t, path, func(s RuleFileStatus) bool { return s.Running && s.LoadedSHA256 == hex.EncodeToString(digest[:]) })
	if status.PID != os.Getpid() || status.Version != 1 || !r.policy.Load().match("10.20.26.253") {
		t.Fatal("wrong initial status/policy")
	}
	updated := `{"version":1,"domains":[],"domain_suffixes":["lab.fudan.edu.cn"],"ip_cidrs":[]}`
	writeTestRules(t, path, updated)
	digest = sha256.Sum256([]byte(updated))
	waitRuleStatus(t, path, func(s RuleFileStatus) bool { return s.LoadedSHA256 == hex.EncodeToString(digest[:]) })
	if !r.policy.Load().match("a.lab.fudan.edu.cn") || r.policy.Load().match("10.20.26.253") {
		t.Fatal("updated policy not in use")
	}
	writeTestRules(t, path, `{"version":1}`)
	waitRuleStatus(t, path, func(s RuleFileStatus) bool { return s.Error != "" })
	if !r.policy.Load().match("a.lab.fudan.edu.cn") {
		t.Fatal("invalid file discarded good rules")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	waitRuleStatus(t, path, func(s RuleFileStatus) bool { return s.Error != "" && s.LoadedSHA256 == hex.EncodeToString(digest[:]) })
	if !r.policy.Load().match("a.lab.fudan.edu.cn") {
		t.Fatal("removed file discarded good rules")
	}
	writeTestRules(t, path, initialRules)
	waitRuleStatus(t, path, func(s RuleFileStatus) bool { return s.Error == "" && r.policy.Load().match("10.20.26.253") })
	if err := watcher.Close(); err != nil {
		t.Fatal(err)
	}
	waitRuleStatus(t, path, func(s RuleFileStatus) bool { return !s.Running })
}

func TestRulesWatcherRejectsInvalidStartup(t *testing.T) {
	r, _ := New(configs.RoutingConfig{UpstreamProxy: "socks5://127.0.0.1:7902"})
	defer r.Close()
	path := filepath.Join(t.TempDir(), "missing.json")
	if _, err := WatchRulesFile(context.Background(), path, r); err == nil {
		t.Fatal("missing initial file accepted")
	}
	writeTestRules(t, path, `{"version":1}`)
	if _, err := WatchRulesFile(context.Background(), path, r); err == nil {
		t.Fatal("invalid initial file accepted")
	}
}

func testSOCKS(t *testing.T, password string) (string, <-chan string, <-chan struct{}) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	seen := make(chan string, 4)
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(3 * time.Second))
		head := make([]byte, 2)
		if _, err = io.ReadFull(c, head); err != nil {
			return
		}
		methods := make([]byte, int(head[1]))
		io.ReadFull(c, methods)
		method := byte(0)
		if password != "" {
			method = 2
		}
		c.Write([]byte{5, method})
		if method == 2 {
			io.ReadFull(c, head)
			user := make([]byte, int(head[1]))
			io.ReadFull(c, user)
			io.ReadFull(c, head[:1])
			pass := make([]byte, int(head[0]))
			io.ReadFull(c, pass)
			if string(user) != "user" || string(pass) != password {
				c.Write([]byte{1, 1})
				return
			}
			c.Write([]byte{1, 0})
		}
		req, err := statute.ParseRequest(c)
		if err != nil {
			return
		}
		if req.Command == statute.CommandConnect {
			seen <- req.DstAddr.String()
			as, _ := statute.ParseAddrSpec("127.0.0.1:12345")
			c.Write((statute.Reply{Version: 5, BndAddr: as}).Bytes())
			io.Copy(c, c)
			return
		}
		udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			return
		}
		defer udp.Close()
		as, _ := statute.ParseAddrSpec(udp.LocalAddr().String())
		c.Write((statute.Reply{Version: 5, BndAddr: as}).Bytes())
		go func() {
			buf := make([]byte, 65535)
			for {
				n, peer, err := udp.ReadFromUDP(buf)
				if err != nil {
					return
				}
				packet, err := statute.ParseDatagram(buf[:n])
				if err != nil {
					return
				}
				seen <- packet.DstAddr.String()
				udp.WriteToUDP(buf[:n], peer)
			}
		}()
		io.Copy(io.Discard, c)
	}()
	url := "socks5://" + ln.Addr().String()
	if password != "" {
		url = "socks5://user:" + password + "@" + ln.Addr().String()
	}
	return url, seen, closed
}

func TestUpstreamTCPUDPDomainIPv6AndAuth(t *testing.T) {
	for _, network := range []string{"tcp", "udp"} {
		for _, target := range []string{"public.invalid:443", "[2001:db8::1]:443"} {
			for _, password := range []string{"", "secret"} {
				t.Run(network+target+password, func(t *testing.T) {
					url, seen, closed := testSOCKS(t, password)
					u, err := newUpstream(url)
					if err != nil {
						t.Fatal(err)
					}
					c, err := u.Dial(context.Background(), network, target)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					c.SetDeadline(time.Now().Add(2 * time.Second))
					if _, err = c.Write([]byte("payload")); err != nil {
						t.Fatal(err)
					}
					buf := make([]byte, 7)
					if _, err = io.ReadFull(c, buf); err != nil || string(buf) != "payload" {
						t.Fatalf("echo %q: %v", buf, err)
					}
					select {
					case got := <-seen:
						if got != target {
							t.Errorf("target=%s, want %s", got, target)
						}
					case <-time.After(time.Second):
						t.Fatal("no target received")
					}
					c.Close()
					select {
					case <-closed:
					case <-time.After(time.Second):
						t.Fatal("SOCKS TCP/control connection leaked")
					}
				})
			}
		}
	}
}

func TestUpstreamCancellationDuringHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	closed := make(chan struct{})
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		io.Copy(io.Discard, c)
		close(closed)
	}()
	u, _ := newUpstream("socks5://" + ln.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if c, err := u.Dial(ctx, "udp", "public.invalid:443"); err == nil {
		c.Close()
		t.Fatal("handshake ignored cancellation")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("cancelled handshake leaked connection")
	}
}
