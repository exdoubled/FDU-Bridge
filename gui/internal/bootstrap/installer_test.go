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
			runs, notices := 0, 0
			r := runtimeInstaller{
				probe: func() (bool, error) { return available, nil },
				run: func(ctx context.Context, path string) error {
					runs++
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
				notify:  func() { notices++ },
				timeout: 10 * time.Millisecond,
			}
			err := r.ensure(dir, digest)
			if tc.want == "" && err != nil {
				t.Fatal(err)
			}
			if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
			shouldRun := tc.installer && !tc.installed && !tc.badHash && !tc.missingHash
			if shouldRun && (runs != 1 || notices != 1) {
				t.Fatalf("install runs/notices = %d/%d", runs, notices)
			}
			if !shouldRun && (runs != 0 || notices != 0) {
				t.Fatalf("unexpected install runs/notices = %d/%d", runs, notices)
			}
			after, _ := os.ReadFile(configPath)
			if string(before) != string(after) {
				t.Fatal("bootstrap modified user settings")
			}
		})
	}
}
