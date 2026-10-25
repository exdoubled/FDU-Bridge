package bootstrap

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckRuntimeDirectsMissingRuntimeToSetup(t *testing.T) {
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
			err := checkRuntime(func() (bool, error) {
				checks++
				return tc.available, tc.probeErr
			})
			if checks != 1 {
				t.Fatalf("startup runtime checks = %d, want exactly one detection", checks)
			}
			if tc.available {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "*-Windows-x64-setup.exe") {
				t.Fatalf("missing runtime must direct user to installer: %v", err)
			}
			if tc.probeErr != nil && !errors.Is(err, tc.probeErr) {
				t.Fatalf("runtime detection error was lost: %v", err)
			}
		})
	}
}

func TestRuntimePresentRequiresMatchingRuntimeFiles(t *testing.T) {
	for _, tc := range []struct {
		name, version string
		dll, browser  bool
		machine       uint16
		want          bool
	}{
		{"runtime", "140.0.1.2", true, true, 0x8664, true},
		{"browser without runtime DLL", "140.0.1.2", false, true, 0x8664, false},
		{"DLL without browser", "140.0.1.2", true, false, 0x8664, false},
		{"wrong architecture", "140.0.1.2", true, true, 0x014c, false},
		{"zero version", "0.0.0.0", true, true, 0x8664, false},
		{"obsolete version", "85.0.1.2", true, true, 0x8664, false},
		{"below loader minimum", "86.0.615.0", true, true, 0x8664, false},
		{"equal loader minimum", "86.0.616.0", true, true, 0x8664, true},
		{"above loader minimum build", "86.0.616.1", true, true, 0x8664, true},
		{"above loader minimum minor", "86.1.0.0", true, true, 0x8664, true},
		{"invalid version", "invalid", true, true, 0x8664, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			folder := filepath.Join(t.TempDir(), tc.version)
			if err := os.MkdirAll(filepath.Join(folder, "EBWebView", "x64"), 0700); err != nil {
				t.Fatal(err)
			}
			if tc.browser {
				if err := os.WriteFile(filepath.Join(folder, "msedgewebview2.exe"), []byte("runtime browser"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.dll {
				data := make([]byte, 128)
				copy(data, "MZ")
				binary.LittleEndian.PutUint32(data[0x3c:], 96)
				copy(data[96:], "PE\x00\x00")
				binary.LittleEndian.PutUint16(data[100:], tc.machine)
				binary.LittleEndian.PutUint16(data[118:], 0x2000)
				if err := os.WriteFile(filepath.Join(folder, "EBWebView", "x64", "EmbeddedBrowserWebView.dll"), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if got := runtimePresent(folder, "amd64"); got != tc.want {
				t.Fatalf("runtime present = %v, want %v", got, tc.want)
			}
		})
	}
}
