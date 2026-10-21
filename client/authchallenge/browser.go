package authchallenge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/websocket"
)

func normalizedCallbackURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid OAuth callback endpoint")
	}
	if u.Scheme == "https" {
		u.Host = strings.TrimSuffix(u.Host, ":443")
	} else if u.Scheme == "http" {
		u.Host = strings.TrimSuffix(u.Host, ":80")
	}
	return u, nil
}

func matchesOAuthCallback(raw, expected string) bool {
	u, err := normalizedCallbackURL(raw)
	if err != nil {
		return false
	}
	want, err := normalizedCallbackURL(expected)
	return err == nil && u.Scheme == want.Scheme && strings.EqualFold(u.Host, want.Host) &&
		u.User == nil && u.Path == want.Path && u.Query().Get("code") != ""
}

// captureBrowserLogin owns a separate browser profile. It never attaches to the
// user's regular browser or sends the intercepted OAuth request to the network.
func captureBrowserLogin(ctx context.Context, loginURL, callbackURL string, output io.Writer) (string, error) {
	var browser string
	for _, path := range []string{
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "Application", "chrome.exe"),
	} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			browser = path
			break
		}
	}
	if browser == "" {
		return "", fmt.Errorf("Microsoft Edge or Google Chrome was not found")
	}
	profile, err := os.MkdirTemp("", "fdu-connect-login-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(profile)
	cmd := exec.CommandContext(ctx, browser,
		"--user-data-dir="+profile, "--remote-debugging-port=0",
		"--remote-debugging-address=127.0.0.1", "--remote-allow-origins=http://127.0.0.1",
		"--no-first-run", "--no-default-browser-check", "--disable-extensions",
		"--disable-background-networking", "--no-proxy-server", "about:blank")
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start login browser: %w", err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	defer func() {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}()
	_, _ = fmt.Fprintln(output, "Opening an isolated login browser. Complete UIS login there; the callback will be captured automatically.")
	startupCtx, startupCancel := context.WithTimeout(ctx, 15*time.Second)
	defer startupCancel()
	endpoint, err := waitForLoginBrowser(startupCtx, profile, done)
	if err != nil {
		return "", err
	}
	return interceptOAuthCallback(ctx, endpoint, loginURL, callbackURL)
}

func waitForLoginBrowser(ctx context.Context, profile string, done <-chan struct{}) (string, error) {
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(filepath.Join(profile, "DevToolsActivePort"))
		if err == nil {
			port, err := strconv.Atoi(strings.SplitN(string(data), "\n", 2)[0])
			if err == nil && port > 0 && port <= 65535 {
				addr := "127.0.0.1:" + strconv.Itoa(port)
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/json/list", nil)
				resp, err := client.Do(req)
				if err == nil {
					var targets []struct {
						Type string `json:"type"`
						URL  string `json:"webSocketDebuggerUrl"`
					}
					decodeErr := json.NewDecoder(resp.Body).Decode(&targets)
					_ = resp.Body.Close()
					if decodeErr == nil {
						for _, target := range targets {
							u, err := url.Parse(target.URL)
							if target.Type == "page" && err == nil && u.Scheme == "ws" && u.Host == addr {
								return target.URL, nil
							}
						}
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("waiting for login browser: %w", ctx.Err())
		case <-done:
			return "", fmt.Errorf("login browser was closed")
		case <-ticker.C:
		}
	}
}

func interceptOAuthCallback(ctx context.Context, endpoint, loginURL, callbackURL string) (string, error) {
	config, err := websocket.NewConfig(endpoint, "http://127.0.0.1")
	if err != nil {
		return "", err
	}
	conn, err := config.DialContext(ctx)
	if err != nil {
		return "", fmt.Errorf("connect to login browser: %w", err)
	}
	defer conn.Close()
	// Browser.close affects only the dedicated profile started for this attempt.
	defer func() {
		_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
		_ = websocket.JSON.Send(conn, map[string]any{"id": 999999, "method": "Browser.close"})
	}()
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	expected, err := normalizedCallbackURL(callbackURL)
	if err != nil {
		return "", err
	}
	expected.RawQuery, expected.Fragment = "", ""
	var id int
	send := func(method string, params any) (int, error) {
		id++
		return id, websocket.JSON.Send(conn, map[string]any{"id": id, "method": method, "params": params})
	}
	// Register interception at Request stage BEFORE allowing any navigation.
	enableID, err := send("Fetch.enable", map[string]any{"patterns": []map[string]string{
		{"urlPattern": expected.String() + "*", "requestStage": "Request"},
	}})
	if err != nil {
		return "", err
	}
	var callback string
	var fulfillID int
	for {
		var msg struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Error  json.RawMessage `json:"error"`
			Params struct {
				RequestID string `json:"requestId"`
				Request   struct {
					URL string `json:"url"`
				} `json:"request"`
			} `json:"params"`
		}
		if err := websocket.JSON.Receive(conn, &msg); err != nil {
			return "", fmt.Errorf("login browser disconnected or timed out")
		}
		if len(msg.Error) > 0 {
			return "", fmt.Errorf("login browser rejected an interception command")
		}
		if msg.ID == enableID {
			_, err = send("Page.navigate", map[string]string{"url": loginURL})
		} else if fulfillID != 0 && msg.ID == fulfillID {
			return callback, nil
		} else if msg.Method == "Fetch.requestPaused" {
			if matchesOAuthCallback(msg.Params.Request.URL, callbackURL) {
				callback = msg.Params.Request.URL
				// Fulfill locally rather than disable interception, which could
				// release the authorization code to the actual VPN endpoint.
				fulfillID, err = send("Fetch.fulfillRequest", map[string]any{
					"requestId": msg.Params.RequestID, "responseCode": 200,
					"responseHeaders": []map[string]string{{"name": "Content-Type", "value": "text/html; charset=utf-8"}},
					"body":            base64.StdEncoding.EncodeToString([]byte("<html><body>Login callback captured. You can return to FDU Connect.</body></html>")),
				})
			} else {
				_, err = send("Fetch.continueRequest", map[string]string{"requestId": msg.Params.RequestID})
			}
		}
		if err != nil {
			return "", err
		}
	}
}
