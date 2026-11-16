package services

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mythologyli/zju-connect/gui/internal/appconf"
	"github.com/mythologyli/zju-connect/gui/internal/clash"
)

func TestUsageSwitchRemembersClashAndRejectsActiveChanges(t *testing.T) {
	b := startupBackend(t)
	b.conn.API = forbiddenAPI{t}
	b.config.Integration.Secret = "test-secret"
	original := b.config.Integration
	s := NewClashService(b)
	if _, err := s.SaveUsage("proxy", "global", true); err != nil {
		t.Fatal(err)
	}
	loaded := appconf.New(b.Dir, filepath.Join(b.Dir, "gui.exe")).Load()
	if loaded.Integration.Mode != "proxy" || loaded.ClashIntegration != original || !loaded.StandaloneTUN || loaded.StandaloneMode != "global" {
		t.Fatal("preferences not persisted", loaded)
	}
	if _, err := s.SaveUsage("clash", "global", true); err != nil {
		t.Fatal(err)
	}
	if b.config.Integration != original {
		t.Fatal("lost Clash configuration")
	}
	b.starting = true
	if _, err := s.SaveUsage("proxy", "rule", false); err == nil {
		t.Fatal("changed mode while connecting")
	}
	b.starting = false
	b.campus = &clash.CampusAttachment{Active: true}
	if _, err := s.SaveUsage("proxy", "rule", false); err == nil {
		t.Fatal("discarded pending recovery")
	}
}

func TestStandaloneSharesRulesAcrossAllCombinations(t *testing.T) {
	b := startupBackend(t)
	b.conn.API = forbiddenAPI{t}
	source := filepath.Join(b.Dir, "example.toml")
	if err := os.WriteFile(source, []byte("protocol='atrust'\nserver_address='vpn.invalid'\nserver_port=443\nsocks_bind='127.0.0.1:1080'\nhttp_bind='127.0.0.1:1081'\n[routing]\ndomains=['campus.invalid']\n"), 0600); err != nil {
		t.Fatal(err)
	}
	view, err := b.workspace.Load(source, false)
	if err != nil {
		t.Fatal(err)
	}
	b.setView(view)
	for _, tun := range []bool{false, true} {
		for _, mode := range []string{"rule", "global"} {
			if _, err := NewClashService(b).SaveUsage("proxy", mode, tun); err != nil {
				t.Fatal(err)
			}
			saved, err := b.saveProxyConfig(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if saved.Values["tun_mode"] != tun || text(saved.Values, "routing.upstream_proxy") != "direct" || text(saved.Values, "routing.mode") != mode {
				t.Fatal("wrong standalone config")
			}
			rules, err := NewRulesService(b).Load()
			if err != nil || len(rules.Snapshot.Document.Domains) != 1 || rules.Snapshot.Document.Domains[0] != "campus.invalid" {
				t.Fatal("lost shared rules", err)
			}
		}
	}
}
