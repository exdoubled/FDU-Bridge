package services

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mythologyli/zju-connect/gui/internal/updates"
)

func TestUpdatePreferencesAndInstallGuard(t *testing.T) {
	dir := t.TempDir()
	b, err := NewBackend(dir, filepath.Join(dir, "FDU-Connect-GUI.exe"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewUpdateService(b)
	defer service.cancel()
	if !service.Status().AutoCheck {
		t.Fatal("automatic checks must default on for existing installations")
	}
	if _, err := service.SetAutoCheck(false); err != nil {
		t.Fatal(err)
	}
	if b.conf.Load().CheckUpdates || service.Status().AutoCheck {
		t.Fatal("automatic check preference was not persisted")
	}
	service.pending = &updates.Pending{}
	b.starting = true
	quitCalled := false
	b.Quit = func(bool) (bool, error) { quitCalled = true; return true, nil }
	if service.Install() == nil || quitCalled || b.updating {
		t.Fatal("installation must not start or quit during a connection attempt")
	}
	b.starting, b.updating = false, true
	if NewCoreService(b).RunTool("auth-info") == nil {
		t.Fatal("tools must not start once installation is committed")
	}
	// The update preference write must never create or replace a core config.
	if _, err := os.Stat(filepath.Join(dir, "client.toml")); !os.IsNotExist(err) {
		t.Fatal("updater changed the core configuration")
	}
}
