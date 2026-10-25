package bootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckRuntimeDirectsOnlyDefiniteAbsenceToSetup(t *testing.T) {
	for _, tc := range []struct {
		name      string
		available bool
		probeErr  error
	}{
		{name: "installed", available: true},
		{name: "not installed"},
		{name: "registration unreadable", probeErr: errors.New("runtime registration unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checks := 0
			err := newStartup(nil).checkRuntime(func() (bool, error) {
				checks++
				return tc.available, tc.probeErr
			})
			if checks != 1 {
				t.Fatalf("startup runtime checks = %d, want exactly one detection", checks)
			}
			if tc.available || tc.probeErr != nil {
				if err != nil {
					t.Fatalf("allow Wails to attempt startup: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "*-Windows-x64-setup.exe") {
				t.Fatalf("missing runtime must direct user to installer: %v", err)
			}
		})
	}
}

func TestRuntimeProbeErrorIsLoggedWithoutBlockingStartup(t *testing.T) {
	s, err := openStartup(t.TempDir(), func(string) { t.Fatal("uncertain detection opened a dialog") })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	want := errors.New("registration access denied")
	if err := s.checkRuntime(func() (bool, error) { return false, want }); err != nil {
		t.Fatalf("uncertain detection must let Wails attempt startup: %v", err)
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "level=WARN") || !strings.Contains(string(data), want.Error()) {
		t.Fatalf("detection warning missing from startup log: %s", data)
	}
}

func TestRuntimeDiscoveryMatchesLoaderFilesAndVersions(t *testing.T) {
	for _, tc := range []struct {
		name, version string
		missingDLL    bool
		want          bool
	}{
		{"registered DLL without browser or PE inspection", "140.0.1.2", false, true},
		{"short version", "140", false, true},
		{"three-part version", "140.0.1", false, true},
		{"version with channel", "140.0.1.2 beta", false, true},
		{"missing DLL", "140.0.1.2", true, false},
		{"zero version", "0.0.0.0", false, false},
		{"obsolete version", "85.0.1.2", false, false},
		{"below loader minimum", "86.0.615.0", false, false},
		{"equal loader minimum", "86.0.616.0", false, true},
		{"implicit minimum fourth part", "86.0.616", false, true},
		{"above minimum build", "86.0.616.1", false, true},
		{"above minimum minor", "86.1.0.0", false, true},
		{"invalid version", "invalid", false, false},
		{"too many version parts", "140.0.1.2.3", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			folder := loaderRuntimeFixture(t, tc.version)
			if tc.missingDLL {
				if err := os.Remove(filepath.Join(folder, "EBWebView", "x64", "EmbeddedBrowserWebView.dll")); err != nil {
					t.Fatal(err)
				}
			}
			if got, err := runtimePresent(folder, "amd64"); got != tc.want || err != nil {
				t.Fatalf("runtime discovered = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestRuntimeDiscoveryResolvesRelativeRegistrationLikeLoader(t *testing.T) {
	folder := loaderRuntimeFixture(t, "140.0.1.2")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(filepath.Dir(executable), folder)
	if err != nil {
		t.Skip(err)
	}
	if available, err := runtimePresent(relative, "amd64"); !available || err != nil {
		t.Fatalf("relative registration must resolve from the executable directory: %v, %v", available, err)
	}
}

func TestRuntimeDiscoveryKeepsFileErrorsUncertain(t *testing.T) {
	folder := filepath.Join(t.TempDir()+"\x00", "140.0.1.2")
	available, err := runtimePresent(folder, "amd64")
	if available || err == nil {
		t.Fatalf("file inspection failure must remain uncertain: %v, %v", available, err)
	}
}

func loaderRuntimeFixture(t *testing.T, version string) string {
	t.Helper()
	folder := filepath.Join(t.TempDir(), version)
	dll := filepath.Join(folder, "EBWebView", "x64", "EmbeddedBrowserWebView.dll")
	if err := os.MkdirAll(filepath.Dir(dll), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dll, []byte("DLL discovery does not inspect executable contents"), 0600); err != nil {
		t.Fatal(err)
	}
	return folder
}
