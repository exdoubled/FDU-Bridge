package clash

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	japan    = "🇯🇵 日本 / \"测试\""
	hongKong = "🇭🇰 香港"
)

var pipeSequence atomic.Uint64

// fakeController serves the Clash API on an isolated pipe or socket; tests never touch the live controller.
type fakeController struct {
	t        *testing.T
	control  ControlAPI
	listener net.Listener
	mu       sync.Mutex

	requests                        []string
	mode, global, current           string
	members                         []string
	noInternet, listenerFront, loop bool
	failNextRead, unsafeGroup       bool
	chunked, ignoreSelection        bool
	written                         string
}

func newFake(t *testing.T) *fakeController {
	t.Helper()
	f := &fakeController{t: t, mode: "global", global: "FDU-ROUTER", current: japan, members: []string{japan, hongKong}}
	l, control := listenControl(t, fmt.Sprintf("fdu-gui-test-%d-%d", time.Now().UnixNano(), pipeSequence.Add(1)))
	f.listener, f.control = l, control
	server := &http.Server{Handler: http.HandlerFunc(f.serve)}
	go func() { _ = server.Serve(l) }()
	t.Cleanup(func() { _ = server.Close() })
	return f
}

func (f *fakeController) api() API { c := f.control; c.Timeout = 3 * time.Second; return c }

func (f *fakeController) with(fn func()) { f.mu.Lock(); defer f.mu.Unlock(); fn() }

func (f *fakeController) puts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []string
	for _, r := range f.requests {
		if !strings.HasPrefix(r, "GET ") {
			result = append(result, r)
		}
	}
	return result
}

func (f *fakeController) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	request := r.Method + " " + r.URL.RequestURI()
	f.requests = append(f.requests, request)
	body, _ := io.ReadAll(r.Body)
	status := http.StatusOK
	var response any
	switch request {
	case "GET /configs":
		response = map[string]any{"mode": f.mode}
	case "GET /proxies":
		proxies := map[string]any{
			"GLOBAL":     map[string]any{"type": "Selector", "now": f.global, "all": []string{"FDU-ROUTER", japan, hongKong}},
			japan:        map[string]any{"type": "Shadowsocks"},
			hongKong:     map[string]any{"type": "Shadowsocks"},
			"FDU-ROUTER": map[string]any{"type": "Socks5"},
		}
		if !f.noInternet {
			proxies["INTERNET"] = map[string]any{"type": "Selector", "now": f.current, "all": f.members}
		}
		if f.unsafeGroup {
			proxies["循环组"] = map[string]any{"type": "Selector", "now": japan, "all": []string{japan, "FDU-ROUTER"}}
		}
		response = map[string]any{"proxies": proxies}
	case "PUT /proxies/INTERNET", "PUT /proxies/GLOBAL":
		var data map[string]string
		if json.Unmarshal(body, &data) != nil || len(data) != 1 || data["name"] == "" {
			f.t.Errorf("unexpected write payload %s", body)
		}
		if strings.HasSuffix(request, "INTERNET") {
			f.written = data["name"]
			if !f.ignoreSelection {
				f.current = data["name"]
			}
		} else if !f.ignoreSelection {
			f.global = data["name"]
		}
		status = http.StatusNoContent
	default:
		f.t.Errorf("unexpected request %s", request)
		status = http.StatusNotFound
	}
	if f.listenerFront && f.global == "FDU-ROUTER" {
		f.loop = true
	}
	if f.failNextRead && r.Method == http.MethodGet {
		f.failNextRead, status, response = false, http.StatusServiceUnavailable, nil
	}
	w.WriteHeader(status)
	if response == nil {
		return
	}
	payload, _ := json.Marshal(response)
	if !f.chunked {
		_, _ = w.Write(payload)
		return
	}
	for i := 0; i < len(payload); i += 17 {
		_, _ = w.Write(payload[i:min(i+17, len(payload))])
		w.(http.Flusher).Flush()
	}
}

func TestOpeningAndRefreshingOnlyRead(t *testing.T) {
	f := newFake(t)
	p := &Picker{API: f.api()}
	state, err := p.Load()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = p.Load()
	if state.Problem != "" || state.Current != japan || !contains(state.Choices, hongKong) || !state.Imported || state.Types[hongKong] != "Shadowsocks" {
		t.Fatalf("incorrect state %+v", state)
	}
	if len(f.requests) != 4 || len(f.puts()) != 0 {
		t.Fatalf("load wrote state: %v", f.requests)
	}
}

