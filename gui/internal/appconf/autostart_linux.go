package appconf

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func autostartPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "autostart", "fdu-connect.desktop")
}

// desktopEntry starts the GUI hidden at login; main.go recognizes --minimized.
func (s *Store) desktopEntry() []byte {
	exe := `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", "$", `\$`).Replace(s.exe) + `"`
	return []byte("[Desktop Entry]\nType=Application\nName=FDU Connect\nExec=" + exe + " --minimized\nX-GNOME-Autostart-enabled=true\nTerminal=false\n")
}

func (s *Store) autoStartEnabled() bool {
	b, err := os.ReadFile(autostartPath())
	return err == nil && bytes.Equal(b, s.desktopEntry())
}

func (s *Store) setAutoStart(enabled bool) error {
	if enabled == s.autoStartEnabled() {
		return nil
	}
	if !enabled {
		if err := os.Remove(autostartPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(autostartPath()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(autostartPath(), s.desktopEntry(), 0o644)
}
