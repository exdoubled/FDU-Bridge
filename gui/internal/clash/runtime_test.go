package clash

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mythologyli/zju-connect/gui/internal/fsx"
	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

func TestManualControllerMustBeLocal(t *testing.T) {
	for _, address := range []string{"http://192.168.1.2:9090", "http://user:pass@127.0.0.1:9090", "http://127.0.0.1:9090/other", "http://127.0.0.1:0", "https://example.com"} {
		if _, err := ParseController(address, "secret"); err == nil {
			t.Errorf("accepted %s", address)
		}
	}
	api, err := ParseController("http://127.0.0.1:9090", "secret")
	if err != nil || api.Address != "127.0.0.1:9090" || api.Secret != "secret" {
		t.Fatalf("local controller: %+v %v", api, err)
	}
}

func TestManualBindingCanSupplyControllerOutsideRuntimeYAML(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	raw := "mixed-port: 17890\nexternal-controller: 127.0.0.1:19091\nexternal-controller-pipe: '\\\\.\\pipe\\stale-controller'\nproxies:\n  - {name: node-a, type: socks5, server: example.invalid, port: 443}\n"
	if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	target, err := ManualTarget(file, "http://127.0.0.1:19090", "local-secret")
	if err != nil || target.Controller == nil || target.Controller.Network != "tcp" || target.Controller.Address != "127.0.0.1:19090" || target.Controller.Secret != "local-secret" || target.RuntimeFile != file {
		t.Fatalf("manual binding: %+v %v", target, err)
	}
	target, err = ManualTarget(file, "", "secret-only")
	if err != nil || target.Controller.Secret != "secret-only" {
		t.Fatalf("secret override: %+v %v", target, err)
	}
}

type restoreRuntimeAPI struct {
	*campusAPI
	payload string
}

func (f *restoreRuntimeAPI) Do(method, path string, payload, out any) error {
	if method == "PUT" && path == "/configs?force=true" {
		f.payload = payload.(map[string]string)["payload"]
		f.missingSupport = true
		return nil
	}
	return f.campusAPI.Do(method, path, payload, out)
}

func TestRuntimeDetachPreservesLatestSubscriptionAndLiveMode(t *testing.T) {
	c, api, a := campusFixture(t, "rule")
	file := filepath.Join(t.TempDir(), "config.yaml")
	a.RuntimeFile, a.RuntimeHash, a.Active = file, "previous-subscription", true
	c.ConfigureTarget(file, nil)
	latest := "mode: global\nmixed-port: 17890\nproxies:\n  - {name: node-a, type: socks5, server: 127.0.0.1, port: 18090}\n  - {name: node-b, type: socks5, server: 127.0.0.1, port: 18091}\nrules: [MATCH,DIRECT]\n"
	if err := os.WriteFile(file, []byte(latest), 0600); err != nil {
		t.Fatal(err)
	}
	f := &restoreRuntimeAPI{campusAPI: api}
	api.global = "FDU-ROUTER"
	c.API = f
	next, err := c.DeactivateCampus(a)
	if err != nil || next.Active {
		t.Fatalf("detach: %+v %v", next, err)
	}
	if !strings.Contains(f.payload, "node-b") || !strings.Contains(f.payload, "mode: rule") || strings.Contains(f.payload, "FDU-ROUTER") {
		t.Fatalf("restored stale or injected configuration: %s", f.payload)
	}
	if fsx.HashFile(file) != fsx.Hash([]byte(latest)) {
		t.Fatal("modified subscription source")
	}
	// Disabling during the settling interval must not replay the old source.
	c.ConfigureFollowingTarget(filepath.Join(filepath.Dir(file), "next-runtime.yaml"), nil)
	next, err = c.DeactivateCampus(a)
	if err != nil || next.Active {
		t.Fatalf("stop during migration: %+v %v", next, err)
	}
}

type runtimeLifecycleAPI struct {
	*campusAPI
	payload    string
	failImport bool
}

func (f *runtimeLifecycleAPI) Do(method, path string, payload, out any) error {
	if method == "GET" && path == "/configs" {
		raw, _ := json.Marshal(map[string]any{"mode": f.mode, "mixed-port": 17890, "tun": map[string]any{"enable": true}})
		return json.Unmarshal(raw, out)
	}
	if method == "PUT" && path == "/configs?force=true" {
		f.payload = payload.(map[string]string)["payload"]
		if f.failImport {
			f.failImport = false
			return errors.New("simulated reload failure")
		}
		var config map[string]any
		if err := yaml.Unmarshal([]byte(f.payload), &config); err != nil {
			return err
		}
		if err := f.campusAPI.Do(method, path, payload, out); err != nil {
			return err
		}
		f.missingSupport, f.mode = !containsRuntimeFDU(config), text(config["mode"])
		return nil
	}
	return f.campusAPI.Do(method, path, payload, out)
}

