package clash

import (
	"encoding/json"
	"errors"
	"github.com/mythologyli/zju-connect/gui/internal/clashsetup"
	"github.com/mythologyli/zju-connect/gui/internal/fsx"
	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type campusAPI struct {
	mode, global, outside string
	requests              []string
	failReload            bool
	reloadFailures        int
	missingSupport        bool
	afterReload           func()
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
		f.missingSupport = false
		if f.afterReload != nil {
			changed := f.afterReload
			f.afterReload = nil
			changed()
		}
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
	c := &Connection{Dir: root, API: f}
	t.Cleanup(c.Close)
	return c, f, a
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
			if mode == "global" {
				plan, err = c.PrepareCampus(a.Root, a.Config, nil)
				if err != nil {
					t.Fatal(err)
				}
				f.afterReload = func() { f.mode = "rule" }
				before := len(f.requests)
				if _, _, err := c.ImportCampus(plan); err == nil || f.mode != "rule" {
					t.Fatal("a newer mode was overwritten during import")
				}
				reloads := 0
				for _, request := range f.requests[before:] {
					if request == "PUT /configs?force=true" {
						reloads++
					}
				}
				if reloads != 1 {
					t.Fatal("rollback reloaded the old mode over a newer user selection")
				}
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

func TestLoopbackTCPControllerSendsSecret(t *testing.T) {
	var auth string
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"mode":"global"}`))
	})}
	go func() { _ = server.Serve(l) }()
	t.Cleanup(func() { _ = server.Close() })
	var config map[string]any
	api := ControlAPI{Network: "tcp", Address: l.Addr().String(), Secret: "s3cret", Timeout: 3 * time.Second}
	if err := api.Do("GET", "/configs", nil, &config); err != nil || auth != "Bearer s3cret" {
		t.Fatalf("secret not sent: %q %v", auth, err)
	}
	if loopbackTCP("192.168.1.2:9090") || !loopbackTCP("127.0.0.1:9097") {
		t.Fatal("loopback check wrong")
	}
}

func TestControllerRefusalShowsReasonWithoutRequestCredentials(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"path is not subpath of home directory: private-password"}`))
	}))
	defer s.Close()
	api := ControlAPI{Network: "tcp", Address: strings.TrimPrefix(s.URL, "http://"), Timeout: time.Second}
	err := api.Do("PUT", "/configs?force=true", map[string]string{"payload": "proxies:\n- password: private-password\n"}, nil)
	if err == nil || !strings.Contains(err.Error(), "path is not subpath") || !strings.Contains(err.Error(), "/configs?force=true") || strings.Contains(err.Error(), "private-password") {
		t.Fatalf("missing diagnostic context or leaked credentials: %v", err)
	}
}

