// Package appconf stores GUI preferences in the GUI data directory.
package appconf

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/mythologyli/zju-connect/gui/internal/fsx"
)

type Config struct {
	Theme       string `json:"theme"` // system, light or dark
	AutoStart   bool   `json:"autoStart"`
	AutoConnect bool   `json:"autoConnect"`
	CloseToTray bool   `json:"closeToTray"`
	ClashDir    string `json:"clashDir"`
	// FduConfig is the core config Clash integration targets; it may live outside the GUI directory.
	FduConfig string `json:"fduConfig"`
	// ConfigPath is the GUI-owned config the settings page saves and connections use.
	ConfigPath string `json:"configPath"`
	CampusConsent bool `json:"campusConsent"`
}

type Store struct {
	path string
	exe  string
}

func New(directory, executable string) *Store {
	return &Store{filepath.Join(directory, "gui-settings.json"), executable}
}

const vergeID = "io.github.clash-verge-rev.clash-verge-rev"

// DefaultClashDir is Clash Verge Rev's data directory (Tauri's app data dir).
func DefaultClashDir() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(os.Getenv("APPDATA"), vergeID)
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", vergeID)
	}
	if data := os.Getenv("XDG_DATA_HOME"); data != "" {
		return filepath.Join(data, vergeID)
	}
	return filepath.Join(home, ".local", "share", vergeID)
}

// DataDir is where the GUI keeps its config, rules and login state. Windows stays portable
// beside the executable; macOS app bundles and Linux installs are not writable, so they use
// the per-user locations instead.
func DataDir(executableDir string) string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		return executableDir
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "FDU Connect")
	}
	if config := os.Getenv("XDG_CONFIG_HOME"); config != "" {
		return filepath.Join(config, "fdu-connect")
	}
	return filepath.Join(home, ".config", "fdu-connect")
}

func (s *Store) defaults() Config {
	// Many Linux desktops hide tray icons, so a hidden window would look like a crash there.
	return Config{Theme: "system", CloseToTray: runtime.GOOS != "linux", ClashDir: DefaultClashDir()}
}

func (s *Store) Load() Config {
	c := s.defaults()
	if b, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	if c.Theme != "light" && c.Theme != "dark" {
		c.Theme = "system"
	}
	if c.ClashDir == "" {
		c.ClashDir = DefaultClashDir()
	}
	c.AutoStart = s.autoStartEnabled()
	return c
}

func (s *Store) Save(c Config) error {
	if c.Theme != "system" && c.Theme != "light" && c.Theme != "dark" {
		return errors.New("未知的主题")
	}
	if err := s.setAutoStart(c.AutoStart); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWrite(s.path, b)
}
