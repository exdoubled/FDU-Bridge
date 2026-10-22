package authchallenge

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoginBrowserWaitsForDevToolsHandoff(t *testing.T) {
	profile := t.TempDir()
	var endpoint string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `[{"type":"page","webSocketDebuggerUrl":%q}]`, endpoint)
	}))
	defer server.Close()
	endpoint = "ws://" + server.Listener.Addr().String() + "/devtools/page/test"
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	// The launcher can exit before the real browser creates this file.
	timer := time.AfterFunc(150*time.Millisecond, func() {
		_ = os.WriteFile(filepath.Join(profile, "DevToolsActivePort"), []byte(port+"\n/devtools/browser/test"), 0600)
	})
	defer timer.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := waitForLoginBrowser(ctx, profile)
	if err != nil || got != endpoint {
		t.Fatalf("browser endpoint = %q, %v", got, err)
	}
}

func TestGUIOAuthDoesNotRequestManualCallback(t *testing.T) {
	var output strings.Builder
	h := &guiHandler{output: &output, stop: make(chan struct{})}
	close(h.stop)
	_, err := h.HandleExternalLogin(ExternalLoginChallenge{Kind: ExternalLoginOAuth2})
	if err == nil || output.Len() != 0 {
		t.Fatalf("OAuth failure requested manual input: output=%q, error=%v", output.String(), err)
	}
}

func TestLoginProxyUsesUnderlayDNSAndClosesWithLogin(t *testing.T) {
	seen := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "login page")
	}))
	defer server.Close()
	addr, closeProxy, err := startLoginProxy(context.Background(), func(ctx context.Context, network, target string) (net.Conn, error) {
		seen <- network + " " + target
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeProxy()
	proxyURL, _ := url.Parse("socks5://" + addr)
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	response, err := client.Get("http://login.invalid/auth")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "login page" || <-seen != "tcp login.invalid:80" {
		t.Fatalf("direct login response = %q, %v", body, err)
	}
	idle, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer idle.Close()
	closeProxy()
	idle.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := idle.Read(make([]byte, 1)); err == nil || os.IsTimeout(err) {
		t.Fatalf("login proxy left a connection open: %v", err)
	}
}

func TestBrowserAutomaticLoginWithDirectUnderlay(t *testing.T) {
	if os.Getenv("FDU_CONNECT_BROWSER_TEST") != "1" {
		t.Skip("opt-in: starts a separate browser with local simulated login")
	}
	const callback = "https://vpn.invalid/passport/v1/auth/httpsOauth2"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, callback+"?code=local-test", http.StatusFound)
	}))
	defer server.Close()
	dial := func(ctx context.Context, network, target string) (net.Conn, error) {
		if target != "login.invalid:80" {
			return nil, fmt.Errorf("unexpected login destination")
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	got, err := captureBrowserLogin(ctx, "http://login.invalid/auth", callback, dial, io.Discard)
	if err != nil || got != callback+"?code=local-test" {
		t.Fatalf("automatic callback = %q, %v", got, err)
	}
}
