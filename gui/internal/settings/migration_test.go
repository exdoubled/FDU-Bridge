package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mythologyli/zju-connect/gui/internal/fsx"
	"github.com/mythologyli/zju-connect/routing"
)

func migrationFixture(t *testing.T) (string, string) {
	t.Helper()
	old := filepath.Join(t.TempDir(), "old")
	dest := filepath.Join(t.TempDir(), "new")
	_ = os.MkdirAll(old, 0700)
	for name, data := range map[string]string{
		"gui-settings.json":   `{"autoStart":true,"theme":"dark","closeToTray":true,"integration":{"mode":"proxy"}}`,
		"client.toml":         "protocol='atrust'\nserver_address='vpn.invalid'\nclient_data_file='cookies.json'\ndebug_pcap_file='capture.pcap'\n[routing]\nrules_file='rules.json'\n",
		"cookies.json":        `{"device_id":"test-device","sid":"test-session"}`,
		"rules.json":          `{"version":2,"domains":["on.fudan.edu.cn","off.fudan.edu.cn"],"domain_suffixes":[],"ip_cidrs":[],"disabled":{"domains":["off.fudan.edu.cn"],"domain_suffixes":[],"ip_cidrs":[]}}`,
		"campus-session.json": `{"active":false,"root":"ignored"}`,
		"unrelated.txt":       "keep",
	} {
		if err := os.WriteFile(filepath.Join(old, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return old, dest
}

func TestMigrationCopiesManagedDataAndRetainsSource(t *testing.T) {
	old, dest := migrationFixture(t)
	sourceHash := fsx.HashFile(filepath.Join(old, "client.toml"))
	preview, err := PreviewMigration(old, dest, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Files) != 4 || len(preview.Conflicts) != 0 {
		t.Fatalf("preview: %+v", preview)
	}
	if _, err = os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("preview wrote destination")
	}
	view, prefs, err := preview.Apply()
	if err != nil {
		t.Fatal(err)
	}
	if prefs.Theme != "dark" || prefs.AutoStart || view.Dirty || view.Values["client_data_file"] != filepath.Join(dest, "client_data.json") || view.Values["routing"].(map[string]any)["rules_file"] != filepath.Join(dest, "routing-rules.json") {
		t.Fatal("managed settings not rebased")
	}
	if read(t, filepath.Join(dest, "client_data.json")) != read(t, filepath.Join(old, "cookies.json")) {
		t.Fatal("lost login state")
	}
	rules, err := routing.ParseRules([]byte(read(t, filepath.Join(dest, "routing-rules.json"))))
	if err != nil || rules.Disabled == nil || len(rules.Disabled.Domains) != 1 {
		t.Fatal("lost disabled rules")
	}
	if fsx.HashFile(filepath.Join(old, "client.toml")) != sourceHash {
		t.Fatal("changed source")
	}
	for _, name := range []string{"campus-session.json", "unrelated.txt"} {
		if _, err = os.Stat(filepath.Join(dest, name)); !os.IsNotExist(err) {
			t.Fatal("copied unmanaged data")
		}
	}
	var decoded map[string]any
	if json.Unmarshal([]byte(read(t, filepath.Join(dest, "gui-settings.json"))), &decoded) != nil || decoded["autoStart"] != false {
		t.Fatal("invalid preferences")
	}
}

func TestMigrationRejectsConflictsExternalFilesAndStalePreview(t *testing.T) {
	for _, scenario := range []string{"conflict", "outside", "stale", "active"} {
		t.Run(scenario, func(t *testing.T) {
			old, dest := migrationFixture(t)
			switch scenario {
			case "conflict":
				_ = os.MkdirAll(dest, 0700)
				_ = os.WriteFile(filepath.Join(dest, "client_data.json"), []byte("personal-state"), 0600)
			case "outside":
				_ = os.WriteFile(filepath.Join(old, "client.toml"), []byte("protocol='atrust'\nserver_address='vpn.invalid'\nclient_data_file='../external.json'\n"), 0600)
			case "active":
				_ = os.WriteFile(filepath.Join(old, "campus-session.json"), []byte(`{"active":true}`), 0600)
			}
			p, err := PreviewMigration(old, dest, false)
			if scenario == "outside" || scenario == "active" {
				if err == nil {
					t.Fatal("accepted unsafe source")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "stale" {
				_ = os.WriteFile(filepath.Join(old, "cookies.json"), []byte(`{"changed":true}`), 0600)
			}
			if _, _, err = p.Apply(); err == nil {
				t.Fatal("accepted conflict or stale preview")
			}
			if scenario == "conflict" && read(t, filepath.Join(dest, "client_data.json")) != "personal-state" {
				t.Fatal("overwrote destination")
			}
			if _, err = os.Stat(filepath.Join(dest, "client.toml")); !os.IsNotExist(err) {
				t.Fatal("partial migration")
			}
		})
	}
}

func TestMigrationPreservesDifferentDestinationBackup(t *testing.T) {
	old, dest := migrationFixture(t)
	p, err := PreviewMigration(old, dest, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = p.Apply(); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dest, "client.toml.bak")
	if err = os.WriteFile(backup, []byte("personal backup"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err = PreviewMigration(old, dest, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Conflicts) != 1 || p.Conflicts[0] != backup {
		t.Fatalf("backup not previewed as conflict: %v", p.Conflicts)
	}
	if _, _, err = p.Apply(); err == nil || read(t, backup) != "personal backup" {
		t.Fatal("overwrote destination backup")
	}
}
