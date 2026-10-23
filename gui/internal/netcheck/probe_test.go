package netcheck

import (
	"context"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProbeRejectsUnknownTargetsAndNonLoopbackProxies(t *testing.T) {
	for _, id := range []string{"", "https://example.com", "library/../../other", "OPENAI"} {
		if _, err := Probe(context.Background(), id, Options{}); err == nil {
			t.Errorf("accepted target %q", id)
		}
	}
	for _, proxy := range []string{
		"http://localhost:7890", "https://127.0.0.1:7890", "http://192.168.1.2:7890",
		"http://user:secret@127.0.0.1:7890", "http://127.0.0.1:7890/path",
		"http://127.0.0.1:7890?token=secret", "http://127.0.0.1:7890#secret",
		"http://127.0.0.1", "http://127.0.0.1:0", "http://127.0.0.1:65536",
	} {
		if _, err := Probe(context.Background(), "library", Options{ProxyURL: proxy}); err == nil {
			t.Errorf("accepted proxy %q", proxy)
		}
	}
}

func TestProbeDoesNotFollowRedirectOrExposeLocationSecrets(t *testing.T) {
	var followed atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		followed.Store(true)
	}))
	defer destination.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", destination.URL+"/login?token=secret")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	result := localProbe(t, server, Options{Lookup: localLookup})
	if followed.Load() || result.HTTPStatus != http.StatusFound {
		t.Fatalf("redirect followed=%v, HTTP status=%d", followed.Load(), result.HTTPStatus)
	}
	check := findCheck(t, result, "http")
	if check.Status != "ok" || !strings.Contains(check.Detail, "未跟随跳转") || strings.Contains(check.Detail, "secret") || strings.Contains(check.Detail, "/login") {
		t.Fatalf("redirect check = %+v", check)
	}
}

func TestProbeClassifiesAuthenticationAndRecordsTLS(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Method != http.MethodGet {
					t.Error("probe sent credentials or a non-GET request")
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			result := localProbe(t, server, Options{Lookup: localLookup})
			if result.HTTPStatus != status || findCheck(t, result, "http").Status != "auth" {
				t.Fatalf("authentication result = %+v", result)
			}
			if findCheck(t, result, "tcp").Status != "ok" || findCheck(t, result, "tls").Status != "ok" {
				t.Fatalf("connection stages = %+v", result.Checks)
			}
		})
	}
}

func TestAIHTTPResponsesReportTransportReachability(t *testing.T) {
	for _, status := range []int{400, 404, 405, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
			defer server.Close()
			transport := server.Client().Transport.(*http.Transport)
			for _, provider := range []string{"anthropic", "campus"} {
				result := probeTarget(context.Background(), Target{ID: "fixture", Host: "127.0.0.1", Provider: provider, URL: server.URL}, Options{Lookup: localLookup}, transport)
				want := "ok"
				if provider == "campus" {
					want = "error"
				}
				if result.HTTPStatus != status || findCheck(t, result, "http").Status != want {
					t.Fatalf("%s HTTP %d lost transport reachability: %+v", provider, status, result)
				}
			}
		})
	}
}

func TestWebSocketIsNotAttemptedAfterHTTPTransportFailure(t *testing.T) {
	failed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	failed.Close()
	var attempted atomic.Bool
	websocket := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempted.Store(true)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer websocket.Close()
	transport, err := newTransport("")
	if err != nil {
		t.Fatal(err)
	}
	defer transport.CloseIdleConnections()
	result := probeTarget(context.Background(), Target{ID: "fixture", Host: "127.0.0.1", Provider: "openai", URL: failed.URL, wsURL: websocket.URL}, Options{Lookup: localLookup}, transport)
	if result.HTTPStatus != 0 || attempted.Load() || findCheck(t, result, "websocket").Status != "skipped" {
		t.Fatalf("a failed HTTP transport triggered a redundant WebSocket attempt: %+v", result)
	}
}

func TestDNSFailureDoesNotSuppressHTTPAndAnswersAreCandidates(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	result := localProbe(t, server, Options{Lookup: func(context.Context, string) ([]string, error) {
		return nil, errors.New("lookup failed")
	}})
	if findCheck(t, result, "dns").Status != "error" || result.HTTPStatus != http.StatusNoContent {
		t.Fatalf("DNS failure suppressed HTTP: %+v", result)
	}
	result = localProbe(t, server, Options{Lookup: func(context.Context, string) ([]string, error) {
		return []string{"203.0.113.8", "invalid", "203.0.113.8"}, nil
	}})
	if len(result.Answers) != 1 || result.Answers[0] != "203.0.113.8" || !strings.Contains(findCheck(t, result, "dns").Detail, "候选") {
		t.Fatalf("candidate answers = %+v", result)
	}
}

func TestProbeRecordsConnectionFailureAndFixedMetadata(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()
	result := localProbe(t, server, Options{Lookup: localLookup})
	if result.TargetID != "local" || result.Name != "Local test" || result.Host != "127.0.0.1" || result.CheckedAt == 0 || result.HTTPStatus != 0 {
		t.Fatalf("metadata = %+v", result)
	}
	if findCheck(t, result, "tcp").Status != "error" || findCheck(t, result, "tls").Status != "unsupported" || findCheck(t, result, "http").Status != "error" {
		t.Fatalf("failed connection stages = %+v", result.Checks)
	}
}

