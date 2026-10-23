package core

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The test binary doubles as a fake core speaking the GUI protocol.
func TestMain(m *testing.M) {
	if marker := os.Getenv("FDU_FAKE_CORE_MARKER"); marker != "" {
		fakeCore(marker)
		return
	}
	os.Exit(m.Run())
}

func fakeCore(marker string) {
	emit := func(v any) { b, _ := json.Marshal(v); fmt.Println("@@FDU_GUI@@" + string(b)) }
	fmt.Println("password=private-pass started " + strings.Join(os.Args[1:], " "))
	fmt.Println("fresh-login=" + os.Getenv("FDU_CONNECT_GUI_RELOGIN"))
	fmt.Fprintln(os.Stderr, "stderr line")
	in := bufio.NewScanner(os.Stdin)
	if os.Getenv("FDU_FAKE_CORE_CHALLENGE") == "1" {
		emit(map[string]any{"kind": "challenge", "type": "code", "id": 1, "message": "输入验证码"})
		if !in.Scan() {
			os.Exit(3)
		}
		var answer map[string]any
		if json.Unmarshal(in.Bytes(), &answer) != nil || answer["code"] != "123456" || answer["id"] != float64(1) {
			os.Exit(4)
		}
	}
	emit(map[string]any{"kind": "state", "state": "connected"})
	fmt.Println("@@FDU_GUI@@{broken")
	for in.Scan() {
		var request map[string]any
		if json.Unmarshal(in.Bytes(), &request) == nil && request["operation"] == "stop" {
			_ = os.WriteFile(marker, []byte("stopped"), 0o600)
			os.Exit(0)
		}
	}
	os.Exit(5)
}

type recorder struct {
	mu         sync.Mutex
	states     []string
	logs       []string
	challenges chan map[string]any
	connected  chan struct{}
	once       sync.Once
}

func newRuntime(t *testing.T, challenge bool) (*Runtime, *recorder, string) {
	t.Helper()
	dir := t.TempDir()
	config := filepath.Join(dir, "fixture.toml")
	_ = os.WriteFile(config, []byte("protocol='atrust'\n"), 0o600)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "stopped.marker")
	rec := &recorder{challenges: make(chan map[string]any, 1), connected: make(chan struct{})}
	r := &Runtime{Core: exe, Env: []string{"FDU_FAKE_CORE_MARKER=" + marker}}
	if challenge {
		r.Env = append(r.Env, "FDU_FAKE_CORE_CHALLENGE=1")
	}
	r.OnState = func(s string) {
		rec.mu.Lock()
		rec.states = append(rec.states, s)
		rec.mu.Unlock()
		if s == "connected" {
			rec.once.Do(func() { close(rec.connected) })
		}
	}
	r.OnLog = func(l string) { rec.mu.Lock(); rec.logs = append(rec.logs, l); rec.mu.Unlock() }
	r.OnChallenge = func(c map[string]any) { rec.challenges <- c }
	return r, rec, config
}

func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestOwnedChildStopsGracefully(t *testing.T) {
	r, rec, config := newRuntime(t, false)
	if err := r.Start(config, map[string]any{"password": "private-pass"}, "auth-info"); err != nil {
		t.Fatal(err)
	}
	defer r.Kill()
	waitFor(t, rec.connected, "connected state")
	if !r.Running() || !r.Connected() || r.Tool() != "auth-info" {
		t.Fatal("runtime state not tracked")
	}
	if err := r.Start(config, nil, "auth-info"); err == nil {
		t.Fatal("started a second child")
	}
	stopped, err := r.Stop(10 * time.Second)
	if err != nil || !stopped {
		t.Fatalf("child did not stop: %v", err)
	}
	r.Wait()
	if r.Running() {
		t.Fatal("still running after stop")
	}
	if r.Tool() != "" {
		t.Fatal("completed tool still blocks client actions")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(config), "stopped.marker")); err != nil {
		t.Fatal("stop request not received")
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	joined := strings.Join(rec.logs, "\n")
	if strings.Contains(joined, "private-pass") || !strings.Contains(joined, "stderr line") || !strings.Contains(joined, "-auth-info") {
		t.Fatalf("log handling wrong:\n%s", joined)
	}
	if !strings.Contains(joined, "客户端事件格式异常") || !strings.Contains(joined, "进程退出，代码 0") {
		t.Fatalf("protocol errors or exit not logged:\n%s", joined)
	}
	if strings.Join(rec.states, ",") != "tool-running,connected,tool-finished" {
		t.Fatalf("states %v", rec.states)
	}
}