// The subprocess only acknowledges a temporary rules file; it never opens a VPN.
func TestCampusFollowAcknowledgementProcess(t *testing.T) {
	if os.Getenv("FDU_TEST_FOLLOW_ACK") != "1" {
		return
	}
	config := os.Args[len(os.Args)-1]
	path := filepath.Join(filepath.Dir(config), "rules.json")
	raw, _ := os.ReadFile(path)
	status, _ := json.Marshal(map[string]any{"version": 1, "pid": os.Getpid(), "running": true,
		"updated_at": time.Now().UTC().Format(time.RFC3339Nano), "loaded_sha256": fsx.Hash(raw)})
	if err := os.WriteFile(path+".status.json", status, 0600); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestCampusFollowsModeAndSubscriptionWithoutChangingCoreConfig(t *testing.T) {
	c, api, attachment := campusFixture(t, "global")
	base := "mixed-port: 17890\nmode: global\nproxies: [{name: node-a, type: ss, server: example.invalid, port: 443}]\nproxy-groups: []\nrules: ['DOMAIN,user.invalid,DIRECT', 'MATCH,node-a']\n"
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(attachment.Root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("profiles.yaml", "current: sub\nitems:\n- {uid: sub, type: local, name: First, file: sub.yaml}\n- {uid: other, type: local, name: Second, file: other.yaml}\n")
	write("clash-verge.yaml", base)
	write("verge.yaml", "enable_system_proxy: false\n")
	config, _ := toml.Marshal(map[string]any{"protocol": "atrust", "auto_detect_interface": true,
		"socks_bind": "127.0.0.1:11080", "http_bind": "127.0.0.1:11081",
		"routing": map[string]any{"enabled": true, "rules_file": attachment.Rules, "upstream_proxy": "socks5://127.0.0.1:17902"}})
	if err := os.WriteFile(attachment.Config, config, 0600); err != nil {
		t.Fatal(err)
	}
	// Avoid timestamp rounding near process launch.
	past := time.Now().Add(-5 * time.Second)
	if err := os.Chtimes(attachment.Config, past, past); err != nil {
		t.Fatal(err)
	}
	plan, err := c.PrepareCampus(attachment.Root, attachment.Config, nil)
	if err != nil || plan.NeedsFduStart {
		t.Fatalf("normalized config was not reusable: %v", err)
	}
	var runtimeConfig struct {
		Providers map[string]struct{ Type, URL, Path, Proxy string } `yaml:"rule-providers"`
	}
	if err := yaml.Unmarshal([]byte(plan.Payload), &runtimeConfig); err != nil {
		t.Fatal(err)
	}
	provider := runtimeConfig.Providers["FDU-CAMPUS"]
	if provider.Type != "http" || provider.Path != "" || provider.Proxy != "DIRECT" {
		t.Fatalf("campus rules cannot cross the service runtime boundary: %+v", provider)
	}
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	readFeed := func(want string) {
		t.Helper()
		res, err := client.Get(provider.URL)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 || string(body) != want {
			t.Fatalf("rule feed = %d %q, want %q", res.StatusCode, body, want)
		}
	}
	_, attachment, err = c.ImportCampus(plan)
	if err != nil {
		t.Fatal(err)
	}
	readFeed("payload: []\n")
	// A GUI restart must keep persisted subscription URLs usable.
	c.Close()
	if _, err := c.PrepareCampus(attachment.Root, attachment.Config, nil); err != nil {
		t.Fatal(err)
	}
	readFeed("payload: []\n")
	if res, err := client.Get(provider.URL + "/../../client.toml"); err != nil {
		t.Fatal(err)
	} else {
		res.Body.Close()
		if res.StatusCode != 404 {
			t.Fatal("rule feed exposed an unregistered file")
		}
	}
	info, _ := os.Stat(attachment.Config)
	cmd := exec.Command(os.Args[0], "-test.run=^TestCampusFollowAcknowledgementProcess$", "--", "-config", attachment.Config)
	cmd.Env = append(os.Environ(), "FDU_TEST_FOLLOW_ACK=1")
	stdin, _ := cmd.StdinPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { stdin.Close(); _ = cmd.Wait() }()
	deadline := time.Now().Add(5 * time.Second)
	for !fileExistsForFollow(attachment.Rules + ".status.json") {
		if time.Now().After(deadline) {
			t.Fatal("test acknowledgement did not arrive")
		}
		time.Sleep(10 * time.Millisecond)
	}
	attachment, err = c.ActivateCampus(attachment)
	if err != nil {
		t.Fatal(err)
	}
	readFeed("payload:\n    - DOMAIN,on.fudan.edu.cn\n")
	for _, change := range []string{"rule", "global", "subscription", "direct", "global", "conflict"} {
		t.Run(change, func(t *testing.T) {
			oldProvider := attachment.ProviderPath
			if change == "conflict" {
				edited := []byte("payload: ['DOMAIN,user.invalid']\n")
				if err := os.WriteFile(oldProvider, edited, 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := c.FollowCampus(attachment); err == nil || fsx.HashFile(oldProvider) != fsx.Hash(edited) {
					t.Fatal("automatic following overwrote an external edit")
				}
				return
			}
			if change == "subscription" {
				raw, _ := os.ReadFile(filepath.Join(attachment.Root, "profiles.yaml"))
				write("profiles.yaml", strings.Replace(string(raw), "current: sub", "current: other", 1))
				write("clash-verge.yaml", base)
				api.missingSupport, api.global = true, "node-a"
			} else {
				api.mode = change
			}
			target, err := c.CurrentCampus(attachment.Root)
			if err != nil || target.Mode != api.mode {
				t.Fatalf("observation failed: %+v %v", target, err)
			}
			if change == "direct" {
				requests := len(api.requests)
				if _, err := c.FollowCampus(attachment); err == nil {
					t.Fatal("direct mode should wait without changing it")
				}
				for _, request := range api.requests[requests:] {
					if strings.HasPrefix(request, "PUT ") {
						t.Fatal("direct mode was modified")
					}
				}
				return
			}
			attachment, err = c.FollowCampus(attachment)
			if err != nil || !attachment.Active || attachment.ClashMode != api.mode {
				t.Fatalf("follow failed: %+v %v", attachment, err)
			}
			if change == "subscription" && (attachment.ProfileUID != "other" ||
				fsx.HashFile(oldProvider) != fsx.Hash([]byte("payload: []\n"))) {
				t.Fatal("old subscription retained active campus rules")
			}
			after, _ := os.Stat(attachment.Config)
			if fsx.HashFile(attachment.Config) != fsx.Hash(config) || !after.ModTime().Equal(info.ModTime()) {
				t.Fatal("following requires a core restart because it changed its config")
			}
			raw, _ := os.ReadFile(filepath.Join(attachment.Root, "clash-verge.yaml"))
			var runtime map[string]any
			_ = yaml.Unmarshal(raw, &runtime)
			if got := runtime["rules"].([]any); got[len(got)-1] != "MATCH,node-a" {
				t.Fatal("outside rules were changed")
			}
		})
	}
}

func fileExistsForFollow(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