func TestUnicodeSelectionWritesOnlyInternet(t *testing.T) {
	f := newFake(t)
	f.current = hongKong
	state, err := (&Picker{API: f.api()}).Select(japan)
	if err != nil || state.Current != japan || f.written != japan {
		t.Fatalf("unicode body corrupted: %+v %v", state, err)
	}
	if f.mode != "global" || f.global != "FDU-ROUTER" {
		t.Fatal("route changed")
	}
	if puts := f.puts(); len(puts) != 1 || puts[0] != "PUT /proxies/INTERNET" {
		t.Fatalf("unexpected writes %v", puts)
	}
}

func TestChangedModeOrGlobalBlocksSelection(t *testing.T) {
	for name, change := range map[string]func(*fakeController){
		"mode":   func(f *fakeController) { f.mode = "rule" },
		"global": func(f *fakeController) { f.global = "DIRECT" },
	} {
		f := newFake(t)
		p := &Picker{API: f.api()}
		_, _ = p.Load()
		f.with(func() { change(f) })
		if _, err := p.Select(hongKong); err == nil || f.written != "" {
			t.Errorf("%s change: accepted selection or wrote %q", name, f.written)
		}
	}
}

func TestNestedGroupReachingFduIsRejected(t *testing.T) {
	f := newFake(t)
	f.unsafeGroup = true
	f.members = []string{japan, "循环组", "FDU-ROUTER", "INTERNET"}
	p := &Picker{API: f.api()}
	if state, _ := p.Load(); contains(state.Choices, "循环组") {
		t.Fatal("unsafe group selectable")
	}
	for _, name := range []string{"循环组", "FDU-ROUTER", "INTERNET"} {
		if _, err := p.Select(name); err == nil {
			t.Errorf("accepted %s", name)
		}
	}
	if f.written != "" {
		t.Fatal("wrote unsafe target")
	}
}

func TestChunkedResponsesPreserveUTF8(t *testing.T) {
	f := newFake(t)
	f.chunked = true
	if state, err := (&Picker{API: f.api()}).Load(); err != nil || state.Current != japan {
		t.Fatalf("chunk framing corrupted unicode: %+v %v", state, err)
	}
}

func TestUnconfirmedWriteIsNotRetried(t *testing.T) {
	f := newFake(t)
	f.ignoreSelection = true
	if _, err := (&Picker{API: f.api()}).Select(hongKong); err == nil {
		t.Fatal("declared unconfirmed selection successful")
	}
	if len(f.puts()) != 1 {
		t.Fatalf("retried write %v", f.puts())
	}
}

func TestFduFirstSwitchesGlobalOnly(t *testing.T) {
	f := newFake(t)
	f.global = japan
	p := &Picker{API: f.api(), Front: true}
	if state, err := p.Load(); err != nil || state.Problem != "" {
		t.Fatalf("FDU-first not recognized: %+v %v", state, err)
	}
	if state, err := p.Select(hongKong); err != nil || state.Current != hongKong {
		t.Fatalf("did not switch GLOBAL: %v", err)
	}
	if f.current != japan || len(f.puts()) != 1 {
		t.Fatal("unexpected route change")
	}
}

func TestModesUseOrderedSelectorChanges(t *testing.T) {
	f := newFake(t)
	front := false
	s := &Switcher{API: f.api(), Configure: func(value bool) error { front = value; return nil }}
	if err := s.Switch(true, japan); err != nil {
		t.Fatal(err)
	}
	if !front || f.global != japan || f.current != japan {
		t.Fatal("FDU-first route incorrect")
	}
	if puts := f.puts(); strings.Join(puts, ",") != "PUT /proxies/GLOBAL" {
		t.Fatalf("unsafe forward order %v", puts)
	}
	f.with(func() { f.requests = nil })
	if err := s.Switch(false, hongKong); err != nil {
		t.Fatal(err)
	}
	if front || f.global != "FDU-ROUTER" || f.current != hongKong {
		t.Fatal("Clash-first route incorrect")
	}
	if puts := f.puts(); strings.Join(puts, ",") != "PUT /proxies/GLOBAL,PUT /proxies/INTERNET,PUT /proxies/GLOBAL" {
		t.Fatalf("unsafe reverse order %v", puts)
	}
}

