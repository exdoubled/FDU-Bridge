package bootstrap

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

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
