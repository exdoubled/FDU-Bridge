package services

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mythologyli/zju-connect/gui/internal/settings"
)

func TestMaintenanceRefusesLifecycleAndDraftChanges(t *testing.T) {
	b, err := NewBackend(t.TempDir(), filepath.Join(t.TempDir(), "gui.exe"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewMaintenanceService(b)
	for _, state := range []string{"starting", "stopping", "updating", "draft", "busy update", "external"} {
		t.Run(state, func(t *testing.T) {
			b.starting, b.stopping, b.updating, b.external = false, false, false, false
			b.view = nil
			b.updater = nil
			switch state {
			case "starting":
				b.starting = true
			case "stopping":
				b.stopping = true
			case "updating":
				b.updating = true
			case "draft":
				b.view = &settings.View{Dirty: true}
			case "busy update":
				b.updater = &UpdateService{busy: true}
			case "external":
				b.external = true
			}
			if _, err := s.RemoveIntegration(); err == nil {
				t.Fatal("maintenance accepted locked state")
			}
			if entries, _ := os.ReadDir(b.Dir); len(entries) != 0 {
				t.Fatal("locked maintenance wrote files")
			}
		})
	}
	b.starting, b.stopping, b.updating, b.external = false, false, false, false
	b.updater = nil
	b.view = &settings.View{Dirty: true, Source: filepath.Join(b.exeDir, "config.toml.example")}
	if s.editable(true) != nil || s.editable(false) == nil {
		t.Fatal("fresh example exception must apply to migration only")
	}
}

func TestMigrationServiceDoesNotCopyDestinationRecoveryJournal(t *testing.T) {
	dir := t.TempDir()
	b, err := NewBackend(dir, filepath.Join(t.TempDir(), "gui.exe"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "campus-session.json")
	if err = os.WriteFile(path, []byte(`{"active":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewMaintenanceService(b)
	if _, err = s.previewDirectory(t.TempDir()); err == nil {
		t.Fatal("migration accepted destination recovery journal")
	}
	if raw, _ := os.ReadFile(path); string(raw) != `{"active":false}` {
		t.Fatal("changed destination recovery journal")
	}
}
