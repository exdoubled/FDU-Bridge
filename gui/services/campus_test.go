package services

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mythologyli/zju-connect/gui/internal/clash"
	"github.com/mythologyli/zju-connect/gui/internal/core"
	"github.com/mythologyli/zju-connect/gui/internal/fsx"
	"github.com/mythologyli/zju-connect/gui/internal/rules"
	"github.com/mythologyli/zju-connect/gui/internal/settings"
)

type forbiddenAPI struct{ t *testing.T }

func (a forbiddenAPI) Do(method, path string, payload, out any) error {
	a.t.Fatalf("unexpected controller call: %s %s", method, path)
	return nil
}

func TestOpeningAndClosingDoesNotRepairPreviousSession(t *testing.T) {
	dir := t.TempDir()
	a := clash.CampusAttachment{Root: dir, ProviderPath: filepath.Join(dir, "provider.yaml"), Active: true}
	raw, _ := json.Marshal(a)
	path := filepath.Join(dir, "campus-session.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	b, err := NewBackend(dir, filepath.Join(dir, "gui.exe"))
	if err != nil {
		t.Fatal(err)
	}
	b.conn.API = forbiddenAPI{t}
	if b.Status().Connected || b.Status().State != "paused" {
		t.Fatal("an old session must not be reported connected")
	}
	b.Shutdown()
	if got, _ := os.ReadFile(path); string(got) != string(raw) {
		t.Fatal("opening/closing changed the recovery record")
	}
}

func TestDisconnectCancelsPreparationBeforeWaiting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	b := &Backend{Dir: t.TempDir(), runtime: core.New(t.TempDir()), starting: true, follow: true, cancel: cancel, startDone: done}
	go func() { <-ctx.Done(); close(done) }()
	stopped, err := b.Disconnect()
	if err != nil || !stopped || ctx.Err() == nil {
		t.Fatalf("cancellation did not finish: stopped=%v, error=%v", stopped, err)
	}
	// A later quit must remain possible after the user has closed Clash.
	b.conn = &clash.Connection{API: forbiddenAPI{t}}
	b.campus = &clash.CampusAttachment{Root: b.Dir, ProviderPath: filepath.Join(b.Dir, "previous.yaml")}
	if stopped, err := b.Disconnect(); err != nil || !stopped {
		t.Fatalf("a completed disable tried to detach again: %v", err)
	}
	b.refreshCampus()
}

func TestAppliedRequiresFDUAndClashAcknowledgements(t *testing.T) {
	dir := t.TempDir()
	store, _ := rules.New(filepath.Join(dir, "rules.json"))
	snapshot, err := store.Save(rules.Empty(), "")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"version": 1, "pid": os.Getpid(), "running": true,
		"updated_at": time.Now().UTC().Format(time.RFC3339Nano), "loaded_sha256": snapshot.Hash})
	if err := os.WriteFile(store.Path+".status.json", raw, 0600); err != nil {
		t.Fatal(err)
	}
	b := &Backend{Dir: dir, runtime: core.New(dir), rulesPath: store.Path, enabled: true,
		campus: &clash.CampusAttachment{RulesHash: fsx.Hash([]byte("old rules"))}}
	s := NewRulesService(b)
	if s.Status(snapshot.Hash).Applied {
		t.Fatal("FDU acknowledgement alone is insufficient")
	}
	b.campus.RulesHash = snapshot.Hash
	if status := s.Status(snapshot.Hash); !status.Applied {
		t.Fatalf("matching acknowledgements were rejected: %s", status.Message)
	}
	b.enabled = false
	if s.Status(snapshot.Hash).Applied {
		t.Fatal("disabled integration must not be reported applied")
	}
}

func TestEditingResourcesBeforeFirstEnableKeepsDefaults(t *testing.T) {
	dir := t.TempDir()
	b := &Backend{Dir: dir, view: &settings.View{Values: map[string]any{
		"routing": map[string]any{"domains": []string{"preset.fudan.edu.cn"}, "ip_cidrs": []string{"10.0.0.0/8"}},
	}}}
	s := NewRulesService(b)
	view, err := s.Load()
	if err != nil || len(view.Snapshot.Document.Domains) != 1 || !view.Snapshot.Missing {
		t.Fatalf("missing read-only preset draft: %+v, %v", view, err)
	}
	if _, err := os.Stat(view.Path); !os.IsNotExist(err) {
		t.Fatal("reading resources created the rules file")
	}
	document := view.Snapshot.Document
	document.Domains = append(document.Domains, "added.fudan.edu.cn")
	saved, err := s.Save(document, view.Snapshot.Hash)
	if err != nil || len(saved.Snapshot.Document.Domains) != 2 || len(saved.Snapshot.Document.IPCIDRs) != 1 {
		t.Fatalf("first save lost defaults: %+v, %v", saved, err)
	}
}
