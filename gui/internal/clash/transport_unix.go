//go:build !windows

package clash

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
)

func dialPipe(context.Context, string) (net.Conn, error) {
	return nil, errors.New("named pipes are Windows-only")
}

// defaultControllers covers the socket paths Verge has used across versions and run modes.
func defaultControllers(clashDir string) ([]ControlAPI, error) {
	uid := strconv.Itoa(os.Getuid())
	var found []ControlAPI
	for _, path := range []string{
		filepath.Join(clashDir, "verge-mihomo.sock"),
		filepath.Join(os.TempDir(), "verge-mihomo.sock"),
		filepath.Join("/var/run/clash-verge-service/users", uid, "verge-mihomo.sock"),
		filepath.Join("/run/clash-verge-service/users", uid, "verge-mihomo.sock"),
		"/var/tmp/verge/verge-mihomo.sock",
		"/tmp/verge/verge-mihomo.sock",
	} {
		if info, err := os.Stat(path); err == nil && info.Mode()&os.ModeSocket != 0 {
			found = append(found, ControlAPI{Network: "unix", Address: path})
		}
	}
	return found, nil
}