func TestProbeRejectsUntrustedTLSCertificates(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	transport, err := newTransport("")
	if err != nil {
		t.Fatal(err)
	}
	defer transport.CloseIdleConnections()
	result := probeTarget(context.Background(), Target{ID: "local", Host: "127.0.0.1", Provider: "campus", URL: server.URL}, Options{Lookup: localLookup}, transport)
	if result.HTTPStatus != 0 || findCheck(t, result, "tls").Status != "error" || findCheck(t, result, "http").Status != "error" {
		t.Fatalf("untrusted TLS accepted: %+v", result)
	}
}

func TestProxyEntryIsLabeledAndEnvironmentProxyIsIgnored(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://192.0.2.1:1234")
	transport, err := newTransport("")
	if err != nil || transport.Proxy != nil {
		t.Fatalf("inherited environment proxy: %v, %+v", err, transport)
	}
	for _, proxy := range []string{"http://127.0.0.1:7890", "http://[::1]:7890"} {
		transport, err = newTransport(proxy)
		if err != nil || transport.Proxy == nil {
			t.Fatalf("rejected loopback proxy %q: %v", proxy, err)
		}
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer proxy.Close()
	result, err := Probe(context.Background(), "library", Options{ProxyURL: proxy.URL, Lookup: localLookup})
	if err != nil {
		t.Fatal(err)
	}
	check := findCheck(t, result, "tcp")
	if check.Status != "ok" || !strings.Contains(check.Detail, "代理入口") || strings.Contains(check.Detail, "服务 IP") {
		t.Fatalf("proxy TCP detail = %+v", check)
	}
}

func TestWebSocketValidatesUpgradeAndNeverSendsApplicationData(t *testing.T) {
	for _, valid := range []bool{true, false} {
		t.Run(map[bool]string{true: "valid", false: "invalid_accept"}[valid], func(t *testing.T) {
			closed := make(chan error, 1)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.ProtoMajor != 1 || r.Header.Get("Upgrade") != "websocket" || r.Header.Get("Sec-WebSocket-Version") != "13" || r.Header.Get("Authorization") != "" {
					t.Error("invalid or authenticated handshake request")
				}
				accept := "invalid"
				if valid {
					sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
					accept = base64.StdEncoding.EncodeToString(sum[:])
				}
				connection, buffered, err := w.(http.Hijacker).Hijack()
				if err != nil {
					closed <- err
					return
				}
				defer connection.Close()
				fmt.Fprintf(buffered, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)
				buffered.Flush()
				connection.SetReadDeadline(time.Now().Add(time.Second))
				_, err = buffered.ReadByte()
				closed <- err
			}))
			server.EnableHTTP2 = true
			server.StartTLS()
			defer server.Close()
			transport, err := newTransport("")
			if err != nil {
				t.Fatal(err)
			}
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			transport.TLSClientConfig.RootCAs = roots
			defer transport.CloseIdleConnections()
			check := checkWebSocket(context.Background(), probeClient(transport), server.URL)
			want := "error"
			if valid {
				want = "ok"
			}
			if check.Status != want {
				t.Fatalf("WebSocket check = %+v, want %s", check, want)
			}
			if err := <-closed; err != io.EOF {
				t.Fatalf("expected connection close without application data, got %v", err)
			}
		})
	}
}

func TestWebSocketAuthenticationIsReachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer server.Close()
	transport, err := newTransport("")
	if err != nil {
		t.Fatal(err)
	}
	defer transport.CloseIdleConnections()
	if got := checkWebSocket(context.Background(), probeClient(transport), server.URL); got.Status != "auth" || !strings.Contains(got.Detail, "已响应") {
		t.Fatalf("WebSocket authentication = %+v", got)
	}
}

func TestNetworkErrorsDoNotEchoRemoteTextOrURLSecrets(t *testing.T) {
	err := &url.Error{Op: "Get", URL: "https://api.openai.com/v1/models?token=secret", Err: errors.New("proxy error: authorization=secret")}
	detail := errorDetail(err)
	if strings.Contains(detail, "secret") || strings.Contains(detail, "token") || strings.Contains(detail, "authorization") {
		t.Fatalf("error exposed remote or URL text: %s", detail)
	}
}

func localLookup(context.Context, string) ([]string, error) { return []string{"127.0.0.1"}, nil }

func localProbe(t *testing.T, server *httptest.Server, opts Options) Result {
	t.Helper()
	transport, err := newTransport(opts.ProxyURL)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	defer transport.CloseIdleConnections()
	return probeTarget(context.Background(), Target{ID: "local", Name: "Local test", Host: "127.0.0.1", Provider: "campus", URL: server.URL}, opts, transport)
}

func findCheck(t *testing.T, result Result, kind string) Check {
	t.Helper()
	for _, check := range result.Checks {
		if check.Kind == kind {
			return check
		}
	}
	t.Fatalf("missing %s check: %+v", kind, result)
	return Check{}
}
