package updates

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mythologyli/zju-connect/gui/internal/sysproc"
)

func installFixture(t *testing.T) *Pending {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	install := filepath.Join(base, "install")
	root, err := os.MkdirTemp(base, ".fdu-update-")
	if err != nil {
		t.Fatal(err)
	}
	p := &Pending{Root: root, Version: "v1.2.3"}
	gui, core := "fdu-connect-gui", "fdu-connect"
	if runtime.GOOS == "windows" {
		gui, core = "FDU-Connect-GUI.exe", "fdu-connect.exe"
	}
	if runtime.GOOS == "darwin" {
		install = filepath.Join(base, "FDU Connect.app")
		p.Executable = filepath.Join(install, "Contents", "MacOS", "FDU Connect")
		source := filepath.Join(root, "payload", "FDU Connect.app")
		for _, name := range []string{"FDU Connect", "fdu-connect"} {
			writeInstallFile(t, filepath.Join(install, "Contents", "MacOS", name), "old-"+name)
			writeInstallFile(t, filepath.Join(source, "Contents", "MacOS", name), "new-"+name)
		}
		digest, err := Digest(source)
		if err != nil {
			t.Fatal(err)
		}
		p.Files = []File{{Source: source, Target: install, SHA256: digest}}
	} else {
		p.Executable = filepath.Join(install, gui)
		for _, name := range []string{gui, core} {
			source := filepath.Join(root, "payload", name)
			writeInstallFile(t, filepath.Join(install, name), "old-"+name)
			writeInstallFile(t, source, "new-"+name)
			digest, err := Digest(source)
			if err != nil {
				t.Fatal(err)
			}
			p.Files = append(p.Files, File{Source: source, Target: filepath.Join(install, name), SHA256: digest})
		}
	}
	writeInstallFile(t, filepath.Join(filepath.Dir(p.Executable), "config.toml"), "personal configuration")
	return p
}

func writeInstallFile(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestApplyReplacesPairAndPreservesPersonalFiles(t *testing.T) {
	p := installFixture(t)
	uninstaller := filepath.Join(filepath.Dir(p.Executable), "Uninstall.exe")
	if runtime.GOOS == "windows" {
		writeInstallFile(t, uninstaller, "existing-uninstaller")
	}
	personal := filepath.Join(filepath.Dir(p.Executable), "config.toml")
	if runtime.GOOS == "darwin" {
		// macOS user data lives outside the app bundle that is replaced as a whole.
		personal = filepath.Join(filepath.Dir(p.Files[0].Target), "config.toml")
		writeInstallFile(t, personal, "personal configuration")
	}
	if err := Apply(p); err != nil {
		t.Fatal(err)
	}
	for _, file := range p.Files {
		digest, err := Digest(file.Target)
		if err != nil || digest != file.SHA256 {
			t.Fatalf("installed digest = %q, %v; want %q", digest, err, file.SHA256)
		}
	}
	if value, err := os.ReadFile(personal); err != nil || string(value) != "personal configuration" {
		t.Fatalf("personal configuration changed: %q, %v", value, err)
	}
	if runtime.GOOS == "windows" {
		if value, err := os.ReadFile(uninstaller); err != nil || string(value) != "existing-uninstaller" {
			t.Fatalf("update changed omitted uninstaller: %q, %v", value, err)
		}
	}
}

func TestApplyRejectsSetupPrograms(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS updates replace an app bundle")
	}
	for _, name := range []string{"MicrosoftEdgeWebView2RuntimeInstallerX64.exe", "Uninstall.exe", "FDU-Connect-v1.0.0-Windows-x64-setup.exe"} {
		t.Run(name, func(t *testing.T) {
			p := installFixture(t)
			target := filepath.Join(filepath.Dir(p.Executable), name)
			source := filepath.Join(p.Root, "payload", name)
			writeInstallFile(t, target, "existing-program")
			writeInstallFile(t, source, "replacement-program")
			digest, err := Digest(source)
			if err != nil {
				t.Fatal(err)
			}
			p.Files = append(p.Files, File{Source: source, Target: target, SHA256: digest})
			if err := Apply(p); err == nil {
				t.Fatal("update accepted a setup-only program")
			}
			if data, err := os.ReadFile(target); err != nil || string(data) != "existing-program" {
				t.Fatalf("rejected update changed setup program: %q, %v", data, err)
			}
		})
	}
}
func TestPublicFileExcludesSetupPrograms(t *testing.T) {
	for _, name := range []string{"MicrosoftEdgeWebView2RuntimeInstallerX64.exe", "Uninstall.exe", "FDU-Connect-v1.0.0-Windows-x64-setup.exe", "unrelated.exe"} {
		if publicFile(name) {
			t.Errorf("publicFile(%q) accepted a setup-only program", name)
		}
	}
}

