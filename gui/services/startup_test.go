package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
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
	ownCores(b)
	return b
}

// ownCores keeps real FDU core detection but reports only the given helper
// PIDs, so FDU cores running on the developer's machine cannot change results.
func ownCores(b *Backend, pids ...int) {
	b.runtime.FindCores = func(exclude uint32) []uint32 {
		var owned []uint32
		for _, pid := range sysproc.FindCores(exclude) {
			if slices.Contains(pids, int(pid)) {
				owned = append(owned, pid)
			}
		}
		return owned
	}
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
	var waiting atomic.Bool
	preparing := make(chan CoreStatus, 1)
	b.OnState = func(status CoreStatus) {
		if strings.Contains(status.Problem, "正在等待 Clash 启动") {
			waiting.Store(true)
		} else if waiting.Load() && status.State == "preparing" && status.Problem == "" {
			select {
			case preparing <- status:
			default:
			}
		}
	}
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
	select {
	case <-preparing:
	default:
		t.Fatal("no preparing status cleared the obsolete Clash waiting message")
	}
}

func startDiscoveryHelper(t *testing.T, name string, args ...string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	helper := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(helper, contents, 0700); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(helper, append([]string{"-test.run=^TestStartupWaitAutoCLIController$", "--"}, args...)...)
	child.Env = append(os.Environ(), "FDU_TEST_CLI_STARTUP=1")
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		_ = input.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close(); _ = child.Process.Kill(); _ = child.Wait() })
}

// ownDiscovery keeps real process discovery, so helpers are still found through
// their command lines, but drops clients whose runtime file is outside dirs.
// Clash clients on the developer's machine therefore cannot change results.
func ownDiscovery(t *testing.T, dirs ...string) {
	t.Helper()
	previous := discoverClients
	discoverClients = func() ([]clash.ClientTarget, error) {
		clients, err := previous()
		var owned []clash.ClientTarget
		for _, client := range clients {
			for _, dir := range dirs {
				rel, relErr := filepath.Rel(dir, client.RuntimeFile)
				if relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					owned = append(owned, client)
					break
				}
			}
		}
		return owned, err
	}
	t.Cleanup(func() { discoverClients = previous })
}

