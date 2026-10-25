package services

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mythologyli/zju-connect/gui/internal/appconf"
	"github.com/mythologyli/zju-connect/gui/internal/bootstrap"
	"github.com/mythologyli/zju-connect/gui/internal/clash"
	"github.com/mythologyli/zju-connect/gui/internal/fsx"
	"github.com/mythologyli/zju-connect/gui/internal/rules"
	"github.com/mythologyli/zju-connect/gui/internal/settings"
	"github.com/mythologyli/zju-connect/gui/internal/updates"
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

func TestRulesExportProtectsManagedTreesWithoutClashLock(t *testing.T) {
	dir, clashDir := t.TempDir(), t.TempDir()
	b := &Backend{Dir: dir, config: appconf.Config{ClashDir: clashDir}}
	s := NewRulesService(b)
	for _, path := range []string{filepath.Join(dir, "connection-backups", "saved", "previous.yaml"), filepath.Join(clashDir, "profiles", "other.yaml")} {
		if _, err := s.exportFile(path, rules.Empty()); err == nil {
			t.Errorf("accepted managed tree destination: %s", path)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("created managed destination: %v", err)
		}
	}
	b.clashBusy.Lock()
	defer b.clashBusy.Unlock()
	done := make(chan error, 1)
	output := filepath.Join(t.TempDir(), "export.json")
	go func() {
		_, err := s.exportFile(output, rules.Empty())
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("export waited for a Clash transaction")
	}
}

func TestLogsAndRulesShareExportProtection(t *testing.T) {
	dir, stage := t.TempDir(), t.TempDir()
	b := &Backend{Dir: dir, logs: []string{"redacted log"}}
	b.updater = &UpdateService{pending: &updates.Pending{Root: stage}}
	logs, resources := NewCoreService(b), NewRulesService(b)
	for _, path := range []string{filepath.Join(dir, "client.toml"), filepath.Join(stage, "job.json"), filepath.Join(stage, "payload", "fdu-connect.exe")} {
		if _, err := logs.exportLogs(path); err == nil {
			t.Errorf("log export accepted managed destination: %s", path)
		}
		if _, err := resources.exportFile(path, rules.Empty()); err == nil {
			t.Errorf("rule export accepted managed destination: %s", path)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("export created managed destination: %v", err)
		}
	}
	output := filepath.Join(t.TempDir(), "log.txt")
	if _, err := logs.exportLogs(output); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(output); err != nil || string(data) != "redacted log\r\n" {
		t.Fatalf("exported logs: %q %v", data, err)
	}
}

func TestExportsAllowDownloadsBesidePortableFiles(t *testing.T) {
	downloads := filepath.Join(t.TempDir(), "Downloads")
	executable := filepath.Join(downloads, "Renamed-FDU.exe")
	b, err := NewBackend(downloads, executable)
	if err != nil {
		t.Fatal(err)
	}
	b.logs = []string{"redacted log"}
	logs, resources := NewCoreService(b), NewRulesService(b)
	for _, path := range []string{filepath.Join(downloads, "campus-export.json"), filepath.Join(downloads, "reports", "campus.json")} {
		if _, err := resources.exportFile(path, rules.Empty()); err != nil {
			t.Fatalf("cannot export beside portable files: %v", err)
		}
		if _, err := logs.exportLogs(path + ".log"); err != nil {
			t.Fatalf("cannot export logs beside portable files: %v", err)
		}
	}
	for _, path := range []string{executable, b.runtime.Core, filepath.Join(downloads, "config.toml.example"), filepath.Join(downloads, "client_data.json"), filepath.Join(downloads, "client_data.json.bak"), filepath.Join(downloads, "gui-settings.json")} {
		if err := os.WriteFile(path, []byte("private"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := resources.exportFile(path, rules.Empty()); err == nil {
			t.Fatalf("rule export accepted portable program/state file: %s", path)
		}
		if _, err := logs.exportLogs(path); err == nil {
			t.Fatalf("log export accepted portable program/state file: %s", path)
		}
		if data, _ := os.ReadFile(path); string(data) != "private" {
			t.Fatalf("overwrote portable file: %s", path)
		}
	}
}

func TestAllExportsProtectBundledWebView2Installer(t *testing.T) {
	b := &Backend{Dir: t.TempDir(), exeDir: t.TempDir(), logs: []string{"log"}}
	network := NewNetworkService(b)
	network.saveDiagnosis(CampusDiagnosis{})
	for _, dir := range []string{b.Dir, b.exeDir} {
		path := filepath.Join(dir, "MicrosoftEdgeWebView2RuntimeInstallerX64.exe")
		if err := os.WriteFile(path, []byte("installer"), 0o600); err != nil {
			t.Fatal(err)
		}
		for name, export := range map[string]func() error{
			"rules": func() error { _, err := NewRulesService(b).exportFile(path, rules.Empty()); return err },
			"logs":  func() error { _, err := NewCoreService(b).exportLogs(path); return err },
			"report": func() error {
				_, err := network.exportDiagnosisReport(func() (string, error) { return path, nil })
				return err
			},
		} {
			if err := export(); err == nil {
				t.Errorf("%s export accepted installer destination %s", name, path)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != "installer" {
				t.Fatalf("%s export changed installer: %q, %v", name, data, err)
			}
		}
	}
}

func TestExportPathsProtectsBootstrapStartupLog(t *testing.T) {
	path, err := bootstrap.LogPath()
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Skip("this platform has no bootstrap startup log")
	}
	b := &Backend{Dir: t.TempDir(), exeDir: t.TempDir()}
	files, directories := b.exportPaths()
	found := false
	for _, file := range files {
		found = found || file == path
	}
	if !found {
		t.Fatal("shared report, log and rule export protection omits bootstrap log")
	}
	// Exercise the shared guard without opening or writing the user's log.
	if _, err := fsx.ExportPath(path, files, directories); err == nil {
		t.Fatal("shared export guard accepted bootstrap log")
	}
}
