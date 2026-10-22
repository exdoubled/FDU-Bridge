package clashsetup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

const runtimeSource = `# Owned by the external client.
mixed-port: 17890
mode: global
tun: {enable: true, stack: mixed}
proxy-providers:
  subscription: {type: file, path: ./subscription.yaml}
proxies:
  - {name: node-a, type: ss, server: node.invalid, port: 443, cipher: aes-128-gcm, password: private}
proxy-groups:
  - {name: original, type: select, proxies: [node-a], use: [subscription]}
rules: ['DOMAIN,private.invalid,DIRECT', 'MATCH,original']
`

func runtimeFixture(t *testing.T, source string) (Request, string) {
	t.Helper()
	dir := t.TempDir()
	sourceDir, work := filepath.Join(dir, "external"), filepath.Join(dir, "fdu")
	for _, path := range []string{sourceDir, work} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(sourceDir, "runtime.yaml")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	// Exercise the request boundary, including the optional runtime_file field.
	raw, err := json.Marshal(map[string]any{
		"runtime_file": path, "config": filepath.Join(work, "config.toml"),
		"directory": work, "clash_mode": "rule", "outside": "original",
		"values": map[string]any{"protocol": "atrust", "socks_bind": "127.0.0.1:11080", "http_bind": "127.0.0.1:11081", "tun_mode": true, "routing": map[string]any{"domains": []string{"ON.fudan.edu.cn"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var r Request
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	r.CampusURL = "http://127.0.0.1:17999/campus/"
	return r, path
}

func TestPrepareRuntimeCampusPreservesSourceAndPlansOnlyOwnedFiles(t *testing.T) {
	r, path := runtimeFixture(t, runtimeSource)
	before := campusSnapshot(t, filepath.Dir(filepath.Dir(path)))
	p, err := PrepareCampus(r, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, campusSnapshot(t, filepath.Dir(filepath.Dir(path)))) {
		t.Fatal("runtime preview wrote files")
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(raw, &metadata); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(runtimeSource))
	if metadata["runtime_file"] != path || metadata["runtime_hash"] != hex.EncodeToString(hash[:]) || p.Inputs[path] != hex.EncodeToString(hash[:]) {
		t.Fatalf("source identity missing: %s", raw)
	}
	if !p.Campus || p.Mode != "clash-first" || p.ClashMode != "rule" || p.Profile != "runtime.yaml" || p.ProfileUID == "" || p.MixedPort != 17890 {
		t.Fatalf("incorrect runtime plan metadata: %+v", p)
	}
	if filepath.Dir(p.ProviderPath) != r.Directory || filepath.Base(p.ProviderPath) != "fdu-campus-runtime.yaml" {
		t.Fatalf("provider escaped GUI directory: %s", p.ProviderPath)
	}
	var runtime, original map[string]any
	if err := yaml.Unmarshal([]byte(p.Payload), &runtime); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal([]byte(runtimeSource), &original); err != nil {
		t.Fatal(err)
	}
	if runtime["mode"] != "rule" || !reflect.DeepEqual(runtime["tun"], original["tun"]) || !reflect.DeepEqual(runtime["proxy-providers"], original["proxy-providers"]) || !reflect.DeepEqual(list(runtime, "rules"), []any{campusRule, "DOMAIN,private.invalid,DIRECT", "MATCH,original"}) {
		t.Fatalf("external behavior was lost: %v", runtime)
	}
	if !reflect.DeepEqual(list(runtime, "proxy-groups")[0], list(original, "proxy-groups")[0]) || len(list(runtime, "proxies")) != 2 || len(list(runtime, "listeners")) != 1 {
		t.Fatalf("original groups or FDU entries missing: %v", runtime)
	}
	provider := mapping(runtime, "rule-providers")[CampusProvider].(map[string]any)
	providerHash := sha256.Sum256([]byte(p.ProviderPath))
	if provider["url"] != r.CampusURL+hex.EncodeToString(providerHash[:])+".yaml" || provider["path"] != nil || provider["proxy"] != "DIRECT" {
		t.Fatalf("provider does not use the local feed: %v", provider)
	}
	seen := map[string]bool{}
	for _, change := range p.Files {
		if seen[change.Path] {
			t.Fatalf("duplicate write: %s", change.Path)
		}
		seen[change.Path] = true
		switch change.Path {
		case p.Config:
			var cfg map[string]any
			if err := toml.Unmarshal([]byte(change.Content), &cfg); err != nil {
				t.Fatal(err)
			}
			if cfg["tun_mode"] != false || mapping(cfg, "routing")["rules_file"] != p.Rules || mapping(cfg, "routing")["upstream_proxy"] != "socks5://127.0.0.1:17902" {
				t.Fatalf("FDU routing was not normalized: %v", cfg)
			}
		case p.Rules:
		case p.ProviderPath:
			var doc struct {
				Payload []string `yaml:"payload"`
			}
			if err := yaml.Unmarshal([]byte(change.Content), &doc); err != nil || !reflect.DeepEqual(doc.Payload, []string{"DOMAIN,on.fudan.edu.cn"}) {
				t.Fatalf("campus payload = %v, %v", doc.Payload, err)
			}
		default:
			t.Fatalf("planned external write: %s", change.Path)
		}
	}
	if len(seen) != 3 || seen[path] {
		t.Fatalf("wrong planned files: %v", seen)
	}
}

func TestPrepareRuntimeCampusSourceIdentityTracksSubscriptionBytes(t *testing.T) {
	r, path := runtimeFixture(t, runtimeSource)
	first, err := PrepareCampus(r, false)
	if err != nil {
		t.Fatal(err)
	}
	again, err := PrepareCampus(r, false)
	if err != nil || again.ProfileUID != first.ProfileUID || again.ProviderPath != first.ProviderPath {
		t.Fatalf("unchanged source changed identity: %v, %+v", err, again)
	}
	if err := os.WriteFile(path, []byte(runtimeSource+"# Subscription refreshed.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	next, err := PrepareCampus(r, false)
	if err != nil || next.ProfileUID == first.ProfileUID || next.ProviderPath != first.ProviderPath {
		t.Fatalf("source update did not change only source identity: %v, %+v", err, next)
	}
	for _, change := range first.Files {
		if err := os.WriteFile(change.Path, []byte(change.Content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r.Values = nil
	repeated, err := PrepareCampus(r, false)
	if err != nil || len(repeated.Files) != 0 {
		t.Fatalf("owned feed could not be reused after source refresh: %v, %+v", err, repeated.Files)
	}
}

func TestPrepareRuntimeCampusProviderOnlyAvoidsRecursiveGroups(t *testing.T) {
	for _, groups := range []string{
		"proxy-groups: [{name: original, type: select, use: [subscription]}]\n",
		"proxy-groups: [{name: original, type: select, include-all: true}]\n",
		"proxy-groups: [{name: original, type: select, proxies: [cycle]}, {name: cycle, type: select, proxies: [original]}]\n",
	} {
		r, _ := runtimeFixture(t, "mixed-port: 17890\nmode: global\nproxy-providers: {subscription: {type: file, path: ./nodes.yaml}}\n"+groups+"rules: ['MATCH,original']\n")
		r.Outside = "dynamic-provider-node"
		p, err := PrepareCampus(r, false)
		if err != nil {
			t.Fatal(err)
		}
		var runtime map[string]any
		if err := yaml.Unmarshal([]byte(p.Payload), &runtime); err != nil {
			t.Fatal(err)
		}
		group := list(runtime, "proxy-groups")[len(list(runtime, "proxy-groups"))-1].(map[string]any)
		if group["name"] != "INTERNET" || group["include-all"] == true || !reflect.DeepEqual(list(group, "use"), []any{"subscription"}) {
			t.Fatalf("provider-only INTERNET is recursive or lacks provider: %v", group)
		}
		for _, member := range list(group, "proxies") {
			if member == r.Outside {
				t.Fatalf("dynamic provider node became an undefined static proxy: %v", group)
			}
			if member == "original" && strings.Contains(groups, "include-all: true") || member == "cycle" || member == "original" && strings.Contains(groups, "proxies: [cycle]") {
				t.Fatalf("unsafe outside group selected: %v", group)
			}
		}
	}
}

func TestPrepareRuntimeCampusRejectsUnrelatedFeedFile(t *testing.T) {
	r, _ := runtimeFixture(t, runtimeSource)
	p, err := PrepareCampus(r, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, contents := range []string{"private: keep\n", "payload: [42]\n", "payload: []\nprivate: keep\n"} {
		if err := os.WriteFile(p.ProviderPath, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := PrepareCampus(r, false); err == nil {
			t.Fatalf("unrelated feed accepted: %q", contents)
		}
		after, err := os.ReadFile(p.ProviderPath)
		if err != nil || string(after) != contents {
			t.Fatal("rejected feed was changed")
		}
	}
}

func TestPrepareRuntimeCampusKeepsLocalLoopAliasesExcluded(t *testing.T) {
	var runtime map[string]any
	if err := yaml.Unmarshal([]byte(runtimeSource), &runtime); err != nil {
		t.Fatal(err)
	}
	runtime["proxies"] = append(list(runtime, "proxies"),
		map[string]any{"name": "local-alias", "type": "socks5", "server": "127.0.0.1", "port": 11080},
		map[string]any{"name": "chained-alias", "type": "ss", "server": "remote.invalid", "port": 443, "dialer-proxy": "local-alias"})
	runtime["proxy-groups"] = append(list(runtime, "proxy-groups"),
		map[string]any{"name": "unsafe-group", "type": "select", "proxies": []any{"chained-alias"}},
		map[string]any{"name": "recursive-all", "type": "select", "include-all": true})
	raw, err := yaml.Marshal(runtime)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := runtimeFixture(t, string(raw))
	p, err := PrepareCampus(r, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.UnsafeNodes, []string{"chained-alias", "local-alias", "recursive-all", "unsafe-group"}) {
		t.Fatalf("known loop guards were lost: %v", p.UnsafeNodes)
	}
	if err := yaml.Unmarshal([]byte(p.Payload), &runtime); err != nil {
		t.Fatal(err)
	}
	groups := list(runtime, "proxy-groups")
	group := groups[len(groups)-1].(map[string]any)
	for _, name := range append([]string{"FDU", "FDU-ROUTER", "GLOBAL", "INTERNET"}, p.UnsafeNodes...) {
		if !strings.Contains(text(group["exclude-filter"]), name) {
			t.Fatalf("INTERNET did not exclude %s: %v", name, group)
		}
	}
}

func TestPrepareRuntimeCampusRejectsConflictingNamesAndInvalidSource(t *testing.T) {
	for _, tc := range []struct{ name, suffix, mode string }{
		{name: "proxy", suffix: "proxies: [{name: FDU-ROUTER, type: ss, server: external.invalid, port: 443}]\n"},
		{name: "group", suffix: "proxy-groups: [{name: INTERNET, type: select, proxies: [node-a]}]\n"},
		{name: "listener", suffix: "listeners: [{name: FDU-FALLBACK, type: mixed, listen: 127.0.0.1, port: 19000, proxy: original}]\n"},
		{name: "provider", suffix: "rule-providers: {FDU-CAMPUS: {type: http, behavior: classical, url: https://external.invalid/rules, proxy: DIRECT}}\n"},
		{name: "live direct mode", mode: "direct"},
		{name: "missing live mode", mode: " "},
		{name: "oversized source", suffix: strings.Repeat("#", 8*1024*1024)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := runtimeSource
			if tc.suffix != "" {
				var runtime, replacement map[string]any
				if len(tc.suffix) > 8*1024*1024-1 {
					source += tc.suffix
				} else {
					_ = yaml.Unmarshal([]byte(source), &runtime)
					_ = yaml.Unmarshal([]byte(tc.suffix), &replacement)
					for key, value := range replacement {
						runtime[key] = value
					}
					data, _ := yaml.Marshal(runtime)
					source = string(data)
				}
			}
			r, path := runtimeFixture(t, source)
			if tc.mode != "" {
				r.ClashMode = tc.mode
			}
			before, _ := os.ReadFile(path)
			if _, err := PrepareCampus(r, false); err == nil {
				t.Fatal("conflicting or invalid runtime accepted")
			}
			after, _ := os.ReadFile(path)
			if string(after) != string(before) {
				t.Fatal("rejected source was modified")
			}
		})
	}
}
