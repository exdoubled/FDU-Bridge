package clash

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAbsentClientIsRetryableButAmbiguousClientsAreNot(t *testing.T) {
	if _, err := selectClientTarget(nil); !errors.Is(err, ErrClientNotRunning) {
		t.Fatalf("absent client not classified: %v", err)
	}
	if _, err := selectClientTarget([]ClientTarget{{Kind: "verge"}, {Kind: "runtime"}}); err == nil || errors.Is(err, ErrClientNotRunning) {
		t.Fatalf("ambiguous targets must require user selection: %v", err)
	}
}

func TestOfflineRuntimeOnlyRetriesMissingControllerBinding(t *testing.T) {
	for _, tt := range []struct {
		name    string
		content string
		wait    bool
	}{
		{"cli-only", "proxies: []\n", true},
		{"invalid-yaml", "proxies: [\n", false},
		{"preferences", "operationMode: standard\n", false},
		{"remote-tcp", "proxies: []\nexternal-controller: 192.0.2.1:9090\n", false},
		{"wildcard-tcp", "proxies: []\nexternal-controller: 0.0.0.0:9090\n", false},
		{"invalid-port", "proxies: []\nexternal-controller: 127.0.0.1:0\n", false},
		{"relative-socket", "proxies: []\nexternal-controller-unix: relative.sock\n", false},
		{"remote-pipe", "proxies: []\nexternal-controller-pipe: '\\\\server\\pipe\\clash'\n", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			file := writeDiscoveryFile(t, filepath.Join(t.TempDir(), "runtime.yaml"), tt.content)
			_, err := resolvePreferredClientTarget(file, nil)
			if err == nil || errors.Is(err, ErrClientNotRunning) != tt.wait {
				t.Fatalf("classification = %v; want retryable %v", err, tt.wait)
			}
		})
	}
}

const discoveryRuntime = "external-controller: 127.0.0.1:19090\nsecret: private-controller-secret\nproxies: [{name: test, type: ss, server: example.invalid, port: 443, password: private-proxy-password}]\nproxy-groups: []\nrules: ['MATCH,DIRECT']\n"

func writeDiscoveryFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDiscoverClientQuotedCoreArguments(t *testing.T) {
	root := filepath.Join(t.TempDir(), "client's data")
	path := writeDiscoveryFile(t, filepath.Join(root, "config.yaml"), discoveryRuntime)
	exe := filepath.Join(t.TempDir(), "core app", "mihomo.exe")
	for _, arg := range []string{`-f "` + path + `"`, `--config="` + path + `"`, `--dir="` + root + `"`} {
		t.Run(arg, func(t *testing.T) {
			targets := discoverClientProcesses([]clientProcess{{name: "mihomo.exe", line: `"` + exe + `" ` + arg}})
			if len(targets) != 1 || targets[0].Kind != "runtime" || targets[0].RuntimeFile != path {
				t.Fatalf("quoted runtime not found: %#v", targets)
			}
			encoded, err := json.Marshal(targets)
			if err != nil || strings.Contains(string(encoded), "private-") {
				t.Fatalf("target must not expose config credentials: %v", err)
			}
		})
	}
}

func TestDiscoverClientUnquotedApostrophePath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "client's-data")
	path := writeDiscoveryFile(t, filepath.Join(root, "config.yaml"), discoveryRuntime)
	got := discoverClientProcesses([]clientProcess{{name: "mihomo.exe", line: `mihomo -f ` + path}})
	if len(got) != 1 || got[0].RuntimeFile != path {
		t.Fatalf("literal apostrophe in unquoted path was treated as quoting: %#v", got)
	}
}

