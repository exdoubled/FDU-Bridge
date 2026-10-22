package clash

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mythologyli/zju-connect/gui/internal/clashsetup"
	"github.com/mythologyli/zju-connect/gui/internal/fsx"
)

type campusAPI struct {
	mode, global, outside string
	requests              []string
	failReload            bool
	reloadFailures        int
	missingSupport        bool
}

func (f *campusAPI) Do(method, path string, payload, out any) error {
	f.requests = append(f.requests, method+" "+path)
	var result any
	switch method + " " + path {
	case "GET /configs":
		result = map[string]any{"mode": f.mode, "mixed-port": 17890}
	case "GET /proxies":
		result = proxyResponse{Proxies: Proxies{
			"GLOBAL":   {Type: "Selector", Now: f.global, All: []string{"node-a", "DIRECT", "FDU-ROUTER"}},
			"INTERNET": {Type: "Selector", Now: f.outside, All: []string{"node-a", "DIRECT", "unsafe"}},
			"node-a":   {Type: "Shadowsocks"}, "DIRECT": {Type: "Direct"}, "FDU-ROUTER": {Type: "Socks5"},
			"unsafe": {Type: "Selector", Now: "node-a", All: []string{"node-a", "FDU-ROUTER"}},
		}}
		if f.missingSupport {
			delete(result.(proxyResponse).Proxies, "INTERNET")
			delete(result.(proxyResponse).Proxies, "FDU-ROUTER")
		}
	case "PUT /proxies/GLOBAL":
		f.global = payload.(map[string]string)["name"]
	case "PUT /proxies/INTERNET":
		f.outside = payload.(map[string]string)["name"]
	case "PUT /providers/rules/FDU-CAMPUS":
		if f.reloadFailures > 0 {
			f.reloadFailures--
			return errors.New("provider reload failed")
		}
		if f.failReload {
			f.failReload = false
			return errors.New("provider reload failed")
		}
	case "PUT /configs?force=true":
		f.global, f.outside = "node-a", "node-a"
	default:
		return errors.New("unexpected request: " + method + " " + path)
	}
	if out != nil {
		b, _ := json.Marshal(result)
		return json.Unmarshal(b, out)
	}
	return nil
}

func campusFixture(t *testing.T, mode string) (*Connection, *campusAPI, CampusAttachment) {
	t.Helper()
	root := t.TempDir()
	provider := filepath.Join(root, "profiles", "campus.yaml")
	if err := os.MkdirAll(filepath.Dir(provider), 0700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		provider:                             "payload: []\n",
		filepath.Join(root, "profiles.yaml"): "current: sub\n",
		filepath.Join(root, "rules.json"):    `{"version":1,"domains":["on.fudan.edu.cn"],"domain_suffixes":[],"ip_cidrs":[]}`,
	} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	f := &campusAPI{mode: mode, global: "DIRECT", outside: "node-a"}
	a := CampusAttachment{Root: root, ProfileUID: "sub", Config: filepath.Join(root, "client.toml"), Rules: filepath.Join(root, "rules.json"), ProviderPath: provider, ProviderHash: fsx.HashFile(provider), ClashMode: mode, Global: "DIRECT", Outside: "DIRECT", MixedPort: 17890}
	return &Connection{Dir: root, API: f}, f, a
}

func TestCampusPickerUsesClashRulesOrSafeGlobalExit(t *testing.T) {
	for _, mode := range []string{"global", "rule"} {
		t.Run(mode, func(t *testing.T) {
			_, f, _ := campusFixture(t, mode)
			f.global = "FDU-ROUTER"
			state, err := (&Picker{API: f, Campus: true}).Load()
			if err != nil || state.Problem != "" {
				t.Fatalf("load: %+v %v", state, err)
			}
			if mode == "rule" {
				if state.Current != "沿用 Clash 设置" || len(state.Choices) != 0 {
					t.Fatalf("rule mode offered overrides: %+v", state)
				}
			} else if !contains(state.Choices, "DIRECT") || contains(state.Choices, "unsafe") {
				t.Fatalf("unsafe global choices: %+v", state)
			}
		})
	}
}

