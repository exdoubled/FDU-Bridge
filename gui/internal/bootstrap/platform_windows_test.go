package bootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestStartupDiagnosticsContinuesWithoutWritableLog(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("file instead of directory"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		path string
		err  error
	}{
		{"unknown LocalAppData", "", errors.New("known folder unavailable")},
		{"blocked log directory", filepath.Join(blocked, "startup.log"), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var messages []string
			s := startupDiagnostics(func() (string, error) { return tc.path, tc.err }, func(message string) {
				messages = append(messages, message)
			})
			defer s.Close()
			if s.Logger == nil || s.file != nil || len(messages) != 0 {
				t.Fatalf("logging failure must leave a usable startup without dialog: %+v, %v", s, messages)
			}
			// The same bootstrap path can continue with an already installed runtime.
			if err := checkRuntime(func() (bool, error) { return true, nil }); err != nil {
				t.Fatal(err)
			}
			s.Fail(errors.New("later startup failure"))
			if len(messages) != 1 || !strings.Contains(messages[0], "later startup failure") || strings.Contains(messages[0], "startup.log") {
				t.Fatalf("fatal message must report error without requesting a nonexistent log: %v", messages)
			}
		})
	}
}

func TestInstalledRuntimeChecksCurrentUserAfterMachineReadFailure(t *testing.T) {
	var roots []registry.Key
	available, err := findInstalledRuntime(func(root registry.Key) (string, error) {
		roots = append(roots, root)
		if root == registry.LOCAL_MACHINE {
			return "", registry.ErrUnexpectedType
		}
		return "", registry.ErrNotExist
	})
	if available || err == nil || len(roots) != 2 || roots[1] != registry.CURRENT_USER {
		t.Fatalf("must inspect per-user runtime even when machine registration is unreadable: available=%v err=%v roots=%v", available, err, roots)
	}
}

func TestWindowsInstalledRuntimeSmoke(t *testing.T) {
	available, err := RuntimeAvailable()
	if err != nil {
		t.Fatal(err)
	}
	if !available {
		t.Skip("host has no WebView2 Runtime; no system installer will be run by this test")
	}
	if err := checkRuntime(RuntimeAvailable); err != nil {
		t.Fatalf("installed Runtime should start without running an installer: %v", err)
	}
	t.Log("Real Windows registration and Runtime files detected without installation or logging")
}
