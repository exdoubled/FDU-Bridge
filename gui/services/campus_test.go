package services

import (
	"context"
	"encoding/json"
	"errors"
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

func TestFailedEnableCanQuitWithoutRecoveringAnUnownedSession(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "previously-disabled", true: "previous-recovery-pending"}[active], func(t *testing.T) {
			dir := t.TempDir()
			a := clash.CampusAttachment{Root: dir, ProviderPath: filepath.Join(dir, "previous.yaml"), Active: active}
			raw, _ := json.Marshal(a)
			journal := filepath.Join(dir, "campus-session.json")
			if err := os.WriteFile(journal, raw, 0600); err != nil {
				t.Fatal(err)
			}
			b, err := NewBackend(dir, filepath.Join(dir, "gui.exe"))
			if err != nil {
				t.Fatal(err)
			}
			b.conn.API = forbiddenAPI{t}
			b.conn.ConfigureTarget(filepath.Join(dir, "other-client.yaml"), nil)
			b.config.Integration.Mode, b.config.Integration.RuntimeFile = "manual", filepath.Join(dir, "missing.yaml")
			if !active {
				if err := b.detachCampus(); err != nil {
					t.Fatalf("inactive history blocked new client: %v", err)
				}
			}
			done := make(chan struct{})
			b.starting, b.startDone = true, done
			b.startCampus(context.Background(), done, false, false)
			if b.startDone != nil || b.runtime.Running() {
				t.Fatal("failed preflight retained a fictitious owned session")
			}
			if stopped, err := b.Disconnect(); err != nil || !stopped {
				t.Fatalf("quit after failed enable: %v", err)
			}
			if stopped, err := b.disconnect(true); err != nil || !stopped {
				t.Fatalf("force quit after failed enable: %v", err)
			}
			if got, _ := os.ReadFile(journal); string(got) != string(raw) {
				t.Fatal("rewrote unrelated recovery history")
			}
		})
	}
}

func TestDisconnectStillProtectsAnImportedInactiveAttachment(t *testing.T) {
	dir := t.TempDir()
	b, err := NewBackend(dir, filepath.Join(dir, "gui.exe"))
	if err != nil {
		t.Fatal(err)
	}
	b.conn.API = forbiddenAPI{t}
	b.conn.ConfigureTarget(filepath.Join(dir, "other-client.yaml"), nil)
	b.campus = &clash.CampusAttachment{Root: dir, ProfileUID: "old", ProviderPath: filepath.Join(dir, "provider.yaml")}
	if err := os.WriteFile(filepath.Join(dir, "profiles.yaml"), []byte("current: old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	b.campusOwned = true
	done := make(chan struct{})
	close(done)
	b.startDone = done
	if stopped, err := b.Disconnect(); stopped || err == nil {
		t.Fatal("quit discarded an imported configuration awaiting recovery")
	}
	if !b.ownsCampus() || b.startDone == nil {
		t.Fatal("lost ownership after failed cleanup")
	}
}

type unavailableClashAPI struct {
	failure error
	calls   int
}

func (a *unavailableClashAPI) Do(method, path string, payload, out any) error {
	a.calls++
	if method != "GET" {
		return errors.New("unexpected controller mutation")
	}
	return a.failure
}

func TestQuitDefersRecoveryWhenClashIsGoneOrExitIsForced(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "controller-gone", true: "forced-after-conflict"}[force], func(t *testing.T) {
			dir := t.TempDir()
			b, err := NewBackend(dir, filepath.Join(dir, "gui.exe"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(b.conn.Close)
			provider := filepath.Join(dir, "campus.yaml")
			contents := []byte("payload: ['DOMAIN,campus.invalid']\n")
			if err := os.WriteFile(provider, contents, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "profiles.yaml"), []byte("current: sub\n"), 0600); err != nil {
				t.Fatal(err)
			}
			a := clash.CampusAttachment{Root: dir, ProfileUID: "sub", ProviderPath: provider, ProviderHash: fsx.Hash(contents), Active: force}
			if err := b.rememberCampus(a); err != nil {
				t.Fatal(err)
			}
			api := &unavailableClashAPI{failure: clash.ErrControllerUnavailable}
			if force {
				api.failure = errors.New("Clash configuration conflict")
			}
			b.conn.API, b.campusOwned = api, true
			done := make(chan struct{})
			close(done)
			b.startDone = done
			if force {
				if stopped, err := b.Disconnect(); stopped || err == nil {
					t.Fatal("ordinary conflict bypassed recovery")
				}
			}
			if stopped, err := b.disconnect(force); err != nil || !stopped {
				t.Fatalf("exit blocked by unavailable Clash: %v", err)
			}
			if b.runtime.Running() || b.ownsCampus() || b.startDone != nil {
				t.Fatal("FDU retained its session after stopping")
			}
			calls := api.calls
			if stopped, err := b.Disconnect(); err != nil || !stopped || calls != api.calls {
				t.Fatal("repeated exit retried failed recovery")
			}
			if fsx.HashFile(provider) != fsx.Hash(contents) {
				t.Fatal("offline exit rewrote rules")
			}
			reopened, err := NewBackend(dir, filepath.Join(dir, "gui.exe"))
			if err != nil {
				t.Fatal(err)
			}
			if reopened.campus == nil || !reopened.campus.Active || reopened.Status().State != "paused" {
				t.Fatal("lost pending recovery across restart")
			}
		})
	}
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
