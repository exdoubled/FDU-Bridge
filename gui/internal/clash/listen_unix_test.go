//go:build !windows

package clash

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func listenControl(t *testing.T, _ string) (net.Listener, ControlAPI) {
	// Keep the path short: macOS limits socket paths to 104 bytes.
	dir, err := os.MkdirTemp("", "fdu")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "c.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	return l, ControlAPI{Network: "unix", Address: path}
}