func TestCampusActivationAndDetachPreserveBaseModeAndOutside(t *testing.T) {
	for _, mode := range []string{"global", "rule"} {
		t.Run(mode, func(t *testing.T) {
			c, f, a := campusFixture(t, mode)
			if mode == "rule" {
				a.Global, f.global = "FDU-ROUTER", "FDU-ROUTER"
			}
			raw, _ := os.ReadFile(a.Rules)
			data, _ := clashsetup.CampusRulesDocument(raw, true)
			active, err := c.activateCampus(f, a, data, fsx.Hash(raw))
			if err != nil || !active.Active || active.RulesHash != fsx.Hash(raw) {
				t.Fatalf("activate: %+v %v", active, err)
			}
			if f.mode != mode || f.global != "FDU-ROUTER" || f.outside != "DIRECT" {
				t.Fatalf("route changed: %+v", f)
			}
			if err := c.CheckCampus(active); err != nil {
				t.Fatal(err)
			}
			if _, err = c.DeactivateCampus(active); err != nil {
				t.Fatal(err)
			}
			if f.mode != mode || f.global != "DIRECT" || fsx.HashFile(a.ProviderPath) != a.ProviderHash {
				t.Fatal("detach did not restore owned route")
			}
			for _, request := range f.requests {
				if strings.Contains(request, "/configs?") {
					t.Fatal("campus activation reloaded full config")
				}
			}
		})
	}
}

