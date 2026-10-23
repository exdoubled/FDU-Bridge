package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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
		"gui-settings.json": `{"autoStart":true,"theme":"dark","closeToTray":true,"integration":{"mode":"proxy"}}`,
		"client.toml":       "protocol='atrust'\nserver_address='vpn.invalid'\nclient_data_file='cookies.json'\ndebug_pcap_file='capture.pcap'\n[routing]\nrules_file='rules.json'\n",
		"cookies.json":      `{"device_id":"test-device","sid":"test-session"}`,
		"rules.json":        `{"version":2,"domains":["on.fudan.edu.cn","off.fudan.edu.cn"],"domain_suffixes":[],"ip_cidrs":[],"disabled":{"domains":["off.fudan.edu.cn"],"domain_suffixes":[],"ip_cidrs":[]}}`,
		"unrelated.txt":     "keep",
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
	for _, scenario := range []string{"conflict", "outside", "stale", "active", "inactive", "pending", "invalid journal"} {
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
			case "inactive", "pending", "invalid journal":
				data := map[string]string{"inactive": `{"active":false}`, "pending": `{"pending":true}`, "invalid journal": `{`}[scenario]
				_ = os.WriteFile(filepath.Join(old, "campus-session.json"), []byte(data), 0600)
			}
			p, err := PreviewMigration(old, dest, false)
			if scenario != "conflict" && scenario != "stale" {
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

func TestMigrationPathChecksOnlyManagedRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "gui")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "client.toml")
	if err := os.WriteFile(file, []byte("managed"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(parent, alias); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}
	if err := migrationPath(filepath.Join(alias, "gui"), filepath.Join(alias, "gui", "client.toml"), false); err != nil {
		t.Fatalf("linked ancestor outside managed root: %v", err)
	}
	if err := migrationPath(alias, filepath.Join(alias, "gui", "client.toml"), false); err != nil {
		t.Fatalf("explicitly selected linked root: %v", err)
	}
	inside := filepath.Join(root, "linked")
	if err := os.Symlink(parent, inside); err != nil {
		t.Fatal(err)
	}
	if err := migrationPath(root, filepath.Join(inside, "gui", "client.toml"), false); err == nil {
		t.Fatal("accepted a link inside managed root")
	}
}

func TestMigrationPreservesPreferenceDefaultsAndExplicitFalse(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "explicit false"}[explicit], func(t *testing.T) {
			old, dest := migrationFixture(t)
			prefs := `{}`
			if explicit {
				prefs = `{"checkUpdates":false,"closeToTray":false}`
			}
			_ = os.WriteFile(filepath.Join(old, "gui-settings.json"), []byte(prefs), 0600)
			p, err := PreviewMigration(old, dest, false)
			if err != nil {
				t.Fatal(err)
			}
			_, got, err := p.Apply()
			if err != nil {
				t.Fatal(err)
			}
			if got.Theme != "system" || got.CheckUpdates != !explicit || got.CloseToTray != (!explicit && runtime.GOOS != "linux") || got.ClashDir == "" {
				t.Fatalf("lost preference defaults or explicit false: %+v", got)
			}
		})
	}
}

func TestMigrationMissingRulesPreservesDestination(t *testing.T) {
	old, dest := migrationFixture(t)
	_ = os.Remove(filepath.Join(old, "rules.json"))
	_ = os.MkdirAll(dest, 0700)
	target := filepath.Join(dest, "routing-rules.json")
	const personal = `{"version":1,"domains":["personal.invalid"]}`
	_ = os.WriteFile(target, []byte(personal), 0600)
	p, err := PreviewMigration(old, dest, false)
	if err != nil {
		t.Fatal(err)
	}
	view, _, err := p.Apply()
	if err != nil || view.Values["routing"].(map[string]any)["rules_file"] != target || read(t, target) != personal {
		t.Fatalf("missing source rules must preserve destination: %v", err)
	}
}

func TestMigrationCopiesPrivateAssetsAndPreservesExternalReferences(t *testing.T) {
	old, dest := migrationFixture(t)
	asset := filepath.Join(old, "assets", "client.p12")
	_ = os.MkdirAll(filepath.Dir(asset), 0700)
	_ = os.WriteFile(asset, []byte("private certificate"), 0600)
	external := filepath.Join(t.TempDir(), "resources.json")
	_ = os.WriteFile(external, []byte("external resources"), 0600)
	config := filepath.Join(old, "client.toml")
	raw := read(t, config)
	raw = "cert_file='assets/client.p12'\nresource_file='" + filepath.ToSlash(external) + "'\n" + raw
	_ = os.WriteFile(config, []byte(raw), 0600)
	p, err := PreviewMigration(old, dest, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Files) != 5 {
		t.Fatalf("asset missing from preview: %v", p.Files)
	}
	view, _, err := p.Apply()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dest, "assets", "client.p12")
	if view.Values["cert_file"] != want || read(t, want) != "private certificate" || filepath.Clean(view.Values["resource_file"].(string)) != filepath.Clean(external) || read(t, asset) != "private certificate" {
		t.Fatalf("asset migration lost private or external reference: %v", view.Values)
	}
}

func TestMigrationRejectsAssetBackupCollision(t *testing.T) {
	old, dest := migrationFixture(t)
	asset := filepath.Join(old, "client.toml.bak")
	_ = os.WriteFile(asset, []byte("certificate"), 0600)
	config := filepath.Join(old, "client.toml")
	_ = os.WriteFile(config, []byte("cert_file='client.toml.bak'\n"+read(t, config)), 0600)
	if _, err := PreviewMigration(old, dest, false); err == nil {
		t.Fatal("accepted resource that would be overwritten by a migration backup")
	}
}