func TestApplyRollsBackEveryReplacedFile(t *testing.T) {
	p := installFixture(t)
	original := make(map[string]string)
	for _, file := range p.Files {
		digest, err := Digest(file.Target)
		if err != nil {
			t.Fatal(err)
		}
		original[file.Target] = digest
	}
	failSource := p.Files[len(p.Files)-1].Source
	failed := false
	_, err := apply(p, func(from, to string) error {
		if from == failSource && !failed {
			failed = true
			return fmt.Errorf("simulated rename failure")
		}
		return os.Rename(from, to)
	})
	if err == nil || !failed {
		t.Fatalf("expected replacement failure, got %v", err)
	}
	for path, want := range original {
		if got, err := Digest(path); err != nil || got != want {
			t.Fatalf("rollback failed for %s: %q, %v", path, got, err)
		}
	}
	if err := Validate(p); err != nil {
		t.Fatalf("staging should remain retryable after rollback: %v", err)
	}
}

func TestApplyRejectsTamperedPayloadBeforeChangingInstallation(t *testing.T) {
	p := installFixture(t)
	before, err := Digest(p.Executable)
	if err != nil {
		t.Fatal(err)
	}
	source := p.Files[0].Source
	if runtime.GOOS == "darwin" {
		source = filepath.Join(source, "Contents", "MacOS", "FDU Connect")
	}
	writeInstallFile(t, source, "tampered")
	if err := Apply(p); err == nil {
		t.Fatal("tampered payload accepted")
	}
	if got, err := Digest(p.Executable); err != nil || got != before {
		t.Fatalf("installation changed before validation: %q, %v", got, err)
	}
}

func TestFailedRollbackRetainsBackupAndCanBeRetried(t *testing.T) {
	p := installFixture(t)
	want, err := Digest(p.Files[0].Target)
	if err != nil {
		t.Fatal(err)
	}
	failSource := p.Files[len(p.Files)-1].Source
	backup := filepath.Join(p.Root, "backup", "0")
	blocked := true
	rollback, err := apply(p, func(from, to string) error {
		if blocked && (from == failSource || from == backup) {
			return fmt.Errorf("simulated unavailable file")
		}
		return os.Rename(from, to)
	})
	if err == nil {
		t.Fatal("expected failure")
	}
	if digest, err := Digest(backup); err != nil || digest != want {
		t.Fatalf("original backup was lost: %q, %v", digest, err)
	}
	if rollback == nil {
		t.Fatal("failed restoration must retain a rollback operation")
	}
	blocked = false
	if err := rollback(); err != nil {
		t.Fatal(err)
	}
	if err := Validate(p); err != nil {
		t.Fatalf("restoration retry failed: %v", err)
	}
}

func TestValidateRejectsUnsafeLocationsAndIncompletePairs(t *testing.T) {
	for _, kind := range []string{"outside source", "outside target", "duplicate target", "wrong executable", "wrong staging directory", "missing core"} {
		t.Run(kind, func(t *testing.T) {
			p := installFixture(t)
			switch kind {
			case "outside source":
				p.Files[0].Source = p.Executable
			case "outside target":
				p.Files[0].Target = filepath.Join(filepath.Dir(p.Root), "config.toml")
			case "duplicate target":
				p.Files = append(p.Files, p.Files[0])
			case "wrong executable":
				p.Executable = filepath.Join(filepath.Dir(p.Executable), "unrelated-app")
			case "wrong staging directory":
				p.Root = filepath.Dir(p.Root)
			case "missing core":
				if runtime.GOOS == "darwin" {
					if err := os.Remove(filepath.Join(p.Files[0].Source, "Contents", "MacOS", "fdu-connect")); err != nil {
						t.Fatal(err)
					}
					p.Files[0].SHA256, _ = Digest(p.Files[0].Source)
				} else {
					p.Files = p.Files[:1]
				}
			}
			if err := Validate(p); err == nil {
				t.Fatal("unsafe pending update accepted")
			}
		})
	}
}

func TestDigestHashesFilesAndRejectsLinks(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "payload")
	writeInstallFile(t, file, "payload")
	want := fmt.Sprintf("%x", sha256.Sum256([]byte("payload")))
	if got, err := Digest(file); err != nil || got != want {
		t.Fatalf("digest = %q, %v; want %q", got, err, want)
	}
	first, err := Digest(root)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := Digest(root); err != nil || second != first {
		t.Fatalf("unstable directory digest: %q / %q, %v", first, second, err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := Digest(root); err == nil || !strings.Contains(err.Error(), "link") {
		t.Fatalf("tree containing a link accepted: %v", err)
	}
}

func TestRemoveStageGuardsInstallationAndOnlyRemovesItsSibling(t *testing.T) {
	p := installFixture(t)
	stage := p.Root
	p.Root = filepath.Dir(p.Executable)
	if err := RemoveStage(p); err == nil {
		t.Fatal("installation directory accepted for recursive cleanup")
	}
	p.Root = stage
	if err := RemoveStage(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stage); !os.IsNotExist(err) {
		t.Fatalf("stage remains after cleanup: %v", err)
	}
	if _, err := os.Stat(p.Executable); err != nil {
		t.Fatalf("installation was removed: %v", err)
	}
}

func TestCleanupJobMustBelongToTheRestartedExecutable(t *testing.T) {
	p := installFixture(t)
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Root, "job.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cleanupInstallJob(p.Root, p.Executable); err != nil {
		t.Fatal(err)
	}
	if _, err := cleanupInstallJob(p.Root, filepath.Join(filepath.Dir(p.Executable), "other-GUI")); err == nil {
		t.Fatal("another executable accepted a cleanup job")
	}
	if _, err := cleanupInstallJob(filepath.Dir(p.Root), p.Executable); err == nil {
		t.Fatal("unrelated cleanup directory accepted")
	}
}

