package clash

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mythologyli/zju-connect/gui/internal/fsx"
	"gopkg.in/yaml.v3"
)

type cleanupAPI struct {
	campusAPI
	cleaned         bool
	fail            bool
	failAfterReload bool
	selected        string
	payload         string
}

func (f *cleanupAPI) Do(method, path string, payload, out any) error {
	if f.fail {
		return errors.New("controller unavailable")
	}
	if method == "GET" && path == "/proxies" && f.cleaned && f.failAfterReload {
		f.failAfterReload = false
		return errors.New("confirmation lost")
	}
	if method == "PUT" && path == "/proxies/Other" {
		f.selected = payload.(map[string]string)["name"]
		return nil
	}
	if method == "PUT" && path == "/configs?force=true" {
		f.cleaned = !strings.Contains(payload.(map[string]string)["payload"], "FDU-ROUTER")
		f.missingSupport = f.cleaned
		f.selected = "DIRECT"
		f.payload = payload.(map[string]string)["payload"]
		return nil
	}
	err := f.campusAPI.Do(method, path, payload, out)
	if err == nil && method == "GET" && path == "/proxies" && f.selected != "" {
		out.(*proxyResponse).Proxies["Other"] = &ProxyData{Type: "Selector", Now: f.selected, All: []string{"node-a", "DIRECT"}}
	}
	return err
}

