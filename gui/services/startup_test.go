package services

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mythologyli/zju-connect/gui/internal/clash"
	"github.com/mythologyli/zju-connect/gui/internal/sysproc"
)

type startupAPI struct {
	ready   atomic.Bool
	seen    chan struct{}
	failure error
}

func (a *startupAPI) Do(method, path string, payload, out any) error {
	if method != "GET" {
		return errors.New("startup probe attempted a write")
	}
	ready := a.ready.Load()
	select {
	case a.seen <- struct{}{}:
	default:
	}
	if a.failure != nil {
		return a.failure
	}
	if !ready {
		return clash.ErrControllerUnavailable
	}
	if path == "/configs" {
		return json.Unmarshal([]byte(`{"mode":"rule"}`), out)
	}
	return json.Unmarshal([]byte(`{"proxies":{}}`), out)
}

func startupBackend(t *testing.T) *Backend {
	t.Helper()
	dir := t.TempDir()
	b, err := NewBackend(dir, filepath.Join(dir, "gui.exe"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.conn.Close)
	file := filepath.Join(dir, "runtime.yaml")
	if err := os.WriteFile(file, []byte("proxies: []\nexternal-controller: 127.0.0.1:9090\n"), 0600); err != nil {
		t.Fatal(err)
	}
	b.config.AutoConnect, b.config.CampusConsent = true, true
	b.config.Integration.Mode, b.config.Integration.RuntimeFile = "manual", file
	return b
}

func TestStartupWaitContinuesWhenControllerAppears(t *testing.T) {
	b := startupBackend(t)
	api := &startupAPI{seen: make(chan struct{}, 1)}
	b.conn.API = api
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- b.waitForStartupClash(ctx) }()
	select {
	case <-api.seen:
	case <-ctx.Done():
		t.Fatal("no readiness probe")
	}
	select {
	case err := <-done:
		t.Fatalf("did not wait: %v", err)
	default:
	}
	api.ready.Store(true)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestStartupWaitDoesNotRetryInvalidController(t *testing.T) {
	b := startupBackend(t)
	want := errors.New("controller rejected credentials")
	b.conn.API = &startupAPI{seen: make(chan struct{}, 1), failure: want}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := b.waitForStartupClash(ctx); !errors.Is(err, want) {
		t.Fatalf("got %v, want original error", err)
	}
}

func TestStartupWaitCancellationPreservesJournal(t *testing.T) {
	b := startupBackend(t)
	api := &startupAPI{seen: make(chan struct{}, 1)}
	b.conn.API = api
	a := clash.CampusAttachment{Root: b.Dir, ProviderPath: filepath.Join(b.Dir, "old.yaml"), Active: true}
	if err := b.rememberCampus(a); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(b.Dir, "campus-session.json")
	before, _ := os.ReadFile(journal)
	if err := b.beginConnect(false, false, true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-api.seen:
	case <-time.After(5 * time.Second):
		t.Fatal("startup did not wait")
	}
	if stopped, err := b.Disconnect(); err != nil || !stopped {
		t.Fatalf("stop: %v %v", stopped, err)
	}
	after, _ := os.ReadFile(journal)
	if string(before) != string(after) {
		t.Fatal("waiting startup changed old recovery journal")
	}
	if b.Status().Starting || b.Status().Running || b.follow {
		t.Fatal("manual stop left startup active")
	}
}

func TestStartupWaitSkipsClashForLocalProxy(t *testing.T) {
	b := startupBackend(t)
	b.config.Integration.Mode = "proxy"
	b.conn.API = forbiddenAPI{t}
	if err := b.waitForStartupClash(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestStartupWaitRequiresAuthorization(t *testing.T) {
	for _, field := range []string{"auto-connect", "consent", "updating", "stopping"} {
		t.Run(field, func(t *testing.T) {
			b := startupBackend(t)
			switch field {
			case "auto-connect":
				b.config.AutoConnect = false
			case "consent":
				b.config.CampusConsent = false
			case "updating":
				b.updating = true
			case "stopping":
				b.stopping = true
			}
			b.conn.API = forbiddenAPI{t}
			if err := b.waitForStartupClash(context.Background()); err == nil {
				t.Fatal("continued without authorization")
			}
		})
	}
}

func TestStartupWaitProceedsToNormalPreparation(t *testing.T) {
	b := startupBackend(t)
	api := &startupAPI{seen: make(chan struct{}, 1)}
	b.conn.API = api
	if err := b.beginConnect(false, false, true); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	done := b.startDone
	b.mu.Unlock()
	select {
	case <-api.seen:
	case <-time.After(5 * time.Second):
		t.Fatal("no probe")
	}
	api.ready.Store(true)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("did not proceed after readiness")
	}
	// This fixture deliberately has no VPN settings: readiness must reach the
	// normal preflight error exactly once, rather than retry arbitrary errors.
	if got := b.Status(); got.State != "error" || got.Problem != "未找到 VPN 配置" || got.Starting {
		t.Fatalf("normal preparation did not run: %+v", got)
	}
}

func TestManualConnectDoesNotWaitForClash(t *testing.T) {
	b := startupBackend(t)
	b.config.Integration.RuntimeFile = filepath.Join(b.Dir, "missing.yaml")
	if err := b.connect(false); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	done := b.startDone
	b.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("manual connection waited for Clash")
		}
	}
	if got := b.Status(); got.State != "error" || got.Starting {
		t.Fatalf("manual preflight did not fail: %+v", got)
	}
}

func TestStartupWaitDoesNotRetryMissingManualFile(t *testing.T) {
	b := startupBackend(t)
	b.config.Integration.RuntimeFile = filepath.Join(b.Dir, "missing.yaml")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := b.waitForStartupClash(ctx)
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("invalid binding was retried: %v", err)
	}
}

func TestStartupWaitStopsForAnotherFDU(t *testing.T) {
	if os.Getenv("FDU_TEST_FOREIGN_STARTUP") == "1" {
		_, _ = os.Stdin.Read(make([]byte, 1))
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(executable, "-test.run=^TestStartupWaitStopsForAnotherFDU$")
	child.Env = append(os.Environ(), "FDU_TEST_FOREIGN_STARTUP=1")
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	// Match the real helper process as an FDU core without installing a binary
	// under a production executable name.
	names := sysproc.CoreNames
	sysproc.CoreNames = []string{filepath.Base(executable)}
	defer func() { sysproc.CoreNames = names }()
	b := startupBackend(t)
	b.conn.API = forbiddenAPI{t}
	if err := b.waitForStartupClash(context.Background()); err == nil {
		t.Fatal("startup ignored another FDU process")
	}
}