func TestRuntimeLifecycleFollowsModesSubscriptionsAndRetainedExtension(t *testing.T) {
	c, baseAPI, a := campusFixture(t, "global")
	api := &runtimeLifecycleAPI{campusAPI: baseAPI}
	api.missingSupport = true
	c.API = api
	file := filepath.Join(a.Root, "effective.yaml")
	source := "mode: global\nmixed-port: 7890\nproxies: [{name: node-a, type: ss, server: example.invalid, port: 443}]\nproxy-groups: []\nrules: ['MATCH,node-a']\n"
	write := func(raw string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(source)
	c.ConfigureTarget(file, nil)
	config, _ := toml.Marshal(map[string]any{"protocol": "atrust", "auto_detect_interface": true,
		"socks_bind": "127.0.0.1:11080", "http_bind": "127.0.0.1:11081",
		"routing": map[string]any{"enabled": true, "rules_file": a.Rules, "upstream_proxy": "socks5://127.0.0.1:17902"}})
	if err := os.WriteFile(a.Config, config, 0600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-5 * time.Second)
	if err := os.Chtimes(a.Config, past, past); err != nil {
		t.Fatal(err)
	}
	plan, err := c.PrepareCampus(a.Root, a.Config, nil)
	if err != nil || plan.NeedsFduStart {
		t.Fatalf("prepare: %v", err)
	}
	api.failImport = true
	if _, err := c.Import(plan); err == nil {
		t.Fatal("accepted failed reload")
	}
	var restored map[string]any
	_ = yaml.Unmarshal([]byte(api.payload), &restored)
	if restored["mixed-port"] != 17890 || restored["tun"].(map[string]any)["enable"] != true || containsRuntimeFDU(restored) {
		t.Fatal("rollback lost live settings")
	}
	_, a, err = c.ImportCampus(plan)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestCampusFollowAcknowledgementProcess$", "--", "-config", a.Config)
	cmd.Env = append(os.Environ(), "FDU_TEST_FOLLOW_ACK=1")
	stdin, _ := cmd.StdinPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { stdin.Close(); _ = cmd.Wait() }()
	deadline := time.Now().Add(5 * time.Second)
	for !fileExistsForFollow(a.Rules + ".status.json") {
		if time.Now().After(deadline) {
			t.Fatal("missing test core acknowledgement")
		}
		time.Sleep(10 * time.Millisecond)
	}
	a, err = c.ActivateCampus(a)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"rule", "global", "subscription", "extension", "path"} {
		if change == "subscription" {
			source += "ipv6: false\n"
			write(source)
			api.missingSupport, api.global = true, "DIRECT"
		} else if change == "path" {
			oldFile := file
			file = filepath.Join(a.Root, "new-effective.yaml")
			write(source)
			c.ConfigureTarget(file, nil)
			api.missingSupport, api.global = true, "DIRECT"
			if fsx.HashFile(oldFile) != fsx.Hash([]byte(source)) {
				t.Fatal("lost old client source")
			}
		} else if change == "extension" {
			source = api.payload
			write(source)
		} else {
			api.mode = change
		}
		a, err = c.FollowCampus(a)
		if err != nil || !a.Active || a.ClashMode != api.mode {
			t.Fatalf("%s: %+v %v", change, a, err)
		}
		if fsx.HashFile(file) != fsx.Hash([]byte(source)) || fsx.HashFile(a.Config) != fsx.Hash(config) {
			t.Fatal("modified client source or core config")
		}
		var applied map[string]any
		_ = yaml.Unmarshal([]byte(api.payload), &applied)
		if applied["mixed-port"] != 17890 || applied["tun"].(map[string]any)["enable"] != true {
			t.Fatal("lost live ports or TUN")
		}
	}
	before := len(api.requests)
	c.ConfigureTarget(filepath.Join(a.Root, "different-client.yaml"), nil)
	if _, err = c.DeactivateCampus(a); err == nil || len(api.requests) != before {
		t.Fatal("restored into another client")
	}
	c.ConfigureTarget(file, nil)
	a, err = c.DeactivateCampus(a)
	if err != nil || a.Active || api.global == "FDU-ROUTER" {
		t.Fatalf("detach extension: %+v %v", a, err)
	}
	if fsx.HashFile(file) != fsx.Hash([]byte(source)) {
		t.Fatal("rewrote installed extension")
	}
	if fsx.HashFile(a.ProviderPath) != fsx.Hash([]byte("payload: []\n")) {
		t.Fatal("left campus rules active")
	}
}

func TestExportedPatchSurvivesSubscriptionNamesChanging(t *testing.T) {
	c, _, a := campusFixture(t, "global")
	plan := Plan{ProviderPath: a.ProviderPath, Payload: `proxies: [{name: FDU-ROUTER, type: socks5, server: 127.0.0.1, port: 1080}]
proxy-groups: [{name: INTERNET, type: select, proxies: [old-node], use: [old-provider], include-all: true}]
listeners: [{name: FDU-FALLBACK, type: mixed, listen: 127.0.0.1, port: 17902, proxy: INTERNET}]
rule-providers: {FDU-CAMPUS: {type: http, behavior: classical, url: http://127.0.0.1/rules}}
`}
	script, fragment, err := c.ExportPatch(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fragment, "old-node") || strings.Contains(fragment, "old-provider") {
		t.Fatal("export tied to current subscription")
	}
	if node, err := exec.LookPath("node"); err == nil {
		check := `
for (const name of ["new-node", "another-node"]) {
 const source = {proxies: [{name, type: "ss"}], "proxy-providers": {[name]: {type: "http"}}, rules: ["MATCH," + name]};
 const result = main(source);
 main(result);
 if (result.proxies.length !== 2 || result.rules.length !== 2 || result.rules[1] !== "MATCH," + name) throw Error("lost or duplicated subscription");
 const group = result["proxy-groups"][0];
 if (group.proxies.join() !== "DIRECT" || !group["include-all-proxies"] || !group["include-all-providers"] || group.use) throw Error("stale group references");
}
`
		if output, err := exec.Command(node, "-e", script+check).CombinedOutput(); err != nil {
			t.Fatalf("export script: %s %v", output, err)
		}
	}
}
