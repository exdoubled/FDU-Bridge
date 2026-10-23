package services

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/mythologyli/zju-connect/gui/internal/clash"
	"github.com/mythologyli/zju-connect/gui/internal/core"
)

type networkTestAPI func(string, string, any, any) error

func (f networkTestAPI) Do(method, path string, payload, out any) error {
	return f(method, path, payload, out)
}

func TestProbeEntrySelection(t *testing.T) {
	for _, test := range []struct {
		name, mode, config, bind, via, proxy string
		running, refusal                     bool
	}{
		{name: "mixed", config: `{"mixed-port":7890}`, via: "Clash 当前配置", proxy: "http://127.0.0.1:7890"},
		{name: "http", config: `{"port":7891}`, via: "Clash 当前配置", proxy: "http://127.0.0.1:7891"},
		{name: "refused", running: true, refusal: true},
		{name: "proxy-only", mode: "proxy", running: true, bind: "127.0.0.1:1081", via: "FDU 本地入口", proxy: "http://127.0.0.1:1081"},
		{name: "system", mode: "proxy", via: "系统路由"},
	} {
		t.Run(test.name, func(t *testing.T) {
			refusal := errors.New("controller refused")
			b := &Backend{runtime: &core.Runtime{}, saved: map[string]any{"http_bind": test.bind}}
			b.config.Integration.Mode = test.mode
			b.conn = &clash.Connection{API: networkTestAPI(func(method, path string, _, out any) error {
				if test.mode == "proxy" || method != "GET" || path != "/configs" {
					t.Fatalf("unexpected controller request: %s %s", method, path)
				}
				if test.refusal {
					return refusal
				}
				return json.Unmarshal([]byte(test.config), out)
			})}
			opts, via, err := NewNetworkService(b).probeOptions(test.running)
			if test.refusal {
				if !errors.Is(err, refusal) || opts.ProxyURL != "" {
					t.Fatalf("a controller failure silently changed the route: %+v / %v", opts, err)
				}
			} else if err != nil || via != test.via || opts.ProxyURL != test.proxy {
				t.Fatalf("wrong entry: %s / %+v / %v", via, opts, err)
			}
		})
	}
}

func TestDiagnosticControllerCacheFollowsSelection(t *testing.T) {
	file := filepath.Join(t.TempDir(), "runtime.yaml")
	write := func(port string) {
		t.Helper()
		if err := os.WriteFile(file, []byte("proxies: []\nexternal-controller: 127.0.0.1:"+port+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("9090")
	b := &Backend{runtime: &core.Runtime{}, conn: &clash.Connection{}}
	b.config.Integration.Mode, b.config.Integration.RuntimeFile = "manual", file
	s := NewNetworkService(b)
	check := func(want string) {
		t.Helper()
		api, err := s.controller()
		if err != nil || api.(clash.ControlAPI).Address != want {
			t.Fatalf("controller = %v / %v; want %s", api, err, want)
		}
	}
	check("127.0.0.1:9090")
	write("9091")
	check("127.0.0.1:9090") // A steady sample uses its cached immutable controller.
	s.forgetController()
	check("127.0.0.1:9091") // Failed reads re-resolve the runtime file on the next sample.
	b.config.Integration.Controller = "http://127.0.0.1:9092"
	check("127.0.0.1:9092")
	b.config.Integration.Mode = "auto"
	b.conn.ConfigureTarget(file, &clash.ControlAPI{Network: "tcp", Address: "127.0.0.1:9093"})
	check("127.0.0.1:9093")
	b.conn.ConfigureFollowingTarget(file, &clash.ControlAPI{Network: "tcp", Address: "127.0.0.1:9094"})
	check("127.0.0.1:9094")
}

func TestDiagnosticsDoNotCompeteWithIntegration(t *testing.T) {
	b := &Backend{runtime: &core.Runtime{}, conn: &clash.Connection{API: forbiddenAPI{t}}}
	b.clashBusy.Lock()
	defer b.clashBusy.Unlock()
	if api, err := NewNetworkService(b).controller(); err != nil || api == nil {
		t.Fatalf("read-only diagnostics were blocked by a write transaction: %v", err)
	}
}

func TestClashLookupAcceptsEitherAddressFamily(t *testing.T) {
	for _, failure := range []string{"A", "AAAA", "AAAA-rcode"} {
		t.Run(failure, func(t *testing.T) {
			api := networkTestAPI(func(_, path string, _, out any) error {
				u, _ := url.Parse(path)
				kind := u.Query().Get("type")
				if kind == failure {
					return errors.New("address family unavailable")
				}
				if kind+"-rcode" == failure {
					return json.Unmarshal([]byte(`{"Status":2}`), out)
				}
				return json.Unmarshal([]byte(`{"Status":0,"Answer":[{"type":1,"data":"203.0.113.8"}]}`), out)
			})
			addresses, err := lookupClash(api)(context.Background(), "library.fudan.edu.cn")
			if err != nil || len(addresses) != 1 {
				t.Fatalf("a successful address family was discarded: %v / %v", addresses, err)
			}
		})
	}
}