func TestSavedRuntimeMismatchRequiresReselection(t *testing.T) {
	other := filepath.Join(t.TempDir(), "other.yaml")
	if err := os.WriteFile(other, []byte("proxies: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ownDiscovery(t, filepath.Dir(other))
	startDiscoveryHelper(t, "mihomo", "-f", other, "-ext-ctl", "127.0.0.1:19099")
	for _, cliOnly := range []bool{false, true} {
		for _, startup := range []bool{false, true} {
			t.Run(fmt.Sprintf("cliOnly=%v/startup=%v", cliOnly, startup), func(t *testing.T) {
				b := startupBackend(t)
				b.config.Integration.Mode = "auto"
				if cliOnly {
					if err := os.WriteFile(b.config.Integration.RuntimeFile, []byte("proxies: []\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				_, err := b.automaticTarget(b.config.Integration.RuntimeFile, "")
				if err == nil || errors.Is(err, clash.ErrClientNotRunning) || !strings.Contains(err.Error(), "重新选择客户端或改用手动接入") {
					t.Fatalf("wrong guidance for running mismatched core: %v", err)
				}
				if startup {
					err = b.beginConnect(false, false, true)
				} else {
					err = b.connect(false)
				}
				if err != nil {
					t.Fatal(err)
				}
				b.mu.Lock()
				done := b.startDone
				b.mu.Unlock()
				if done != nil {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Fatal("mismatched core left connection waiting")
					}
				}
				if got := b.Status(); got.State != "error" || !strings.Contains(got.Problem, "重新选择客户端或改用手动接入") {
					t.Fatalf("guidance lost in lifecycle: %+v", got)
				}
			})
		}
	}
}

func TestSavedRuntimeStillWaitsWhenOnlyVergeGUIIsPresent(t *testing.T) {
	root := t.TempDir()
	for _, file := range []string{"profiles.yaml", "verge.yaml", "clash-verge.yaml"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte("proxies: []\nexternal-controller: 127.0.0.1:19100\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ownDiscovery(t, root)
	startDiscoveryHelper(t, "clash-verge", "-d", root)
	// Without the helper, this test would pass vacuously.
	if clients, err := discoverClients(); err != nil || len(clients) != 1 || clients[0].Kind != "verge" {
		t.Fatalf("Verge GUI helper was not discovered: %+v / %v", clients, err)
	}
	b := startupBackend(t)
	if _, err := b.automaticTarget(b.config.Integration.RuntimeFile, ""); !errors.Is(err, clash.ErrClientNotRunning) {
		t.Fatalf("Verge GUI without the selected core must remain retryable: %v", err)
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
	ownCores(b, child.Process.Pid)
	// Without the helper, this test would pass vacuously.
	if !b.runtime.ExternalRunning() {
		t.Fatal("helper FDU process was not detected")
	}
	b.conn.API = forbiddenAPI{t}
	if err := b.waitForStartupClash(context.Background()); err == nil {
		t.Fatal("startup ignored another FDU process")
	}
}

func TestStartupWaitAutoCLIController(t *testing.T) {
	if os.Getenv("FDU_TEST_CLI_STARTUP") == "1" {
		_, _ = os.Stdin.Read(make([]byte, 1))
		return
	}
	b := startupBackend(t)
	b.config.Integration.Mode = "auto"
	file := b.config.Integration.RuntimeFile
	ownDiscovery(t, filepath.Dir(file))
	if err := os.WriteFile(file, []byte("proxies: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := b.automaticTarget(file, ""); !errors.Is(err, clash.ErrClientNotRunning) {
		t.Fatalf("offline CLI-only runtime must wait for its process binding: %v", err)
	}
	api := &startupAPI{seen: make(chan struct{}, 1)}
	api.ready.Store(true)
	b.conn.API = api
	if err := b.beginConnect(false, false, true); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	done := b.startDone
	b.mu.Unlock()
	select {
	case <-done:
		t.Fatal("startup did not wait for the selected client")
	case <-time.After(100 * time.Millisecond):
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	name := "mihomo"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	helper := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(helper, contents, 0700); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(helper, "-test.run=^TestStartupWaitAutoCLIController$", "--", "-f", file, "-ext-ctl", "127.0.0.1:9090")
	child.Env = append(os.Environ(), "FDU_TEST_CLI_STARTUP=1")
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("running CLI controller did not unblock startup")
	}
	if got := b.Status(); got.State != "error" || got.Problem != "未找到 VPN 配置" {
		t.Fatalf("did not reach normal preparation after process discovery: %+v", got)
	}
}

type startupBlockingAPI struct {
	base    startupAPI
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (a *startupBlockingAPI) Do(method, path string, payload, out any) error {
	a.once.Do(func() { close(a.entered); <-a.release })
	return a.base.Do(method, path, payload, out)
}

func TestStartupWaitRevokedDuringProbeDoesNotPrepare(t *testing.T) {
	b := startupBackend(t)
	api := &startupBlockingAPI{entered: make(chan struct{}), release: make(chan struct{}), base: startupAPI{seen: make(chan struct{}, 1)}}
	api.base.ready.Store(true)
	b.conn.API = api
	if err := b.beginConnect(false, false, true); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	done := b.startDone
	b.mu.Unlock()
	select {
	case <-api.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no startup probe")
	}
	if _, err := NewAppService(b).SavePreferences("system", false, false, true); err != nil {
		t.Fatal(err)
	}
	close(api.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("startup did not finish after revoked authorization")
	}
	// Normal preparation would fail on this fixture's missing VPN settings.
	if got := b.Status(); got.State != "stopped" || got.Starting || got.Problem != "" {
		t.Fatalf("startup entered preparation after auto-connect was disabled: %+v", got)
	}
}
