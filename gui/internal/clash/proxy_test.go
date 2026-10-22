package clash

import (
	"reflect"
	"testing"
)

func TestParseNetworkSetupOutput(t *testing.T) {
	services := parseNetworkServices("An asterisk (*) denotes that a network service is disabled.\nWi-Fi\n*Thunderbolt Bridge\nUSB 10/100/1000 LAN\n")
	if !reflect.DeepEqual(services, []string{"Wi-Fi", "USB 10/100/1000 LAN"}) {
		t.Fatalf("services %v", services)
	}
	p := parseWebProxy("Enabled: Yes\nServer: 127.0.0.1\nPort: 7897\nAuthenticated Proxy Enabled: 0\n")
	if p != (webProxy{Enabled: true, Server: "127.0.0.1", Port: "7897"}) {
		t.Fatalf("proxy %+v", p)
	}
	if parseWebProxy("Enabled: No\nServer: \nPort: 0\n").Enabled {
		t.Fatal("disabled proxy read as enabled")
	}
}

func TestUnquoteGSettings(t *testing.T) {
	for in, want := range map[string]string{"'manual'\n": "manual", "uint32 7897\n": "7897", "8080": "8080", "''": ""} {
		if got := unquoteGSettings(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
