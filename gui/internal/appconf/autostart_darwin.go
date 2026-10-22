package appconf

import (
	"bytes"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
)

const launchAgentLabel = "cn.edu.fudan.fdu-connect.gui"

func launchAgentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
}

func xmlText(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// launchAgent starts the GUI hidden at login; main.go recognizes --minimized.
func (s *Store) launchAgent() []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + launchAgentLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + xmlText(s.exe) + `</string>
		<string>--minimized</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
</dict>
</plist>
`)
}

func (s *Store) autoStartEnabled() bool {
	b, err := os.ReadFile(launchAgentPath())
	return err == nil && bytes.Equal(b, s.launchAgent())
}

func (s *Store) setAutoStart(enabled bool) error {
	if enabled == s.autoStartEnabled() {
		return nil
	}
	if !enabled {
		if err := os.Remove(launchAgentPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(launchAgentPath()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(launchAgentPath(), s.launchAgent(), 0o644)
}