func TestUnconfirmedReloadsRestoreListenerBeforeGlobal(t *testing.T) {
	for scenario := 0; scenario < 3; scenario++ {
		f := newFake(t)
		reloads := 0
		s := &Switcher{API: f.api(), Configure: func(front bool) error {
			reloads++
			var global string
			f.with(func() { global = f.global })
			if global != japan {
				t.Errorf("scenario %d: reload started with unsafe GLOBAL %q", scenario, global)
			}
			if reloads == 2 && scenario == 2 {
				return errors.New("restore unconfirmed")
			}
			f.with(func() { f.listenerFront = front })
			if reloads == 1 {
				if scenario == 1 {
					f.with(func() { f.failNextRead = true })
				} else {
					return errors.New("reload unconfirmed")
				}
			}
			return nil
		}}
		if err := s.Switch(true, japan); err == nil {
			t.Fatalf("scenario %d: accepted unconfirmed reload", scenario)
		}
		if reloads != 2 || f.loop {
			t.Fatalf("scenario %d: skipped safe listener restoration (reloads %d, loop %v)", scenario, reloads, f.loop)
		}
		want := "FDU-ROUTER"
		if scenario == 2 {
			want = japan
		}
		if f.global != want {
			t.Fatalf("scenario %d: GLOBAL %q after restoration", scenario, f.global)
		}
	}
}

func TestRenamedFduEndpointsAreExcluded(t *testing.T) {
	f := newFake(t)
	unsafe := map[string]bool{hongKong: true}
	if state, _ := (&Picker{API: f.api(), Unsafe: unsafe}).Load(); contains(state.Choices, hongKong) {
		t.Fatal("offered FDU alias")
	}
	s := &Switcher{API: f.api(), Configure: func(bool) error { return nil }, Unsafe: unsafe}
	if err := s.Switch(true, hongKong); err == nil {
		t.Fatal("selected FDU alias")
	}
	if len(f.puts()) != 0 {
		t.Fatal("wrote unsafe alias")
	}
}

func TestMissingImportRefusesModeChanges(t *testing.T) {
	f := newFake(t)
	f.noInternet = true
	if err := (&Switcher{API: f.api(), Configure: func(bool) error { return nil }}).Switch(true, japan); err == nil {
		t.Fatal("accepted incomplete import")
	}
	if len(f.puts()) != 0 {
		t.Fatal("wrote an incomplete route")
	}
	if state, err := (&Picker{API: f.api()}).Load(); err != nil || state.Imported {
		t.Fatalf("missing import not reported: %+v %v", state, err)
	}
}

func TestEnableRouterPreservesExitAndRejectsUnsafeModes(t *testing.T) {
	for _, scenario := range []string{"ready", "fdu-first", "unsafe-exit", "rule-mode"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFake(t)
			f.global, f.current = japan, hongKong
			s := &Switcher{API: f.api(), Front: scenario == "fdu-first"}
			if scenario == "unsafe-exit" {
				f.current = "FDU-ROUTER"
			}
			if scenario == "rule-mode" {
				f.mode = "rule"
			}
			err := s.EnableRouter()
			if scenario != "ready" {
				if err == nil || len(f.puts()) != 0 {
					t.Fatalf("unsafe activation: %v, writes %v", err, f.puts())
				}
				return
			}
			if err != nil || f.global != "FDU-ROUTER" || f.current != hongKong {
				t.Fatalf("exit changed or activation failed: %v", err)
			}
			if err := s.EnableRouter(); err != nil {
				t.Fatal(err)
			}
			if writes := f.puts(); len(writes) != 1 || writes[0] != "PUT /proxies/GLOBAL" {
				t.Fatalf("unexpected writes: %v", writes)
			}
		})
	}
}

func TestEnableRouterRefusesUnreadyFduBeforeControllerAccess(t *testing.T) {
	f := newFake(t)
	c := &Connection{API: f.api()}
	if err := c.EnableRouter(Plan{Mode: "clash-first", NeedsFduStart: true}); err == nil {
		t.Fatal("enabled before FDU was ready")
	}
	if len(f.requests) != 0 {
		t.Fatalf("unready activation reached controller: %v", f.requests)
	}
}

func TestAllowlistRejectsOtherRequests(t *testing.T) {
	f := newFake(t)
	for _, c := range [][2]string{{"DELETE", "/proxies/GLOBAL"}, {"PUT", "/proxies/FDU-ROUTER"}, {"PATCH", "/configs"}, {"GET", "/logs"}} {
		if err := f.api().Do(c[0], c[1], nil, nil); err == nil {
			t.Errorf("allowed %v", c)
		}
	}
	if len(f.requests) != 0 {
		t.Fatalf("disallowed request reached controller: %v", f.requests)
	}
}
