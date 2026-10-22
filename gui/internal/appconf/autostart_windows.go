package appconf

import (
	"errors"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const (
	runKey   = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValue = "FDU Connect"
)

func (s *Store) autoStartCommand() string { return `"` + s.exe + `" --minimized` }

func (s *Store) autoStartEnabled() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	value, _, err := key.GetStringValue(runValue)
	return err == nil && strings.EqualFold(value, s.autoStartCommand())
}

func (s *Store) setAutoStart(enabled bool) error {
	if enabled == s.autoStartEnabled() {
		return nil
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if enabled {
		return key.SetStringValue(runValue, s.autoStartCommand())
	}
	if err := key.DeleteValue(runValue); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}
