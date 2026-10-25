package bootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestRuntimeDiscoveryChecksChannelsInLoaderOrder(t *testing.T) {
	channels := []string{
		"{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}",
		"{2CD8A007-E189-409D-A2C8-9AF4EF3C72AA}",
		"{0D50BFEC-CD6A-4F9A-964C-C7416E3ACB10}",
		"{65C35B14-6C1D-4122-AC46-7148CC9D6497}",
	}
	type query struct {
		root registry.Key
		path string
	}
	var actual, expected []query
	for _, channel := range channels {
		for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
			expected = append(expected, query{root, `Software\Microsoft\EdgeUpdate\ClientState\` + channel})
		}
	}
	available, err := findInstalledRuntime(func(root registry.Key, path string) (string, error) {
		actual = append(actual, query{root, path})
		return "", registry.ErrNotExist
	})
	if available || err != nil || !reflect.DeepEqual(actual, expected) {
		t.Fatalf("discovery queries = %#v, want %#v; available=%v err=%v", actual, expected, available, err)
	}
	for _, channel := range channels {
		t.Run(channel, func(t *testing.T) {
			folder := loaderRuntimeFixture(t, "140.0.1.2")
			available, err := findInstalledRuntime(func(root registry.Key, path string) (string, error) {
				if root == registry.CURRENT_USER && strings.HasSuffix(path, channel) {
					return folder, nil
				}
				return "", registry.ErrNotExist
			})
			if !available || err != nil {
				t.Fatalf("registered loader channel rejected: %v, %v", available, err)
			}
		})
	}
}

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
			if err := s.checkRuntime(func() (bool, error) { return true, nil }); err != nil {
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
	available, err := findInstalledRuntime(func(root registry.Key, _ string) (string, error) {
		roots = append(roots, root)
		if root == registry.LOCAL_MACHINE {
			return "", registry.ErrUnexpectedType
		}
		return "", registry.ErrNotExist
	})
	if available || err == nil || len(roots) < 2 || roots[1] != registry.CURRENT_USER {
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
	if err := newStartup(nil).checkRuntime(RuntimeAvailable); err != nil {
		t.Fatalf("installed Runtime should start without running an installer: %v", err)
	}
	t.Log("Real Windows registration and Runtime files detected without installation or logging")
}
