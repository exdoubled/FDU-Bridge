package clashsetup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

func TestPrepareCampusPreservesModeAndRulesWithoutPreviewWrites(t *testing.T) {
	for _, tc := range []struct {
		name, mode, override  string
		enabled, legacy, seed bool
	}{
		{name: "global enabled", mode: "global", enabled: true},
		{name: "rule disabled", mode: "rule"},
		{name: "live mode wins", mode: "global", override: "rule", enabled: true},
		{name: "migrate front listener", mode: "global", enabled: true, legacy: true},
		{name: "seed unsaved settings", mode: "rule", enabled: true, seed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fixture(t)
			r.ClashMode = tc.override
			r.Outside = "original"
			runtimePath := filepath.Join(r.Root, "clash-verge.yaml")
			b, _ := os.ReadFile(runtimePath)
			var runtime map[string]any
			if err := yaml.Unmarshal(b, &runtime); err != nil {
				t.Fatal(err)
			}
			runtime["mode"], runtime["tun"] = tc.mode, map[string]any{"enable": true, "stack": "mixed"}
			userRules := []any{"DOMAIN,personal.invalid,original", "MATCH,original"}
			runtime["rules"] = userRules
			if tc.legacy {
				runtime["listeners"] = []any{map[string]any{"name": "FDU-FALLBACK", "type": "mixed", "listen": "127.0.0.1", "port": 17903, "proxy": "GLOBAL"}}
			}
			b, _ = yaml.Marshal(runtime)
			if err := os.WriteFile(runtimePath, b, 0600); err != nil {
				t.Fatal(err)
			}
			metaPath := filepath.Join(r.Root, "profiles.yaml")
			b, _ = os.ReadFile(metaPath)
			b = []byte(strings.Replace(string(b), "groups: g}", "groups: g, rules: r}\n", 1) + "- {uid: r, type: rules, file: r.yaml}\n")
			if err := os.WriteFile(metaPath, b, 0600); err != nil {
				t.Fatal(err)
			}
			rulesExtension := filepath.Join(r.Root, "profiles", "r.yaml")
			if err := os.WriteFile(rulesExtension, []byte("prepend: ['DOMAIN,prepend.invalid,DIRECT']\nappend: ['DOMAIN,append.invalid,DIRECT']\ndelete: ['DOMAIN,delete.invalid,DIRECT']\n"), 0600); err != nil {
				t.Fatal(err)
			}
			rulesPath := filepath.Join(r.Directory, "private-rules.json")
			raw := `{"version":2,"domains":["ON.fudan.edu.cn","off.fudan.edu.cn"],"domain_suffixes":["FUDAN.EDU.CN"],"ip_cidrs":["10.1.1.7/24"],"disabled":{"domains":["off.fudan.edu.cn"],"domain_suffixes":[],"ip_cidrs":[]}}`
			r.Values = map[string]any{"protocol": "atrust", "socks_bind": "127.0.0.1:11080", "http_bind": "127.0.0.1:11081", "client_data_file": "private-cache.json", "routing": map[string]any{"enabled": false, "rules_file": rulesPath, "domains": []string{"seed.fudan.edu.cn"}}}
			if tc.seed {
				for _, key := range []string{"tun_mode", "add_route", "dns_hijack", "fake_ip"} {
					r.Values[key] = true
				}
			}
			if !tc.seed {
				if err := os.WriteFile(rulesPath, []byte(raw), 0600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Remove(r.Config); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(r.Directory, "fdu-routing.toml"), []byte("unrelated='keep'"), 0600); err != nil {
				t.Fatal(err)
			}
			before := campusSnapshot(t, filepath.Dir(r.Root))
			valuesBefore, _ := json.Marshal(r.Values)
			p, err := PrepareCampus(r, tc.enabled)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, campusSnapshot(t, filepath.Dir(r.Root))) {
				t.Fatal("preview wrote files")
			}
			valuesAfter, _ := json.Marshal(r.Values)
			if string(valuesAfter) != string(valuesBefore) {
				t.Fatal("planner mutated settings draft")
			}
			wantMode := tc.mode
			if tc.override != "" {
				wantMode = tc.override
			}
			if !p.Campus || p.Mode != "clash-first" || p.ClashMode != wantMode || p.LegacyFront != tc.legacy || p.Config != r.Config || p.Rules != rulesPath {
				t.Fatalf("unexpected campus plan: %+v", p)
			}
			if err := yaml.Unmarshal([]byte(p.Payload), &runtime); err != nil {
				t.Fatal(err)
			}
			if runtime["mode"] != wantMode || !reflect.DeepEqual(runtime["tun"], map[string]any{"enable": true, "stack": "mixed"}) || !reflect.DeepEqual(list(runtime, "rules"), append([]any{"RULE-SET,FDU-CAMPUS,FDU-ROUTER"}, userRules...)) {
				t.Fatalf("mode, TUN, or user rule order changed: %v", runtime)
			}
			if list(runtime, "listeners")[0].(map[string]any)["proxy"] != "INTERNET" {
				t.Fatal("fallback was not pinned to INTERNET")
			}
			provider := mapping(runtime, "rule-providers")[CampusProvider].(map[string]any)
			for _, entry := range list(runtime, "proxy-groups") {
				group := entry.(map[string]any)
				if group["name"] == "INTERNET" && !reflect.DeepEqual(list(group, "proxies"), []any{"node-a", "DIRECT", "original"}) {
					t.Fatalf("outside exit missing from INTERNET: %v", group)
				}
			}
			if provider["type"] != "file" || provider["behavior"] != "classical" || provider["path"] != p.ProviderPath || filepath.Dir(p.ProviderPath) != filepath.Join(r.Root, "profiles") {
				t.Fatalf("wrong provider: %v", provider)
			}
			var payload []string
			seen := map[string]bool{}
			for _, change := range p.Files {
				if seen[change.Path] {
					t.Fatalf("duplicate planned write: %s", change.Path)
				}
				seen[change.Path] = true
				if old, exists := before[change.Path]; exists && change.Hash != digest([]byte(old)) {
					t.Fatalf("lost original hash for %s", change.Path)
				}
				switch change.Path {
				case p.ProviderPath:
					var doc struct {
						Payload []string `yaml:"payload"`
					}
					if err := yaml.Unmarshal([]byte(change.Content), &doc); err != nil {
						t.Fatal(err)
					}
					payload = doc.Payload
				case r.Config:
					var cfg map[string]any
					if err := toml.Unmarshal([]byte(change.Content), &cfg); err != nil {
						t.Fatal(err)
					}
					if cfg["client_data_file"] != filepath.Join(r.Directory, "private-cache.json") || mapping(cfg, "routing")["rules_file"] != rulesPath {
						t.Fatal("private cache or single rules file reference was lost")
					}
					for _, key := range []string{"tun_mode", "add_route", "dns_hijack", "fake_ip"} {
						if cfg[key] != false {
							t.Fatalf("generated config retained incompatible %s", key)
						}
					}
				case rulesExtension:
					var doc map[string]any
					_ = yaml.Unmarshal([]byte(change.Content), &doc)
					if !reflect.DeepEqual(list(doc, "prepend"), []any{"RULE-SET,FDU-CAMPUS,FDU-ROUTER", "DOMAIN,prepend.invalid,DIRECT"}) || !reflect.DeepEqual(list(doc, "append"), []any{"DOMAIN,append.invalid,DIRECT"}) || !reflect.DeepEqual(list(doc, "delete"), []any{"DOMAIN,delete.invalid,DIRECT"}) {
						t.Fatalf("existing rules extension changed: %v", doc)
					}
				}
			}
			wantPayload := []string{}
			if tc.enabled {
				wantPayload = []string{"DOMAIN,on.fudan.edu.cn", "DOMAIN-SUFFIX,fudan.edu.cn", "IP-CIDR,10.1.1.0/24"}
				if tc.seed {
					wantPayload = []string{"DOMAIN,seed.fudan.edu.cn"}
				}
			}
			if !reflect.DeepEqual(payload, wantPayload) {
				t.Fatalf("provider payload = %v, want %v", payload, wantPayload)
			}
			for _, change := range p.Files {
				if err := os.WriteFile(change.Path, []byte(change.Content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			r.Values = nil
			again, err := PrepareCampus(r, tc.enabled)
			if err != nil || again.Payload != p.Payload || len(again.Files) != 0 {
				t.Fatalf("repeated import changed plan: %v, %+v", err, again.Files)
			}
		})
	}
}

func campusSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		files[path] = string(b)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func TestCampusRulesDocumentSchemaAndRejectedSetup(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		enabled   bool
		want      []string
		invalid   bool
	}{
		{name: "version one", raw: `{"version":1,"domains":["ON.fudan.edu.cn"],"domain_suffixes":[],"ip_cidrs":[]}`, enabled: true, want: []string{"DOMAIN,on.fudan.edu.cn"}},
		{name: "disabled", raw: `{"version":1,"domains":["on.fudan.edu.cn"],"domain_suffixes":[],"ip_cidrs":[]}`, want: []string{}},
		{name: "invalid still rejected when disabled", raw: `{"version":2,"domains":[],"domain_suffixes":[],"ip_cidrs":[]}`, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := CampusRulesDocument([]byte(tc.raw), tc.enabled)
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid FDU rules accepted")
				}
				return
			}
			var doc struct {
				Payload []string `yaml:"payload"`
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = yaml.Unmarshal(b, &doc); err != nil || !reflect.DeepEqual(doc.Payload, tc.want) {
				t.Fatalf("provider payload = %v, %v; want %v", doc.Payload, err, tc.want)
			}
		})
	}
	for _, tc := range []struct{ name, mode, provider string }{
		{name: "direct mode", mode: "direct"},
		{name: "unknown mode", mode: "unexpected"},
		{name: "unrelated runtime provider", provider: "rule-providers:\n  FDU-CAMPUS: {type: http, behavior: classical, path: ./other.yaml}\n"},
		{name: "unrelated merge provider", provider: "merge"},
		{name: "unowned provider file", provider: "file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fixture(t)
			r.ClashMode = tc.mode
			if tc.provider == "merge" {
				if err := os.WriteFile(filepath.Join(r.Root, "profiles", "m.yaml"), []byte("rule-providers:\n  FDU-CAMPUS: {type: file, behavior: classical, path: ./other.yaml}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			} else if tc.provider == "file" {
				if err := os.WriteFile(filepath.Join(r.Root, "profiles", "fdu-campus-ddc6e2b224d0.yaml"), []byte("payload: ['DOMAIN,private.invalid']\n"), 0600); err != nil {
					t.Fatal(err)
				}
			} else if tc.provider != "" {
				path := filepath.Join(r.Root, "clash-verge.yaml")
				b, _ := os.ReadFile(path)
				if err := os.WriteFile(path, append(b, []byte(tc.provider)...), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := PrepareCampus(r, false); err == nil {
				t.Fatal("unsafe setup accepted")
			}
		})
	}
}