func TestChallengeRoundTrip(t *testing.T) {
	r, rec, config := newRuntime(t, true)
	if err := r.Start(config, nil, "auth-info"); err != nil {
		t.Fatal(err)
	}
	defer r.Kill()
	select {
	case c := <-rec.challenges:
		if c["type"] != "code" || c["message"] != "输入验证码" {
			t.Fatalf("challenge %v", c)
		}
		if err := r.Respond(map[string]any{"id": c["id"], "code": "123456"}); err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no challenge")
	}
	waitFor(t, rec.connected, "connected after challenge")
	if ok, _ := r.Stop(10 * time.Second); !ok {
		t.Fatal("did not stop")
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.states) < 3 || rec.states[1] != "authentication" {
		t.Fatalf("states %v", rec.states)
	}
}

func TestFreshLoginIsExplicitAndLeavesSavedConfigurationIntact(t *testing.T) {
	t.Setenv("FDU_CONNECT_GUI_RELOGIN", "inherited")
	for _, fresh := range []bool{false, true} {
		r, rec, config := newRuntime(t, false)
		before, _ := os.ReadFile(config)
		// A fake tool avoids probing installed cores and listener ports.
		if err := r.start(config, nil, "auth-info", fresh); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = r.Kill() })
		waitFor(t, rec.connected, "fresh-login flag")
		if stopped, err := r.Stop(10 * time.Second); err != nil || !stopped {
			t.Fatalf("stop: %v", err)
		}
		rec.mu.Lock()
		logs := "\n" + strings.Join(rec.logs, "\n") + "\n"
		rec.mu.Unlock()
		want := "fresh-login="
		if fresh {
			want += "1"
		}
		if !strings.Contains(logs, "\n"+want+"\n") {
			t.Fatalf("fresh=%v: incorrect child environment: %s", fresh, logs)
		}
		if after, _ := os.ReadFile(config); string(after) != string(before) {
			t.Fatal("fresh login changed the saved configuration")
		}
	}
}

func TestRejectsUnknownToolAndMissingFiles(t *testing.T) {
	r, _, config := newRuntime(t, false)
	if err := r.Start(config, nil, "delete-everything"); err == nil {
		t.Fatal("accepted unknown action")
	}
	if err := r.Start(config+".missing", nil, "auth-info"); err == nil {
		t.Fatal("accepted missing config")
	}
	r.Core += ".missing"
	if err := r.Start(config, nil, "auth-info"); err == nil {
		t.Fatal("accepted missing core")
	}
}

func TestRespondReportsExitedCore(t *testing.T) {
	r := New(t.TempDir())
	if err := r.Respond(map[string]any{"operation": "relogin"}); err == nil {
		t.Fatal("relogin silently succeeded without a running core")
	}
	if stopped, err := r.Stop(time.Second); err != nil || !stopped {
		t.Fatalf("stopping an exited core: stopped=%v error=%v", stopped, err)
	}
}

func TestRedactsCredentialsAndCallbackParameters(t *testing.T) {
	text := Redact("Authorization: Bearer bearer-private\npassword=private-pass https://vpn.invalid/callback?code=private-code", []string{"private-pass"})
	text += Redact("Given auth data: {Cookies:[{Name:sid Value:private-cookie}]}", nil)
	text += Redact(`{"sid":"sid-private","signKey":"key-private"}`, nil)
	for _, secret := range []string{"bearer-private", "private-pass", "private-code", "private-cookie", "sid-private", "key-private"} {
		if strings.Contains(text, secret) {
			t.Errorf("leaked %s in %q", secret, text)
		}
	}
}

func TestSecretsComeFromSensitiveKeysLongestFirst(t *testing.T) {
	got := secretsOf(map[string]any{"password": "ab", "totp_secret": "abcd", "username": "visible", "sid": ""})
	if len(got) != 2 || got[0] != "abcd" {
		t.Fatalf("secrets %v", got)
	}
}
