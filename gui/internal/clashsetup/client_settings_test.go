package clashsetup

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestClientSettingsCloneIsReadOnlyAndCoversDefaults(t *testing.T) {
	r := fixture(t)
	source := "protocol='atrust'\nserver_address='vpn.example.invalid'\nclient_data_file='session.json'\nzju_dns_server='192.0.2.53'\n[custom_section]\nkeep='value'\n"
	_ = os.WriteFile(r.Config, []byte(source), 0600)
	_ = os.WriteFile(filepath.Join(filepath.Dir(r.Config), "session.json"), []byte("private-session"), 0600)
	r.Directory = filepath.Join(r.Directory, "gui")
	reply, err := clientSettings(r)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Values["client_data_file"] != filepath.Join(r.Directory, "client_data.json") || len(reply.Copies) != 1 {
		t.Fatal("Did not isolate login state")
	}
	if _, old := reply.Values["zju_dns_server"]; old {
		t.Fatal("Deprecated alias can overwrite GUI changes")
	}
	if _, err := os.Stat(r.Directory); !os.IsNotExist(err) {
		t.Fatal("Preview wrote files")
	}
	got, _ := os.ReadFile(r.Config)
	if string(got) != source {
		t.Fatal("Changed imported configuration")
	}
	covered := map[string]bool{}
	for _, field := range reply.Fields {
		covered[field.Key] = true
	}
	for key, value := range settingsDefaults() {
		if _, ok := reply.Values[key]; !ok {
			t.Fatalf("Missing field %s", key)
		}
		if nested, ok := value.(map[string]any); ok {
			for sub := range nested {
				if !covered[key+"."+sub] {
					t.Fatalf("Missing GUI field %s.%s", key, sub)
				}
			}
		} else if !covered[key] {
			t.Fatalf("Missing GUI field %s", key)
		}
	}
	_ = os.MkdirAll(r.Directory, 0700)
	privateConfig := filepath.Join(r.Directory, "generated.toml")
	_ = os.WriteFile(privateConfig, []byte(fmt.Sprintf("protocol='atrust'\nclient_data_file=%q\n", filepath.Join(filepath.Dir(r.Config), "session.json"))), 0600)
	_ = os.WriteFile(filepath.Join(r.Directory, "client_data.json"), []byte("old-private-state"), 0600)
	r.Config = privateConfig
	again, err := clientSettings(r)
	if err != nil || !again.Adjusted || len(again.Copies) != 1 || again.Copies[0].DestinationHash != digest([]byte("old-private-state")) {
		t.Fatalf("Did not isolate external state in an internal TOML: %v", err)
	}
}

func TestClientSettingsRoundTripPreservesUnknownAndTableEntries(t *testing.T) {
	r := fixture(t)
	r.Operation = "settings-render"
	r.Values = map[string]any{"protocol": "atrust", "server_address": "vpn.example.invalid", "server_port": float64(443), "unknown": map[string]any{"keep": "original"}, "port_forwarding": []any{map[string]any{"network_type": "tcp", "bind_address": "127.0.0.1:19898", "remote_address": "192.0.2.10:22"}}}
	reply, err := clientSettings(r)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = toml.Unmarshal([]byte(reply.Content), &decoded); err != nil {
		t.Fatal(err)
	}
	if number(decoded["server_port"]) != 443 || mapping(decoded, "unknown")["keep"] != "original" || len(list(decoded, "port_forwarding")) != 1 {
		t.Fatal("Configuration data lost")
	}
	r.Values["routing"] = map[string]any{"enabled": true, "upstream_proxy": "socks5://127.0.0.1:17902"}
	r.Values["auto_detect_interface"] = true
	if _, err = clientSettings(r); err == nil {
		t.Fatal("Accepted port forwarding with selective routing")
	}
}