func TestDiscoverClientRejectsPreferencesRemoteAndValidationCores(t *testing.T) {
	for name, content := range map[string]string{
		"preferences": "external-controller: 127.0.0.1:19090\nmodeSelected: true\ncurrentProfile: cached\n",
		"remote":      strings.Replace(discoveryRuntime, "127.0.0.1", "203.0.113.8", 1),
		"remote pipe": strings.Replace(discoveryRuntime, "external-controller: 127.0.0.1:19090", `external-controller-pipe: '\\server\pipe\controller'`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			path := writeDiscoveryFile(t, filepath.Join(t.TempDir(), "config.yaml"), content)
			if got := discoverClientProcesses([]clientProcess{{name: "mihomo.exe", line: `mihomo -f "` + path + `"`}}); len(got) != 0 {
				t.Fatalf("unsafe or non-runtime config accepted: %#v", got)
			}
		})
	}
	path := writeDiscoveryFile(t, filepath.Join(t.TempDir(), "config.yaml"), discoveryRuntime)
	if got := discoverClientProcesses([]clientProcess{{name: "mihomo.exe", line: `mihomo -t -f "` + path + `"`}}); len(got) != 0 {
		t.Fatalf("config validation process is not a running core: %#v", got)
	}
}

func TestDiscoverClientExplicitConfigDoesNotFallbackToCachedDefault(t *testing.T) {
	root := t.TempDir()
	writeDiscoveryFile(t, filepath.Join(root, "config.yaml"), discoveryRuntime)
	for _, config := range []string{"relative.yaml", filepath.Join(root, "missing.yaml")} {
		got := discoverClientProcesses([]clientProcess{{name: "mihomo.exe", line: `mihomo -d "` + root + `" -f "` + config + `"`}})
		if len(got) != 0 {
			t.Fatalf("explicit config must take precedence over cached default: %#v", got)
		}
	}
}

func TestDiscoverClientExplicitConfigDoesNotUseVergeSibling(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"profiles.yaml", "verge.yaml", "clash-verge.yaml"} {
		writeDiscoveryFile(t, filepath.Join(root, name), discoveryRuntime)
	}
	path := writeDiscoveryFile(t, filepath.Join(root, "active.yaml"), strings.Replace(discoveryRuntime, "19090", "19091", 1))
	got := discoverClientProcesses([]clientProcess{{name: "mihomo.exe", line: `mihomo -f "` + path + `"`}})
	if len(got) != 1 || got[0].Kind != "runtime" || got[0].RuntimeFile != path {
		t.Fatalf("explicit active runtime replaced by cached Verge sibling: %#v", got)
	}
}

func TestDiscoverClientFlClashHomeRequiresCoreProcess(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APPDATA", root)
	t.Setenv("XDG_DATA_HOME", root)
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	path := filepath.Join(root, "com.follow.clash", "config.yaml")
	if runtime.GOOS == "windows" {
		path = filepath.Join(root, "com.follow", "clash", "config.yaml")
	} else if runtime.GOOS == "darwin" {
		path = filepath.Join(root, "Library", "Application Support", "com.follow.clash", "config.yaml")
	}
	writeDiscoveryFile(t, path, discoveryRuntime)
	if got := discoverClientProcesses([]clientProcess{{name: "FlClash.exe", line: `FlClash`}}); len(got) != 0 {
		t.Fatalf("GUI alone must not establish an active FlClash core: %#v", got)
	}
	got := discoverClientProcesses([]clientProcess{{name: "FlClashCore.exe", line: `FlClashCore local-ipc-address`}})
	if len(got) != 1 || got[0].Name != "FlClash" || got[0].RuntimeFile != path {
		t.Fatalf("FlClashCore support-home runtime not found: %#v", got)
	}
}

func TestDiscoverClientDeduplicatesControllerAcrossRuntimePaths(t *testing.T) {
	root := t.TempDir()
	first := writeDiscoveryFile(t, filepath.Join(root, "first.yaml"), discoveryRuntime)
	second := writeDiscoveryFile(t, filepath.Join(root, "second.yaml"), discoveryRuntime)
	got := discoverClientProcesses([]clientProcess{
		{name: "mihomo.exe", line: `mihomo -f "` + first + `"`},
		{name: "mihomo.exe", line: `mihomo -f "` + second + `"`},
	})
	if len(got) != 1 {
		t.Fatalf("one controller must not appear as multiple core instances: %#v", got)
	}
}

