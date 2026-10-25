package bootstrap

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureRuntime(t *testing.T) {
	for _, tc := range []struct {
		name                                                                        string
		installed, installer, badHash, missingHash, runFail, staysMissing, timesOut bool
		want                                                                        string
	}{
		{name: "existing runtime skips installer and hash", installed: true},
		{name: "missing runtime installs bundled file", installer: true},
		{name: "missing installer", want: "安装器"},
		{name: "missing build hash", installer: true, missingHash: true, want: "SHA256"},
		{name: "modified installer", installer: true, badHash: true, want: "SHA256"},
		{name: "installer failure", installer: true, runFail: true, want: "install failed"},
		{name: "installer success without runtime", installer: true, staysMissing: true, want: "仍不可用"},
		{name: "installation timeout", installer: true, timesOut: true, want: "超时"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			configPath := filepath.Join(dir, "gui-settings.json")
			if err := os.WriteFile(configPath, []byte(`{"theme":"dark","sentinel":"unchanged"}`), 0600); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(configPath)
			data := []byte("test offline installer")
			digest := fmt.Sprintf("%x", sha256.Sum256(data))
			if tc.installer {
				if err := os.WriteFile(filepath.Join(dir, WebView2InstallerName), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.badHash {
				digest = strings.Repeat("0", 64)
			}
			if tc.missingHash {
				digest = ""
			}
			available := tc.installed
			runs, notices, closes := 0, 0, 0
			r := runtimeInstaller{
				probe: func() (bool, error) { return available, nil },
				run: func(ctx context.Context, path string) error {
					runs++
					if notices != 1 || closes != 0 {
						t.Fatal("installer ran without active progress")
					}
					if path != filepath.Join(dir, WebView2InstallerName) {
						t.Fatalf("installer path = %q", path)
					}
					if tc.timesOut {
						<-ctx.Done()
						return ctx.Err()
					}
					if tc.runFail {
						return errors.New("install failed")
					}
					available = !tc.staysMissing
					return nil
				},
				progress:        func() (func(), error) { notices++; return func() { closes++ }, nil },
				timeout:         10 * time.Millisecond,
				recheckTimeout:  10 * time.Millisecond,
				recheckInterval: time.Millisecond,
			}
			err := r.ensure(dir, digest)
			if tc.want == "" && err != nil {
				t.Fatal(err)
			}
			if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
			shouldRun := tc.installer && !tc.installed && !tc.badHash && !tc.missingHash
			if shouldRun && (runs != 1 || notices != 1 || closes != 1) {
				t.Fatalf("install runs/notices/closes = %d/%d/%d", runs, notices, closes)
			}
			if !shouldRun && (runs != 0 || notices != 0 || closes != 0) {
				t.Fatalf("unexpected install runs/notices/closes = %d/%d/%d", runs, notices, closes)
			}
			after, _ := os.ReadFile(configPath)
			if string(before) != string(after) {
				t.Fatal("bootstrap modified user settings")
			}
		})
	}
}

func TestEnsureRuntimeAcceptsAvailableRuntimeAfterInstallerError(t *testing.T) {
	directory, digest := testInstaller(t)
	available := false
	r := runtimeInstaller{
		probe: func() (bool, error) { return available, nil },
		run: func(context.Context, string) error {
			available = true
			return errors.New("installer reported an error after registration")
		},
		progress: func() (func(), error) { return func() {}, nil },
		timeout:  time.Second,
	}
	if err := r.ensure(directory, digest); err != nil {
		t.Fatalf("usable runtime should allow startup: %v", err)
	}
}

func TestEnsureRuntimeWaitsForRegistration(t *testing.T) {
	directory, digest := testInstaller(t)
	checks := 0
	r := runtimeInstaller{
		probe: func() (bool, error) {
			checks++
			return checks >= 3, nil
		},
		run:             func(context.Context, string) error { return nil },
		progress:        func() (func(), error) { return func() {}, nil },
		timeout:         time.Second,
		recheckTimeout:  20 * time.Millisecond,
		recheckInterval: time.Millisecond,
	}
	if err := r.ensure(directory, digest); err != nil {
		t.Fatalf("delayed runtime registration should allow startup: %v", err)
	}
}

func TestEnsureRuntimeRetriesTransientProbeFailure(t *testing.T) {
	directory, digest := testInstaller(t)
	checks := 0
	r := runtimeInstaller{
		probe: func() (bool, error) {
			checks++
			if checks == 2 {
				return false, errors.New("registration temporarily unavailable")
			}
			return checks >= 3, nil
		},
		run:             func(context.Context, string) error { return nil },
		progress:        func() (func(), error) { return func() {}, nil },
		timeout:         time.Second,
		recheckTimeout:  20 * time.Millisecond,
		recheckInterval: time.Millisecond,
	}
	if err := r.ensure(directory, digest); err != nil {
		t.Fatalf("transient registration error blocked startup: %v", err)
	}
}

func TestEnsureRuntimeAcceptsAvailableRuntimeAfterTimeout(t *testing.T) {
	directory, digest := testInstaller(t)
	available := false
	r := runtimeInstaller{
		probe: func() (bool, error) { return available, nil },
		run: func(ctx context.Context, _ string) error {
			<-ctx.Done()
			available = true
			return ctx.Err()
		},
		progress: func() (func(), error) { return func() {}, nil },
		timeout:  time.Millisecond,
	}
	if err := r.ensure(directory, digest); err != nil {
		t.Fatalf("usable runtime after timeout should allow startup: %v", err)
	}
}

func TestEnsureRuntimeDoesNotInstallWithoutProgress(t *testing.T) {
	directory, digest := testInstaller(t)
	r := runtimeInstaller{
		probe: func() (bool, error) { return false, nil },
		run: func(context.Context, string) error {
			t.Fatal("installer started without visible progress")
			return nil
		},
		progress: func() (func(), error) { return nil, errors.New("native window unavailable") },
	}
	if err := r.ensure(directory, digest); err == nil || !strings.Contains(err.Error(), "native window unavailable") {
		t.Fatalf("error = %v", err)
	}
}

func testInstaller(t *testing.T) (string, string) {
	t.Helper()
	directory := t.TempDir()
	data := []byte("test offline installer")
	if err := os.WriteFile(filepath.Join(directory, WebView2InstallerName), data, 0600); err != nil {
		t.Fatal(err)
	}
	return directory, fmt.Sprintf("%x", sha256.Sum256(data))
}
