package services

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mythologyli/zju-connect/gui/internal/appconf"
	"github.com/mythologyli/zju-connect/gui/internal/clash"
	"github.com/mythologyli/zju-connect/gui/internal/rules"
	"github.com/mythologyli/zju-connect/gui/internal/settings"
)

func TestRulesImportPreviewAndCancellationDoNotSave(t *testing.T) {
	dir := t.TempDir()
	s := NewRulesService(&Backend{Dir: dir})
	store, _ := s.store()
	if _, err := store.Save(rules.Empty(), ""); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(store.Path)
	input := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(input, []byte(`{"version":2,"domains":["LAB.CN."],"domain_suffixes":[],"ip_cidrs":[],"disabled":{"domains":["lab.cn"],"domain_suffixes":[],"ip_cidrs":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	preview, err := s.importFile(input)
	if err != nil || preview == nil || preview.Name != "input.json" || len(preview.Document.Disabled.Domains) != 1 || preview.Document.Domains[0] != "lab.cn" {
		t.Fatalf("invalid preview: %+v %v", preview, err)
	}
	if preview, err := s.importFile(""); err != nil || preview != nil {
		t.Fatalf("cancelled import: %+v %v", preview, err)
	}
	if path, err := s.exportFile("", rules.Empty()); err != nil || path != "" {
		t.Fatalf("cancelled export: %q %v", path, err)
	}
	if after, _ := os.ReadFile(store.Path); string(after) != string(before) {
		t.Fatal("preview or cancellation saved rules")
	}
}

func TestRulesExportProtectsManagedAndActiveExternalFiles(t *testing.T) {
	dir, external := t.TempDir(), t.TempDir()
	w, _ := settings.New(dir)
	b := &Backend{Dir: dir, workspace: w, conn: &clash.Connection{},
		config:    appconf.Config{FduConfig: filepath.Join(external, "fdu.toml"), Integration: appconf.Integration{RuntimeFile: filepath.Join(external, "runtime.yaml")}},
		rulesPath: filepath.Join(external, "rules.json"),
		saved:     map[string]any{"client_data_file": filepath.Join(external, "login.json")},
		campus:    &clash.CampusAttachment{ProviderPath: filepath.Join(external, "provider.yaml")}}
	s := NewRulesService(b)
	for _, path := range []string{filepath.Join(dir, "gui-settings.json"), filepath.Join(dir, "campus-session.json"), filepath.Join(dir, "campus-feed.json"), w.Target,
		b.rulesPath, b.rulesPath + ".status.json", b.config.FduConfig, b.config.Integration.RuntimeFile, b.saved["client_data_file"].(string), b.campus.ProviderPath} {
		if err := os.WriteFile(path, []byte("private"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.exportFile(path, rules.Empty()); err == nil {
			t.Fatalf("export accepted managed destination %s", path)
		}
		if data, _ := os.ReadFile(path); string(data) != "private" {
			t.Fatalf("export overwrote %s", path)
		}
	}
	d := rules.Empty()
	d.Domains = []string{"draft.cn"}
	d.Disabled.Domains = []string{"draft.cn"}
	output := filepath.Join(t.TempDir(), "campus.json")
	if path, err := s.exportFile(output, d); err != nil || path != output {
		t.Fatalf("export draft: %q %v", path, err)
	}
	got, err := rules.ImportFile(output)
	if err != nil || len(got.Disabled.Domains) != 1 || got.Domains[0] != "draft.cn" {
		t.Fatalf("export lost unsaved rule or switch: %+v %v", got, err)
	}
}