func TestDiscoverClientKeepsDistinctCoreControllerOverrides(t *testing.T) {
	path := writeDiscoveryFile(t, filepath.Join(t.TempDir(), "runtime.yaml"), discoveryRuntime)
	got := discoverClientProcesses([]clientProcess{
		{name: "mihomo.exe", line: `mihomo -f "` + path + `" -ext-ctl 127.0.0.1:19091`},
		{name: "mihomo.exe", line: `mihomo -f "` + path + `" -ext-ctl 127.0.0.1:19092`},
	})
	if len(got) != 2 {
		t.Fatalf("separate core instances sharing an input file must remain ambiguous: %#v", got)
	}
}

func TestDiscoverClientDoesNotTreatGUIArgumentsAsCoreRuntime(t *testing.T) {
	root := t.TempDir()
	writeDiscoveryFile(t, filepath.Join(root, "config.yaml"), discoveryRuntime)
	got := discoverClientProcesses([]clientProcess{{name: "clash-party.exe", line: `clash-party -d "` + root + `"`}})
	if len(got) != 0 {
		t.Fatalf("GUI directory arguments do not identify an effective core runtime: %#v", got)
	}
}

func TestDiscoverClientUsesCoreControllerOverrides(t *testing.T) {
	root := filepath.Join(t.TempDir(), "party work")
	writeDiscoveryFile(t, filepath.Join(root, "config.yaml"), strings.Replace(discoveryRuntime, "external-controller: 127.0.0.1:19090\n", "", 1))
	exe := filepath.Join(t.TempDir(), "mihomo-party", "mihomo.exe")
	for name, args := range map[string]string{
		"pipe": `-ext-ctl-pipe "\\.\pipe\party-controller"`,
		"tcp":  `-ext-ctl=127.0.0.1:19091`,
		"unix": `-ext-ctl-unix "` + filepath.Join(t.TempDir(), "party.sock") + `"`,
	} {
		t.Run(name, func(t *testing.T) {
			got := discoverClientProcesses([]clientProcess{{name: "mihomo.exe", line: `"` + exe + `" -d "` + root + `" ` + args + ` -secret "private-cli-secret"`}})
			if len(got) != 1 || got[0].Name != "Clash Party" || got[0].Controller == nil || got[0].Controller.Network != name {
				t.Fatalf("core CLI controller not discovered: %#v", got)
			}
			if got[0].Controller.Secret != "private-cli-secret" {
				t.Fatal("CLI controller secret did not override runtime YAML secret")
			}
			encoded, err := json.Marshal(got)
			if err != nil || strings.Contains(string(encoded), "private-") || strings.Contains(string(encoded), `"Controller":`) || strings.Contains(string(encoded), `"controller":`) {
				t.Fatalf("CLI controller credentials exposed in target metadata: %v", err)
			}
		})
	}
}

func TestDiscoverClientRejectsRemoteCLIController(t *testing.T) {
	root := t.TempDir()
	writeDiscoveryFile(t, filepath.Join(root, "config.yaml"), discoveryRuntime)
	got := discoverClientProcesses([]clientProcess{{name: "mihomo.exe", line: `mihomo -d "` + root + `" -ext-ctl 203.0.113.8:9090`}})
	if len(got) != 0 {
		t.Fatalf("remote CLI override must not fall back to local YAML controller: %#v", got)
	}
}

func TestDiscoverClientPortableVerge(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, ".config", "verge-data")
	for _, name := range []string{"profiles.yaml", "verge.yaml", "clash-verge.yaml"} {
		writeDiscoveryFile(t, filepath.Join(root, name), discoveryRuntime)
	}
	got := discoverClientProcesses([]clientProcess{{name: "clash-verge.exe", line: `"` + filepath.Join(base, "clash-verge.exe") + `"`}})
	if len(got) != 1 || got[0].Kind != "verge" || got[0].Root != root {
		t.Fatalf("associated portable Verge root not found: %#v", got)
	}
}