func TestStartupConfirmationKeepsBackupsUntilCommit(t *testing.T) {
	p := installFixture(t)
	if err := Apply(p); err != nil {
		t.Fatal(err)
	}
	state := &installStartup{pending: p, pid: os.Getpid(), done: make(chan struct{})}
	startupMu.Lock()
	previous := startupState
	startupState = state
	startupMu.Unlock()
	t.Cleanup(func() {
		startupMu.Lock()
		startupState = previous
		startupMu.Unlock()
	})
	if err := ConfirmStartup(); err != nil {
		t.Fatal(err)
	}
	if err := ConfirmStartup(); err != nil {
		t.Fatalf("repeat confirmation failed: %v", err)
	}
	if !StartupPending() {
		t.Fatal("rendering acknowledgement committed the update too early")
	}
	if _, err := os.Stat(filepath.Join(p.Root, "backup", "0")); err != nil {
		t.Fatalf("startup acknowledgement discarded rollback backup: %v", err)
	}
	if _, err := waitStartupCommit(p, state.pid, 5*time.Millisecond); err == nil {
		t.Fatal("startup acknowledgement was accepted as commit")
	}
}

func TestNewGUIOnlyCommitsAfterMatchingLiveAcknowledgement(t *testing.T) {
	for _, kind := range []string{"ready", "missing", "wrong pid", "exited", "not alive"} {
		t.Run(kind, func(t *testing.T) {
			p := installFixture(t)
			done := make(chan struct{})
			stopped := false
			gui := &installChild{pid: 4242, done: done, alive: func() bool { return kind != "not alive" }, stop: func() error { stopped = true; return nil }}
			if kind != "missing" {
				pid := gui.pid
				if kind == "wrong pid" {
					pid++
				}
				if err := writeInstallExclusive(filepath.Join(p.Root, "startup-ready"), []byte(fmt.Sprint(pid)), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "exited" {
				close(done)
			}
			err := commitNewGUI(p, gui, 5*time.Millisecond)
			if kind == "ready" {
				if err != nil || stopped {
					t.Fatalf("healthy rendered GUI rejected: %v (stopped=%v)", err, stopped)
				}
				if helperPID, err := waitStartupCommit(p, gui.pid, time.Millisecond); err != nil || helperPID != os.Getpid() {
					t.Fatalf("commit not bound to this helper and GUI: pid=%d err=%v", helperPID, err)
				}
			} else {
				if err == nil || !stopped {
					t.Fatalf("failed startup was committed or left running: %v (stopped=%v)", err, stopped)
				}
				if _, err := os.Stat(filepath.Join(p.Root, "committed")); !os.IsNotExist(err) {
					t.Fatalf("failed startup created a commit marker: %v", err)
				}
			}
		})
	}
}

func TestInstallCoreRecheckFailsClosed(t *testing.T) {
	for _, name := range sysproc.CoreNames {
		if err := checkInstallCores([]sysproc.Entry{{PID: 42, Name: name}}, nil); err == nil {
			t.Fatalf("active core %s accepted", name)
		}
	}
	if err := checkInstallCores(nil, fmt.Errorf("process list unavailable")); err == nil {
		t.Fatal("process enumeration failure accepted")
	}
	if err := checkInstallCores([]sysproc.Entry{{PID: 43, Name: "mihomo.exe"}}, nil); err != nil {
		t.Fatalf("unrelated Clash process blocked update: %v", err)
	}
}

func TestFailedStartupRemainsBlockedAndOrdinaryStartupDoesNotWait(t *testing.T) {
	startupMu.Lock()
	previous := startupState
	startupState = nil
	startupMu.Unlock()
	t.Cleanup(func() {
		startupMu.Lock()
		startupState = previous
		startupMu.Unlock()
	})
	if StartupPending() || ConfirmStartup() != nil || AwaitStartup() != nil {
		t.Fatal("ordinary startup was gated by the updater")
	}
	state := &installStartup{result: fmt.Errorf("helper never committed"), done: make(chan struct{})}
	close(state.done)
	startupMu.Lock()
	startupState = state
	startupMu.Unlock()
	if !StartupPending() || ConfirmStartup() == nil || AwaitStartup() == nil {
		t.Fatal("failed startup allowed actions before rollback")
	}
}
