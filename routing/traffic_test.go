package routing

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/mythologyli/zju-connect/configs"
)

func TestAIProviderUsesCanonicalHostnameBoundaries(t *testing.T) {
	for host, want := range map[string]string{
		"api.openai.com": "openai", "CHATGPT.COM.": "openai", "cdn.oaistatic.com": "openai", "files.oaiusercontent.com": "openai",
		"api.anthropic.com": "anthropic", "claude.ai": "anthropic", "files.claudeusercontent.com": "anthropic",
		"gemini.google.com": "gemini", "aistudio.google.com": "gemini", "generativelanguage.googleapis.com": "gemini",
		"api.x.ai": "grok", "grok.com": "grok",
		"evilopenai.com": "", "api.openai.com.evil.example": "", "openai.com..": "", " openai.com": "", "https://openai.com": "", "openai.com:443": "",
		"google.com": "", "googleapis.com": "", "storage.googleapis.com": "", "googleusercontent.com": "", "gstatic.com": "", "sub.gemini.google.com": "", "gemini.google.com.evil.example": "",
		"192.0.2.1": "", "2001:db8::1": "", "": "",
	} {
		if got := AIProvider(host); got != want {
			t.Errorf("AIProvider(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestTrafficCountsUpstreamPayloadWithoutSOCKSFraming(t *testing.T) {
	for _, network := range []string{"tcp", "udp"} {
		t.Run(network, func(t *testing.T) {
			upstream, _, _ := testSOCKS(t, "secret")
			r, err := New(configs.RoutingConfig{UpstreamProxy: upstream})
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			started := r.Traffic().Started
			c, err := r.Dial(context.Background(), network, "api.openai.com:443")
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(time.Second))
			if _, err := io.Copy(c, strings.NewReader("payload")); err != nil {
				t.Fatal(err)
			}
			if _, err := io.CopyN(io.Discard, c, 7); err != nil {
				t.Fatal(err)
			}
			snapshot := r.Traffic()
			if snapshot.Started != started || started <= 0 || snapshot.NonFDU.Upload != 7 || snapshot.NonFDU.Download != 7 || snapshot.NonFDU.Connections != 1 || snapshot.FDU != (TrafficTotals{}) {
				t.Fatalf("wrong upstream totals: %+v", snapshot)
			}
			if len(snapshot.Connections) != 1 {
				t.Fatalf("AI connection missing: %+v", snapshot)
			}
			flow := snapshot.Connections[0]
			if flow.ID == "" || flow.Provider != "openai" || flow.Host != "api.openai.com" || flow.Port != "443" || flow.Network != network || flow.Route != "non-fdu" || flow.TargetIP != "" || flow.Upload != 7 || flow.Download != 7 || flow.Started < started {
				t.Fatalf("wrong AI connection: %+v", flow)
			}
			c.Close()
			c.Close()
			snapshot = r.Traffic()
			if snapshot.NonFDU.Connections != 0 || snapshot.NonFDU.Upload != 7 || snapshot.NonFDU.Download != 7 || len(snapshot.Connections) != 0 {
				t.Fatalf("close discarded totals or retained active flow: %+v", snapshot)
			}
		})
	}
}

func TestTrafficKeepsEstablishedRouteAndExcludesControlAndFailedDials(t *testing.T) {
	r, err := New(configs.RoutingConfig{Domains: []string{"api.openai.com"}, UpstreamProxy: "socks5://127.0.0.1:7902"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	peers := make(chan net.Conn, 4)
	dialPipe := func(context.Context, string, string) (net.Conn, error) {
		c, peer := net.Pipe()
		t.Cleanup(func() { peer.Close() })
		peers <- peer
		return trafficGatewayConn{c}, nil
	}
	r.SetVPN(dialPipe)
	r.upstreamDial = dialPipe
	r.SetControl(dialPipe, []string{"api.anthropic.com:443"}, nil)
	dial := func(host string) net.Conn {
		t.Helper()
		c, err := r.Dial(context.Background(), "tcp", host+":443")
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	old := dial("api.openai.com")
	oldPeer := <-peers
	if err := r.ReplaceRules(RuleSet{Version: 1, Domains: []string{}, DomainSuffixes: []string{}, IPCIDRs: []string{}}); err != nil {
		t.Fatal(err)
	}
	current := dial("api.openai.com")
	currentPeer := <-peers
	control := dial("api.anthropic.com")
	controlPeer := <-peers
	ordinary := dial("ordinary.invalid")
	ordinaryPeer := <-peers
	exchangeTraffic(t, old, oldPeer, "upload", "down")
	exchangeTraffic(t, current, currentPeer, "up", "download")
	exchangeTraffic(t, control, controlPeer, "control request", "control response")
	exchangeTraffic(t, ordinary, ordinaryPeer, "abc", "de")
	r.upstreamDial = func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("unavailable") }
	if _, err := r.Dial(context.Background(), "tcp", "claude.ai:443"); err == nil {
		t.Fatal("failed dial unexpectedly succeeded")
	}
	snapshot := r.Traffic()
	if snapshot.FDU != (TrafficTotals{Upload: 6, Download: 4, Connections: 1}) || snapshot.NonFDU != (TrafficTotals{Upload: 5, Download: 10, Connections: 2}) || len(snapshot.Connections) != 2 {
		t.Fatalf("route changed or control/failed flow counted: %+v", snapshot)
	}
	routes := map[string]TrafficConnection{}
	for _, flow := range snapshot.Connections {
		if flow.TargetIP != "" {
			t.Fatalf("VPN/upstream gateway was exposed as target IP: %+v", flow)
		}
		routes[flow.Route] = flow
	}
	if routes["fdu"].Upload != 6 || routes["non-fdu"].Upload != 2 || routes["fdu"].ID == routes["non-fdu"].ID {
		t.Fatalf("established flow classification changed: %+v", routes)
	}
	r.Close()
	snapshot = r.Traffic()
	if snapshot.FDU.Connections != 0 || snapshot.NonFDU.Connections != 0 || len(snapshot.Connections) != 0 {
		t.Fatalf("shutdown left active telemetry: %+v", snapshot)
	}
}

type trafficGatewayConn struct{ net.Conn }

func (trafficGatewayConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("203.0.113.9"), Port: 443}
}

func TestTrafficBoundsActiveAIRegistry(t *testing.T) {
	r, err := New(configs.RoutingConfig{UpstreamProxy: "socks5://127.0.0.1:7902"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	r.upstreamDial = func(context.Context, string, string) (net.Conn, error) {
		c, peer := net.Pipe()
		t.Cleanup(func() { peer.Close() })
		return c, nil
	}
	conns := make([]net.Conn, 129)
	for i := range conns {
		conns[i], err = r.Dial(context.Background(), "tcp", "claude.ai:443")
		if err != nil {
			t.Fatal(err)
		}
	}
	snapshot := r.Traffic()
	if len(snapshot.Connections) != 128 || !snapshot.Truncated || snapshot.NonFDU.Connections != 129 {
		t.Fatalf("active AI registry not bounded: %+v", snapshot)
	}
	conns[128].Close()
	conns[128].Close()
	snapshot = r.Traffic()
	if len(snapshot.Connections) != 128 || snapshot.Truncated || snapshot.NonFDU.Connections != 128 {
		t.Fatalf("overflow close retained truncated flag or double-decremented: %+v", snapshot)
	}
}

func exchangeTraffic(t *testing.T, c, peer net.Conn, upload, download string) {
	t.Helper()
	c.SetDeadline(time.Now().Add(time.Second))
	peer.SetDeadline(time.Now().Add(time.Second))
	result := make(chan error, 1)
	go func() {
		if _, err := io.CopyN(io.Discard, peer, int64(len(upload))); err != nil {
			result <- err
			return
		}
		_, err := io.Copy(peer, strings.NewReader(download))
		result <- err
	}()
	if _, err := io.Copy(c, strings.NewReader(upload)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, c, int64(len(download))); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}
