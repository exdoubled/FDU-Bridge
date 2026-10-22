package clashsetup

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mythologyli/zju-connect/routing"
	"gopkg.in/yaml.v3"
)

func TestFirstImportSeedsExampleRulesAndKeepsExistingFile(t *testing.T) {
	r := fixture(t)
	example, err := os.ReadFile(filepath.Join("..", "..", "..", "config.toml.example"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.Config, example, 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := Prepare(r)
	if err != nil {
		t.Fatal(err)
	}
	var seeded routing.RuleSet
	for _, change := range plan.Files {
		if change.Path == plan.Rules {
			seeded, err = routing.ParseRules([]byte(change.Content))
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(seeded.Domains) != 7 || len(seeded.DomainSuffixes) != 1 || len(seeded.IPCIDRs) != 5 || seeded.Disabled == nil {
		t.Fatalf("example presets were lost: %+v", seeded)
	}
	if _, err := os.Stat(plan.Rules); !os.IsNotExist(err) {
		t.Fatal("preview wrote the rules file")
	}
	for _, existing := range []string{
		`{"version":2,"domains":["kept.fudan.edu.cn"],"domain_suffixes":[],"ip_cidrs":[],"disabled":{"domains":["kept.fudan.edu.cn"],"domain_suffixes":[],"ip_cidrs":[]}}`,
		`{"version":2,"domains":[],"domain_suffixes":[],"ip_cidrs":[],"disabled":{"domains":[],"domain_suffixes":[],"ip_cidrs":[]}}`,
	} {
		if err := os.WriteFile(plan.Rules, []byte(existing), 0600); err != nil {
			t.Fatal(err)
		}
		again, err := Prepare(r)
		if err != nil {
			t.Fatal(err)
		}
		for _, change := range again.Files {
			if change.Path == plan.Rules {
				t.Fatal("existing rules or disabled switches were overwritten")
			}
		}
		got, _ := os.ReadFile(plan.Rules)
		if string(got) != existing {
			t.Fatal("preview changed existing rules")
		}
	}
}

func fixture(t *testing.T) Request {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "clash")
	work := filepath.Join(dir, "fdu")
	if err := os.MkdirAll(filepath.Join(root, "profiles"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(work, 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"profiles.yaml":    "current: sub\nitems:\n- uid: sub\n  type: remote\n  name: Example subscription\n  url: https://subscription.invalid/private-token\n  file: sub.yaml\n  option: {merge: m, proxies: p, groups: g}\n- {uid: m, type: merge, file: m.yaml}\n- {uid: p, type: proxies, file: p.yaml}\n- {uid: g, type: groups, file: g.yaml}\n",
		"profiles/m.yaml":  "dns: {enable: true}\n",
		"profiles/p.yaml":  "prepend: [{name: keep, type: socks5, server: 127.0.0.1, port: 12345}]\nappend: []\ndelete: []\n",
		"profiles/g.yaml":  "prepend: []\nappend: []\ndelete: []\n",
		"clash-verge.yaml": "mixed-port: 17890\nmode: global\nsecret: keep-private-secret\nproxies:\n- {name: node-a, type: ss, server: node.invalid, port: 443, cipher: aes-128-gcm, password: keep-password}\nproxy-groups: [{name: original, type: select, proxies: [node-a]}]\nrules: ['MATCH,original']\n",
		"verge.yaml":       "enable_system_proxy: false\nenable_proxy_guard: false\n",
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	config := filepath.Join(work, "config.toml")
	if err := os.WriteFile(config, []byte("protocol='atrust'\nserver_address='vpn.fudan.edu.cn'\nauth_type='auth/httpsOauth2'\nlogin_domain='id.fudan.edu.cn'\nsocks_bind='127.0.0.1:11080'\nhttp_bind='127.0.0.1:11081'\n[routing]\nenabled=false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return Request{Root: root, Config: config, Directory: work, OccupiedPorts: []int{17902}}
}

func TestPrepareIsReadOnlyAndKeepsSubscriptionAndOtherSettings(t *testing.T) {
	r := fixture(t)
	old, _ := os.ReadFile(filepath.Join(r.Root, "profiles.yaml"))
	plan, err := Prepare(r)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(r.Root, "profiles.yaml"))
	if string(old) != string(after) {
		t.Fatal("Preview changed live files")
	}
	if plan.UpstreamPort == 17902 || plan.Config == r.Config {
		t.Fatal("Did not generate a separate portable configuration")
	}
	if _, err := os.Stat(plan.Config); !os.IsNotExist(err) {
		t.Fatal("Preview wrote generated configuration")
	}
	if !strings.Contains(plan.Payload, "keep-password") || !strings.Contains(plan.Payload, "keep-private-secret") {
		t.Fatal("Unrelated runtime configuration was lost")
	}
	var runtime map[string]any
	if err := yaml.Unmarshal([]byte(plan.Payload), &runtime); err != nil {
		t.Fatal(err)
	}
	groups := runtime["proxy-groups"].([]any)
	if len(groups) != 2 {
		t.Fatalf("Original group lost: %v", groups)
	}
	var internet map[string]any
	for _, item := range groups {
		g := item.(map[string]any)
		if g["name"] == "INTERNET" {
			internet = g
		}
	}
	if internet["include-all"] != true || strings.Contains(plan.Payload, "- GLOBAL") {
		t.Fatal("New subscription nodes or front mode unavailable")
	}
	r.Mode = "fdu-first"
	front, err := Prepare(r)
	if err != nil || !strings.Contains(front.Payload, "proxy: GLOBAL") {
		t.Fatalf("FDU-first listener unavailable: %v", err)
	}
	for _, change := range plan.Files {
		if filepath.Base(change.Path) == "p.yaml" && !strings.Contains(change.Content, "keep") {
			t.Fatal("Existing extension node lost")
		}
	}
}

func TestRepeatedImportIsIdempotentAndRetainsMetadata(t *testing.T) {
	r := fixture(t)
	first, err := Prepare(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range first.Files {
		if err := os.WriteFile(change.Path, []byte(change.Content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r.Config = first.Config
	second, err := Prepare(r)
	if err != nil {
		t.Fatal(err)
	}
	if first.Payload != second.Payload {
		t.Fatal("Repeated import changes runtime configuration")
	}
	for _, change := range second.Files {
		actual, _ := os.ReadFile(change.Path)
		if string(actual) != change.Content {
			t.Fatalf("Repeated import duplicates or changes %s", filepath.Base(change.Path))
		}
	}
	metadata, _ := os.ReadFile(filepath.Join(r.Root, "profiles.yaml"))
	if !strings.Contains(string(metadata), "private-token") {
		t.Fatal("Subscription metadata lost")
	}
	if err := os.WriteFile(filepath.Join(r.Root, "clash-verge.yaml"), []byte(strings.Replace(first.Payload, "name: FDU-FALLBACK", "name: unrelated-listener", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(r); err == nil {
		t.Fatal("Reused another listener's port")
	}
}

func TestPrepareRejectsEscapingExtensionAndConflictingRouter(t *testing.T) {
	r := fixture(t)
	metadataPath := filepath.Join(r.Root, "profiles.yaml")
	metadata, _ := os.ReadFile(metadataPath)
	if err := os.WriteFile(metadataPath, []byte(strings.Replace(string(metadata), "file: p.yaml", "file: ../outside.yaml", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(r); err == nil {
		t.Fatal("Accepted extension outside profiles directory")
	}
	if err := os.WriteFile(metadataPath, metadata, 0600); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(r.Root, "clash-verge.yaml")
	runtime, _ := os.ReadFile(runtimePath)
	runtime = []byte(strings.Replace(string(runtime), "proxies:\n", "proxies:\n- {name: FDU-ROUTER, type: ss, server: remote.invalid, port: 443}\n", 1))
	if err := os.WriteFile(runtimePath, runtime, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(r); err == nil {
		t.Fatal("Overwrote unrelated node using the reserved name")
	}
}

func TestUnsupportedUpstreamsRequireASeparateFduConfig(t *testing.T) {
	for _, upstream := range []string{"", "socks5://[::1]:17903", "socks5://127.0.0.2:17903", "socks5://user:pass@127.0.0.1:17903", "socks5://127.0.0.1:70000", "socks5://127.0.0.1:17903/path"} {
		r := fixture(t)
		cfg := fmt.Sprintf("protocol='atrust'\nsocks_bind='127.0.0.1:11080'\nhttp_bind='127.0.0.1:11081'\nauto_detect_interface=true\n[routing]\nenabled=true\nrules_file=%q\nupstream_proxy=%q\n", filepath.Join(r.Directory, "rules.json"), upstream)
		if err := os.WriteFile(r.Config, []byte(cfg), 0600); err != nil {
			t.Fatal(err)
		}
		plan, err := Prepare(r)
		if err != nil || !plan.NeedsFduStart || plan.Config == r.Config {
			t.Fatalf("Unsupported upstream marked ready: %q, %v", upstream, err)
		}
	}
	r := fixture(t)
	b, _ := os.ReadFile(r.Config)
	_ = os.WriteFile(r.Config, []byte(strings.Replace(string(b), "127.0.0.1:11080", "[::1]:11080", 1)), 0600)
	if _, err := Prepare(r); err == nil {
		t.Fatal("Accepted unsupported bind")
	}
}

func TestLocalEndpointAliasesAreExcluded(t *testing.T) {
	r := fixture(t)
	path := filepath.Join(r.Root, "clash-verge.yaml")
	b, _ := os.ReadFile(path)
	changed := strings.Replace(string(b), "proxies:\n", "proxies:\n- {name: 'campus.+', type: socks5, server: localhost, port: 11080}\n- {name: chained, type: ss, server: node.invalid, port: 443, dialer-proxy: 'campus.+'}\n- {name: indirect, type: ss, server: node.invalid, port: 443, dialer-proxy: underlay}\n", 1)
	changed = strings.Replace(changed, "proxy-groups: [", "proxy-groups: [{name: underlay, type: select, proxies: ['campus.+']},", 1)
	_ = os.WriteFile(path, []byte(changed), 0600)
	plan, err := Prepare(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(plan.UnsafeNodes, ",") != "campus.+,chained,indirect,underlay" {
		t.Fatal("Local aliases not identified")
	}
	var runtime map[string]any
	_ = yaml.Unmarshal([]byte(plan.Payload), &runtime)
	for _, entry := range list(runtime, "proxy-groups") {
		g := entry.(map[string]any)
		if g["name"] == "INTERNET" {
			re := regexp.MustCompile(text(g["exclude-filter"]))
			if !re.MatchString("campus.+") || !re.MatchString("chained") || re.MatchString("campus-other") || list(g, "proxies")[0] != "node-a" {
				t.Fatal("Unsafe alias offered as external node")
			}
		}
	}
}