func TestCampusProviderReloadRollsBackAndRefusesExternalEdits(t *testing.T) {
	c, f, a := campusFixture(t, "global")
	raw, _ := os.ReadFile(a.Rules)
	data, _ := clashsetup.CampusRulesDocument(raw, true)
	f.failReload = true
	if _, err := c.activateCampus(f, a, data, fsx.Hash(raw)); err == nil {
		t.Fatal("unconfirmed provider activation accepted")
	}
	if fsx.HashFile(a.ProviderPath) != a.ProviderHash || f.global != "DIRECT" {
		t.Fatal("failed activation was not rolled back")
	}
	if err := os.WriteFile(a.ProviderPath, []byte("payload: ['DOMAIN,user.invalid']\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f.global = "FDU-ROUTER"
	before := len(f.requests)
	if _, err := c.DeactivateCampus(a); err == nil {
		t.Fatal("overwrote user provider edit")
	}
	if f.global != "node-a" || fsx.HashFile(a.ProviderPath) == a.ProviderHash {
		t.Fatal("owned GLOBAL was not restored or user provider edit was overwritten")
	}
	for _, request := range f.requests[before:] {
		if request == "PUT /providers/rules/FDU-CAMPUS" {
			t.Fatal("reloaded an externally edited provider")
		}
	}
}

func TestCampusImportPreservesDirectWithoutActivation(t *testing.T) {
	for _, mode := range []string{"global", "rule"} {
		t.Run(mode, func(t *testing.T) {
			c, f, a := campusFixture(t, mode)
			files := map[string]string{
				"profiles.yaml":    "current: sub\nitems:\n- {uid: sub, type: remote, name: Test, file: sub.yaml}\n",
				"clash-verge.yaml": "mixed-port: 17890\nmode: global\nproxies: [{name: node-a, type: ss, server: example.invalid, port: 443}]\nproxy-groups: []\nrules: ['MATCH,DIRECT']\n",
				"verge.yaml":       "enable_system_proxy: false\n",
			}
			for path, content := range files {
				if err := os.WriteFile(filepath.Join(a.Root, path), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			values := map[string]any{"protocol": "atrust", "socks_bind": "127.0.0.1:11080", "http_bind": "127.0.0.1:11081", "routing": map[string]any{"rules_file": a.Rules}}
			plan, err := c.PrepareCampus(a.Root, a.Config, values)
			if err != nil {
				t.Fatal(err)
			}
			if plan.ClashMode != mode {
				t.Fatal("preview ignored live mode")
			}
			backup, attached, err := c.ImportCampus(plan)
			if err != nil {
				t.Fatal(err)
			}
			wantOutside := "DIRECT"
			if mode == "rule" {
				wantOutside = "node-a"
			}
			if backup == "" || attached.Active || attached.Global != "DIRECT" || attached.Outside != wantOutside || f.global != "DIRECT" || f.outside != wantOutside {
				t.Fatalf("DIRECT import changed route: %+v %+v", attached, f)
			}
			if fsx.HashFile(attached.ProviderPath) != fsx.Hash([]byte("payload: []\n")) {
				t.Fatal("import enabled campus rules before login")
			}
		})
	}
}

func TestCampusCheckAndDetachRespectUserChanges(t *testing.T) {
	for _, change := range []string{"mode", "profile", "global"} {
		t.Run(change, func(t *testing.T) {
			c, f, a := campusFixture(t, "global")
			a.Active = true
			f.global = "FDU-ROUTER"
			switch change {
			case "mode":
				f.mode = "rule"
			case "profile":
				if err := os.WriteFile(filepath.Join(a.Root, "profiles.yaml"), []byte("current: other\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "global":
				f.global = "node-a"
			}
			if err := c.CheckCampus(a); err == nil {
				t.Fatal("user change not detected")
			}
			_, err := c.DeactivateCampus(a)
			if change == "profile" {
				if err == nil || !strings.Contains(err.Error(), "订阅已切换且仍引用 FDU") || f.global != "FDU-ROUTER" {
					t.Fatalf("unknown profile route was changed or unsafe cleanup succeeded: %v", err)
				}
				f.missingSupport = true
				if _, err = c.DeactivateCampus(a); err != nil {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if change == "global" && f.global != "node-a" {
				t.Fatal("user GLOBAL choice was overwritten")
			}
			if change == "mode" && f.mode != "rule" {
				t.Fatal("user mode was overwritten")
			}
		})
	}
}

func TestCampusRequiresFduAcknowledgementBeforeControllerWrites(t *testing.T) {
	c, f, a := campusFixture(t, "global")
	if _, err := c.ActivateCampus(a); !errors.Is(err, ErrCampusPending) {
		t.Fatal("unacknowledged activation accepted")
	}
	a.Active = true
	if _, err := c.SyncCampus(a); !errors.Is(err, ErrCampusPending) {
		t.Fatal("unacknowledged sync accepted")
	}
	if !reflect.DeepEqual(f.requests, []string(nil)) {
		t.Fatalf("unready FDU reached controller: %v", f.requests)
	}
}

func TestCampusDetachClearsFileWhenIntegrationWasRemoved(t *testing.T) {
	c, f, a := campusFixture(t, "global")
	data := []byte("payload: ['DOMAIN,on.fudan.edu.cn']\n")
	if err := os.WriteFile(a.ProviderPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	a.ProviderHash, a.Active = fsx.Hash(data), true
	f.missingSupport = true
	f.global = "DIRECT"
	if _, err := c.DeactivateCampus(a); err != nil {
		t.Fatal(err)
	}
	if fsx.HashFile(a.ProviderPath) != fsx.Hash([]byte("payload: []\n")) {
		t.Fatal("removed integration retained campus payload")
	}
	for _, request := range f.requests {
		if strings.HasPrefix(request, "PUT ") {
			t.Fatalf("removed integration caused live mutation: %v", f.requests)
		}
	}
}

func TestCampusRuleToGlobalDetachRestoresDormantRouterSelection(t *testing.T) {
	c, f, a := campusFixture(t, "rule")
	a.Active, a.Global = true, "FDU-ROUTER"
	f.mode, f.global, f.outside = "global", "FDU-ROUTER", "node-a"
	if err := c.CheckCampus(a); err == nil {
		t.Fatal("rule-to-global mode change was not detected")
	}
	if _, err := c.DeactivateCampus(a); err != nil {
		t.Fatal(err)
	}
	if f.mode != "global" || f.global != "node-a" || f.outside != "node-a" {
		t.Fatalf("detach left the active GLOBAL route pointing at FDU: %+v", f)
	}
}

func TestCampusDetachReloadsEmptyProviderAfterUnconfirmedRollback(t *testing.T) {
	c, f, a := campusFixture(t, "global")
	raw, _ := os.ReadFile(a.Rules)
	data, _ := clashsetup.CampusRulesDocument(raw, true)
	f.reloadFailures = 2
	recovery, err := c.activateCampus(f, a, data, fsx.Hash(raw))
	if err == nil {
		t.Fatal("unconfirmed provider rollback was accepted")
	}
	if fsx.HashFile(a.ProviderPath) != a.ProviderHash {
		t.Fatal("failed activation did not restore the empty provider file")
	}
	if _, err := c.DeactivateCampus(recovery); err != nil {
		t.Fatal(err)
	}
	reloads := 0
	for _, request := range f.requests {
		if request == "PUT /providers/rules/FDU-CAMPUS" {
			reloads++
		}
	}
	if reloads != 3 {
		t.Fatalf("detach did not confirm reloading the empty provider: %v", f.requests)
	}
}