func TestCleanupKeepsSubscriptionAdditionsAndRuntimePreferences(t *testing.T) {
	for _, runtime := range []bool{false, true} {
		t.Run(map[bool]string{false: "verge", true: "runtime"}[runtime], func(t *testing.T) {
			c, f, a, p := importedCleanupFixture(t, runtime)
			root, config, runtimePath := a.Root, a.Config, filepath.Join(a.Root, "clash-verge.yaml")
			customPath := ""
			if !runtime {
				metaPath := filepath.Join(root, "profiles.yaml")
				raw, _ := os.ReadFile(metaPath)
				var meta map[string]any
				_ = yaml.Unmarshal(raw, &meta)
				for _, item := range meta["items"].([]any) {
					entry := item.(map[string]any)
					if entry["type"] == "rules" {
						path := filepath.Join(root, "profiles", entry["file"].(string))
						customPath = path
						raw, _ = os.ReadFile(path)
						_ = os.WriteFile(path, append(raw, []byte("custom: keep\n")...), 0600)
					}
				}
				installCleanupScript(t, root, p, false)
			}
			backup, err := c.RemoveCampus(a, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !f.cleaned || f.global != "DIRECT" || f.outside != "node-a" || f.selected != "node-a" {
				t.Fatal("runtime or exits were not preserved")
			}
			if _, err := os.Stat(a.ProviderPath); !os.IsNotExist(err) {
				t.Fatal("owned feed remains")
			}
			if fsx.HashFile(config) == "" || !strings.Contains(string(mustRead(t, runtimePath)), "node-a") {
				t.Fatal("deleted user config")
			}
			if !runtime && !strings.Contains(string(mustRead(t, runtimePath)), "enable: true") {
				t.Fatal("lost TUN")
			}
			if !runtime {
				if !strings.Contains(string(mustRead(t, customPath)), "custom: keep") {
					t.Fatal("lost user extension content")
				}
				if _, err := os.Stat(filepath.Join(root, "profiles", "script.js")); !os.IsNotExist(err) {
					t.Fatal("generated script remains")
				}
			}
			var manifest map[string]any
			if json.Unmarshal(mustRead(t, filepath.Join(backup, "manifest.json")), &manifest) != nil {
				t.Fatal("missing recovery manifest")
			}
		})
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func TestCleanupRefusesChangedOwnershipBeforeWriting(t *testing.T) {
	c, f, a := campusFixture(t, "rule")
	before := fsx.HashFile(a.ProviderPath)
	_ = os.WriteFile(a.ProviderPath, []byte("payload: ['DOMAIN,user.invalid']\n"), 0600)
	if _, err := c.RemoveCampus(a, nil, nil); err == nil {
		t.Fatal("accepted ownership mismatch")
	}
	if fsx.HashFile(a.ProviderPath) == before {
		t.Fatal("changed user data")
	}
	for _, request := range f.requests {
		if strings.HasPrefix(request, "PUT ") {
			t.Fatal("mutated controller before validation")
		}
	}
}

func TestCleanupAcceptsInternetChoiceChangedAfterImport(t *testing.T) {
	c, f, a, _ := importedCleanupFixture(t, false)
	f.outside = "Other"
	if _, err := c.RemoveCampus(a, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !f.cleaned || f.selected != "node-a" {
		t.Fatal("cleanup lost the user's outside selector")
	}
}

func importedCleanupFixture(t *testing.T, runtime bool) (*Connection, *cleanupAPI, CampusAttachment, Plan) {
	t.Helper()
	return importedCleanupFixtureAt(t, runtime, t.TempDir())
}

func importedCleanupFixtureAt(t *testing.T, runtime bool, dir string) (*Connection, *cleanupAPI, CampusAttachment, Plan) {
	t.Helper()
	root := filepath.Join(dir, "client")
	_ = os.MkdirAll(filepath.Join(root, "profiles"), 0700)
	config := filepath.Join(dir, "client.toml")
	_ = os.WriteFile(config, []byte("protocol='atrust'\nserver_address='vpn.invalid'\nsocks_bind='127.0.0.1:1080'\nhttp_bind='127.0.0.1:1081'\n"), 0600)
	_ = os.WriteFile(filepath.Join(root, "profiles.yaml"), []byte("current: sub\nitems:\n- {uid: sub, type: remote, file: sub.yaml, name: Subscription}\n"), 0600)
	_ = os.WriteFile(filepath.Join(root, "verge.yaml"), []byte("enable_system_proxy: true\n"), 0600)
	_ = os.WriteFile(filepath.Join(root, "profiles", "sub.yaml"), []byte("proxies: [{name: node-a, type: ss, server: outside.invalid, port: 443}]\n"), 0600)
	runtimePath := filepath.Join(root, "clash-verge.yaml")
	_ = os.WriteFile(runtimePath, []byte("mode: rule\nmixed-port: 17890\ntun: {enable: true}\nproxies: [{name: node-a, type: ss, server: outside.invalid, port: 443}]\nrules: ['MATCH,node-a']\n"), 0600)
	f := &cleanupAPI{campusAPI: campusAPI{mode: "rule", global: "DIRECT", outside: "node-a"}}
	c := &Connection{Dir: dir, API: f}
	t.Cleanup(c.Close)
	if runtime {
		c.ConfigureTarget(runtimePath, nil)
	}
	p, err := c.PrepareCampus(root, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, a, err := c.ImportCampus(p)
	if err != nil {
		t.Fatal(err)
	}
	f.selected = "node-a"
	return c, f, a, p
}

func TestCleanupAllowsLinkedParent(t *testing.T) {
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(t.TempDir(), alias); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}
	c, f, a, _ := importedCleanupFixtureAt(t, false, filepath.Join(alias, "gui"))
	if _, err := c.RemoveCampus(a, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !f.cleaned || fsx.HashFile(a.ProviderPath) != "" {
		t.Fatal("integration was not removed")
	}
}

func installCleanupScript(t *testing.T, root string, p Plan, edited bool) {
	t.Helper()
	script, _, err := renderExportPatch(p)
	if err != nil {
		t.Fatal(err)
	}
	if edited {
		script += "\n// user edit\n"
	}
	if err = os.WriteFile(filepath.Join(root, "profiles", "script.js"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "profiles.yaml")
	var meta map[string]any
	if err = yaml.Unmarshal(mustRead(t, path), &meta); err != nil {
		t.Fatal(err)
	}
	items := meta["items"].([]any)
	for _, item := range items {
		entry := item.(map[string]any)
		if entry["uid"] == "sub" {
			entry["option"].(map[string]any)["script"] = "script"
		}
	}
	meta["items"] = append(items, map[string]any{"uid": "script", "type": "script", "file": "script.js"})
	raw, _ := yaml.Marshal(meta)
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupKeepsRecoveryOnEditedScriptOrControllerFailure(t *testing.T) {
	for _, scenario := range []string{"edited script", "shared groups", "shared script", "unavailable", "rollback", "metadata"} {
		t.Run(scenario, func(t *testing.T) {
			shared := strings.HasPrefix(scenario, "shared ")
			c, f, a, p := importedCleanupFixture(t, scenario != "edited script" && !shared)
			if scenario == "edited script" {
				installCleanupScript(t, a.Root, p, true)
			}
			if shared {
				if scenario == "shared script" {
					installCleanupScript(t, a.Root, p, false)
				}
				path := filepath.Join(a.Root, "profiles.yaml")
				var meta map[string]any
				_ = yaml.Unmarshal(mustRead(t, path), &meta)
				items := meta["items"].([]any)
				for _, item := range items {
					entry := item.(map[string]any)
					if entry["uid"] == a.ProfileUID {
						kind := strings.TrimPrefix(scenario, "shared ")
						meta["items"] = append(items, map[string]any{"uid": "other", "type": "remote", "file": "other.yaml", "option": map[string]any{kind: entry["option"].(map[string]any)[kind]}})
					}
				}
				raw, _ := yaml.Marshal(meta)
				_ = os.WriteFile(path, raw, 0600)
				f.requests = nil
			}
			runtimePath := filepath.Join(a.Root, "clash-verge.yaml")
			before, feed := fsx.HashFile(runtimePath), fsx.HashFile(a.ProviderPath)
			f.fail = scenario == "unavailable"
			f.failAfterReload = scenario == "rollback"
			f.mode = "global"
			f.global = "FDU-ROUTER"
			finalize := func() error { return nil }
			if scenario == "metadata" {
				finalize = func() error { return errors.New("journal finalization failed") }
			}
			if _, err := c.RemoveCampus(a, nil, finalize); err == nil {
				t.Fatal("accepted uncertain cleanup")
			}
			if fsx.HashFile(runtimePath) != before || fsx.HashFile(a.ProviderPath) != feed {
				t.Fatal("did not retain source and owned feed")
			}
			if shared {
				for _, request := range f.requests {
					if strings.HasPrefix(request, "PUT ") {
						t.Fatal("shared extension caused runtime mutation")
					}
				}
			}
			if (scenario == "rollback" || scenario == "metadata") && (f.cleaned || f.global != "FDU-ROUTER" || f.outside != "node-a" || f.selected != "node-a") {
				t.Fatal("did not restore prior runtime and selections")
			}
		})
	}
}
