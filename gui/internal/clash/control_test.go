package clash

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiscoverReadsRuntimeControllerAddress(t *testing.T) {
	f := newFake(t)
	dir := t.TempDir()
	key := "external-controller-unix"
	if f.control.Network == "pipe" {
		key = "external-controller-pipe"
	}
	runtime := key + ": '" + strings.ReplaceAll(f.control.Address, "'", "''") + "'\nexternal-controller: ''\n"
	if err := os.WriteFile(filepath.Join(dir, "clash-verge.yaml"), []byte(runtime), 0o600); err != nil {
		t.Fatal(err)
	}
	api, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if state, err := (&Picker{API: api}).Load(); err != nil || state.Current != japan {
		t.Fatalf("discovered controller unusable: %+v %v", state, err)
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