func TestResolveClientTargetSupportsLocalPipeAndUnixRuntime(t *testing.T) {
	for name, endpoint := range map[string]string{
		"pipe": `external-controller-pipe: '\\.\pipe\client-controller'`,
		"unix": "external-controller-unix: '" + filepath.Join(t.TempDir(), "client.sock") + "'",
	} {
		t.Run(name, func(t *testing.T) {
			path := writeDiscoveryFile(t, filepath.Join(t.TempDir(), "runtime.yaml"), strings.Replace(discoveryRuntime, "external-controller: 127.0.0.1:19090", endpoint, 1))
			if _, err := resolvePreferredClientTarget(path, nil); err != nil {
				t.Fatalf("local %s runtime rejected: %v", name, err)
			}
		})
	}
}

func TestDiscoverClientDeduplicatesVergeAndItsCore(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"profiles.yaml", "verge.yaml", "clash-verge.yaml"} {
		content := "current: test\n"
		if name == "clash-verge.yaml" {
			content = discoveryRuntime
		}
		writeDiscoveryFile(t, filepath.Join(root, name), content)
	}
	processes := []clientProcess{
		{name: "mihomo.exe", line: `mihomo -f "` + filepath.Join(root, "clash-verge.yaml") + `"`},
		{name: "clash-verge.exe", line: `clash-verge --dir "` + root + `"`},
	}
	got := discoverClientProcesses(processes)
	if len(got) != 1 || got[0].Kind != "verge" || got[0].Root != root {
		t.Fatalf("Verge and its runtime should be one target: %#v", got)
	}
}

func TestResolveClientTargetValidatesManualRuntime(t *testing.T) {
	path := writeDiscoveryFile(t, filepath.Join(t.TempDir(), "runtime.yaml"), discoveryRuntime)
	target, err := resolvePreferredClientTarget(path, nil)
	if err != nil || target.Kind != "runtime" || target.RuntimeFile != path {
		t.Fatalf("manual runtime not resolved: %#v, %v", target, err)
	}
	prefs := writeDiscoveryFile(t, filepath.Join(t.TempDir(), "config.yaml"), "operationMode: standard\n")
	if _, err := resolvePreferredClientTarget(prefs, nil); err == nil {
		t.Fatal("manual app preferences should not be accepted as runtime")
	}
}

func TestResolveClientTargetRetainsRunningControllerForPreferredFile(t *testing.T) {
	path := writeDiscoveryFile(t, filepath.Join(t.TempDir(), "runtime.yaml"), strings.Replace(discoveryRuntime, "external-controller: 127.0.0.1:19090\n", "", 1))
	controller := &ControlAPI{Network: "tcp", Address: "127.0.0.1:19091", Secret: "private-cli-secret"}
	live := ClientTarget{Kind: "runtime", Name: "Clash Party", Root: filepath.Dir(path), RuntimeFile: path, Controller: controller}
	got, err := resolvePreferredClientTarget(path, []ClientTarget{live})
	if err != nil || got.Controller != controller {
		t.Fatalf("remembered runtime lost its verified process controller: %v", err)
	}
	other := live
	other.Controller = &ControlAPI{Network: "tcp", Address: "127.0.0.1:19092"}
	if _, err := resolvePreferredClientTarget(path, []ClientTarget{live, other}); err == nil {
		t.Fatal("same preferred file with distinct running controllers must be ambiguous")
	}
}

func TestResolveClientTargetRetainsNativeVergeDirectory(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"profiles.yaml", "verge.yaml", "clash-verge.yaml"} {
		writeDiscoveryFile(t, filepath.Join(root, name), "current: test\n")
	}
	got, err := resolvePreferredClientTarget(root, nil)
	if err != nil || got.Kind != "verge" || got.Root != root {
		t.Fatalf("validated Verge directory must retain native integration: %#v, %v", got, err)
	}
}

func TestResolveClientTargetRequiresSelectionWhenAmbiguous(t *testing.T) {
	targets := []ClientTarget{{Kind: "runtime", RuntimeFile: "first.yaml"}, {Kind: "runtime", RuntimeFile: "second.yaml"}}
	if _, err := selectClientTarget(targets); err == nil {
		t.Fatal("multiple running cores require explicit selection")
	}
}
