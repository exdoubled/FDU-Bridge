package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestImportedProfilesSaveTheirOwnConfigCookiesAndRules(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "gui")
	source := filepath.Join(root, "external-source.toml")
	cache := filepath.Join(root, "external-cache.json")
	rulesFile := filepath.Join(root, "external-rules.json")
	w, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"account-a", "account-b"} {
		_ = os.WriteFile(cache, []byte(name), 0o600)
		_ = os.WriteFile(rulesFile, []byte(name+"-rules"), 0o600)
		_ = os.WriteFile(source, []byte("protocol='atrust'\nserver_address='"+name+".invalid'\nclient_data_file='external-cache.json'\n[routing]\nrules_file='external-rules.json'\n"), 0o600)
		view, err := w.Load(source, false)
		if err != nil {
			t.Fatal(err)
		}
		if !view.Dirty {
			t.Fatal("import not marked for save")
		}
		if _, err := w.Save(view.Values); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(read(t, w.Target), name+".invalid") {
			t.Fatal("saved previous server")
		}
		if read(t, filepath.Join(dir, "client_data.json")) != name || read(t, filepath.Join(dir, "routing-rules.json")) != name+"-rules" {
			t.Fatal("reused previous account state or rules")
		}
		if read(t, cache) != name {
			t.Fatal("modified external cache")
		}
	}
	_ = os.WriteFile(source, []byte("protocol='atrust'\nserver_address='account-c.invalid'\nclient_data_file='not-created-session.json'\n"), 0o600)
	view, err := w.Load(source, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Save(view.Values); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "client_data.json")); !os.IsNotExist(err) {
		t.Fatal("fresh account reused previous cookies")
	}
}

func TestExternalEditsAndInvalidValuesAreNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	w, _ := New(dir)
	_ = os.WriteFile(w.Target, []byte("protocol='atrust'\nserver_address='vpn.invalid'\n"), 0o600)
	view, err := w.Load(w.Target, true)
	if err != nil || view.Dirty || !view.Saved {
		t.Fatalf("own config should load clean: %+v %v", view, err)
	}
	_ = os.WriteFile(w.Target, []byte("protocol='atrust'\nserver_address='changed.invalid'\n"), 0o600)
	if _, err := w.Save(view.Values); err == nil {
		t.Fatal("overwrote external edit")
	}
	view, _ = w.Load(w.Target, true)
	view.Values["server_port"] = float64(70000)
	if _, err := w.Save(view.Values); err == nil {
		t.Fatal("accepted invalid port")
	}
	if !strings.Contains(read(t, w.Target), "changed.invalid") {
		t.Fatal("invalid save changed file")
	}
}

func TestManagedLoadOnlyRetargetsPrivatePaths(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "gui")
	_ = os.MkdirAll(dir, 0o700)
	outside := filepath.Join(root, "outside.toml")
	inside := filepath.Join(dir, "fdu-routing.toml")
	for _, p := range []string{outside, inside} {
		_ = os.WriteFile(p, []byte("protocol='atrust'\nserver_address='vpn.invalid'\n"), 0o600)
	}
	w, _ := New(dir)
	if _, err := w.Load(outside, true); err != nil || w.Target != filepath.Join(dir, "client.toml") {
		t.Fatalf("retargeted to outside path: %s %v", w.Target, err)
	}
	if _, err := w.Load(inside, true); err != nil || w.Target != inside {
		t.Fatalf("did not retarget managed path: %s %v", w.Target, err)
	}
}
