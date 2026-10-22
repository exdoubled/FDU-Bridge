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
	cleaned bool
	fail    bool
}

func (f *cleanupAPI) Do(method, path string, payload, out any) error {
	if f.fail {
		return errors.New("controller unavailable")
	}
	if method == "PUT" && path == "/configs?force=true" {
		f.cleaned = !strings.Contains(payload.(map[string]string)["payload"], "FDU-ROUTER")
		f.missingSupport = f.cleaned
		return nil
	}
	return f.campusAPI.Do(method, path, payload, out)
}

func TestCleanupKeepsSubscriptionAdditionsAndRuntimePreferences(t *testing.T) {
	for _, runtime := range []bool{false, true} {
		t.Run(map[bool]string{false: "verge", true: "runtime"}[runtime], func(t *testing.T) {
			dir := t.TempDir()
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
			if !runtime {
				metaPath := filepath.Join(root, "profiles.yaml")
				raw, _ := os.ReadFile(metaPath)
				var meta map[string]any
				_ = yaml.Unmarshal(raw, &meta)
				for _, item := range meta["items"].([]any) {
					entry := item.(map[string]any)
					if entry["type"] == "rules" {
						path := filepath.Join(root, "profiles", entry["file"].(string))
						raw, _ = os.ReadFile(path)
						_ = os.WriteFile(path, append(raw, []byte("custom: keep\n")...), 0600)
					}
				}
			}
			backup, err := c.RemoveCampus(a, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !f.cleaned || f.global != "DIRECT" || f.outside != "node-a" {
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
	if _, err := c.RemoveCampus(a, nil); err == nil {
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
