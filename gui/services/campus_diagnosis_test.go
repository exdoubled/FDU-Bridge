package services

import (
	"encoding/json"
	"errors"
	"github.com/mythologyli/zju-connect/gui/internal/clash"
	"github.com/mythologyli/zju-connect/gui/internal/core"
	"github.com/mythologyli/zju-connect/gui/internal/netcheck"
	"github.com/mythologyli/zju-connect/gui/internal/rules"
	"github.com/mythologyli/zju-connect/gui/internal/settings"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLatestDiagnosisDoesNotShareMutableResults(t *testing.T) {
	s := &NetworkService{}
	if _, ok := s.latestDiagnosis(); ok {
		t.Fatal("unexpected initial diagnosis")
	}
	input := CampusDiagnosis{Target: "https://example.edu/", Suggestions: []string{"original"}, Probe: &ProbeReport{Result: netcheck.Result{Answers: []string{"10.0.0.1"}, Checks: []netcheck.Check{{Kind: "http", Detail: "original"}}}}}
	s.saveDiagnosis(input)
	input.Suggestions[0] = "input mutation"
	input.Probe.Result.Answers[0] = "input mutation"
	first, ok := s.latestDiagnosis()
	if !ok || first.Suggestions[0] != "original" || first.Probe.Result.Answers[0] != "10.0.0.1" {
		t.Fatalf("input aliases snapshot: %+v", first)
	}
	first.Suggestions[0] = "reader mutation"
	first.Probe.Result.Checks[0].Detail = "reader mutation"
	second, _ := s.latestDiagnosis()
	if second.Suggestions[0] != "original" || second.Probe.Result.Checks[0].Detail != "original" {
		t.Fatalf("reader aliases snapshot: %+v", second)
	}
}

func TestDiagnoseCampusDNSLabelReflectsActualLookup(t *testing.T) {
	for _, host := range []string{"10.20.1.2", "2001:db8::1", "library.fudan.edu.cn"} {
		t.Run(host, func(t *testing.T) {
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
			defer proxy.Close()
			proxyURL, _ := url.Parse(proxy.URL)
			b := &Backend{Dir: t.TempDir(), runtime: &core.Runtime{}}
			lookups := 0
			b.conn = &clash.Connection{API: networkTestAPI(func(_, path string, _, out any) error {
				if path == "/configs" {
					return json.Unmarshal([]byte(`{"mixed-port":`+proxyURL.Port()+`}`), out)
				}
				lookups++
				query, _ := url.Parse(path)
				if net.ParseIP(query.Query().Get("name")) != nil {
					return json.Unmarshal([]byte(`{"Status":3}`), out)
				}
				return json.Unmarshal([]byte(`{"Status":0,"Answer":[{"type":1,"data":"203.0.113.8"}]}`), out)
			})}
			authority := host
			ipLiteral := net.ParseIP(host) != nil
			if strings.Contains(host, ":") {
				authority = "[" + host + "]"
			}
			got, err := NewNetworkService(b).DiagnoseCampus("http://" + authority + "/campus")
			if err != nil || got.Probe == nil || got.Probe.Result.HTTPStatus != 204 {
				t.Fatalf("probe failed: %+v / %v", got, err)
			}
			dnsFound := false
			for _, check := range got.Probe.Result.Checks {
				if check.Kind != "dns" {
					continue
				}
				dnsFound = true
				if check.Status != "ok" || strings.Contains(check.Detail, "Clash DNS") == ipLiteral {
					t.Fatalf("misleading DNS label: %+v", check)
				}
				if ipLiteral && (lookups != 0 || !strings.Contains(check.Detail, "无需解析")) {
					t.Fatalf("IP used Clash DNS: %+v; lookups=%d", check, lookups)
				}
				if !ipLiteral && lookups != 2 {
					t.Fatalf("domain skipped DNS: lookups=%d", lookups)
				}
			}
			if !dnsFound {
				t.Fatal("missing DNS evidence")
			}
		})
	}
}

func TestDiagnoseCampusUsesSavedRulesAndSelectedClashEntry(t *testing.T) {
	requests := 0
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.String() != "http://library.fudan.edu.cn/catalog" {
			t.Errorf("unexpected probe target %s", r.URL)
		}
		w.WriteHeader(403)
	}))
	defer proxy.Close()
	u, _ := url.Parse(proxy.URL)
	port, _ := strconv.Atoi(u.Port())
	b := &Backend{Dir: t.TempDir(), runtime: &core.Runtime{}, state: "disconnected", authRequired: true, coverage: "系统代理未启用"}
	b.config.Integration.Mode = "manual"
	b.conn = &clash.Connection{API: networkTestAPI(func(_, path string, _, out any) error {
		if path == "/configs" {
			return json.Unmarshal([]byte(`{"mixed-port":`+strconv.Itoa(port)+`}`), out)
		}
		return json.Unmarshal([]byte(`{"Status":0,"Answer":[{"type":1,"data":"10.20.1.2"}]}`), out)
	})}
	doc := rules.Empty()
	doc.Domains = []string{"library.fudan.edu.cn"}
	doc.Disabled.Domains = []string{"library.fudan.edu.cn"}
	store, _ := rules.New(filepath.Join(b.Dir, "routing-rules.json"))
	if _, err := store.Save(doc, ""); err != nil {
		t.Fatal(err)
	}
	// An unsaved editor draft must not change diagnosis of the persisted document.
	b.view = &settings.View{Values: map[string]any{"routing": map[string]any{"domain_suffixes": []string{"fudan.edu.cn"}}}, Dirty: true}
	// Importing settings can also redirect the editor's rulesPath to a draft.
	b.rulesPath = filepath.Join(b.Dir, "draft-rules.json")
	draftStore, _ := rules.New(b.rulesPath)
	if _, err := draftStore.Save(rules.Empty(), ""); err != nil {
		t.Fatal(err)
	}
	s := NewNetworkService(b)
	got, err := s.DiagnoseCampus("http://library.fudan.edu.cn/catalog?token=private#secret")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Rule.Matched || got.Rule.Enabled || got.Rule.Value != "library.fudan.edu.cn" || got.Rule.Applied {
		t.Fatalf("wrong saved rule: %+v", got.Rule)
	}
	if got.Target != "http://library.fudan.edu.cn/catalog" || got.Integration != "manual" || !got.AuthRequired || got.Coverage != b.coverage || got.Probe == nil || got.Probe.Via != "Clash 当前配置" || got.Probe.Result.HTTPStatus != 403 || requests != 1 {
		t.Fatalf("wrong evidence: %+v requests=%d", got, requests)
	}
	if len(got.Suggestions) == 0 || strings.Contains(got.ExpectedPath, "已通过") {
		t.Fatalf("missing guidance or misleading path: %+v", got)
	}
	previous, ok := s.latestDiagnosis()
	if !ok || previous.Target != got.Target {
		t.Fatal("completed diagnosis not retained")
	}
	if _, err = s.DiagnoseCampus("https://user:secret@library.fudan.edu.cn/"); err == nil {
		t.Fatal("credentials accepted")
	}
	previous, _ = s.latestDiagnosis()
	if previous.CheckedAt != got.CheckedAt || requests != 1 {
		t.Fatal("invalid input replaced snapshot or started a request")
	}
}

func TestDiagnoseCampusDoesNotFallBackWhenClashUnavailable(t *testing.T) {
	b := &Backend{Dir: t.TempDir(), runtime: &core.Runtime{}, saved: map[string]any{}, problem: "Clash 接入待下次启用时恢复"}
	b.conn = &clash.Connection{API: networkTestAPI(func(_, _ string, _, _ any) error { return errors.New("selected controller unavailable") })}
	s := NewNetworkService(b)
	got, err := s.DiagnoseCampus("http://127.0.0.1:1/")
	if err != nil || got.Probe != nil || got.Problem == "" || len(got.Suggestions) == 0 {
		t.Fatalf("silently changed route: %+v / %v", got, err)
	}
	if !strings.Contains(got.Problem, b.problem) || !strings.Contains(got.Problem, "当前代理检测入口不可用") {
		t.Fatalf("connection or probe problem was discarded: %s", got.Problem)
	}
}

func TestCampusSuggestionsOnlyChecksCoverageAfterHTTPFailure(t *testing.T) {
	for _, status := range []string{"ok", "auth", "error", "not-probed"} {
		t.Run(status, func(t *testing.T) {
			d := CampusDiagnosis{Integration: "auto", Connected: true, Coverage: "全部经 Clash TUN 接管的应用", Rule: CampusRuleDiagnosis{Known: true, Matched: true, Enabled: true, Applied: true}}
			if status != "not-probed" {
				d.Probe = &ProbeReport{Result: netcheck.Result{Checks: []netcheck.Check{{Kind: "http", Status: status}}}}
			}
			coverageAdvice := false
			for _, suggestion := range campusSuggestions(d) {
				coverageAdvice = coverageAdvice || strings.Contains(suggestion, "TUN 或系统代理")
			}
			if coverageAdvice != (status == "error") {
				t.Fatalf("unexpected coverage advice for HTTP %s: %v", status, campusSuggestions(d))
			}
		})
	}
}

func TestDiagnoseCampusMissingRulesUsesSavedConfigNotDraft(t *testing.T) {
	b := &Backend{Dir: t.TempDir(), runtime: &core.Runtime{}, saved: map[string]any{"routing": map[string]any{"domains": []string{"library.fudan.edu.cn"}}}}
	b.config.Integration.Mode = "proxy"
	b.view = &settings.View{Values: map[string]any{"routing": map[string]any{"domains": []string{"other.edu.cn"}}}}
	s := NewNetworkService(b)
	got, err := s.DiagnoseCampus("library.fudan.edu.cn")
	if err != nil || !got.Rule.Matched || !got.Rule.Enabled || got.Probe != nil || got.Problem == "" {
		t.Fatalf("saved fallback: %+v / %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(b.Dir, "routing-rules.json")); !os.IsNotExist(err) {
		t.Fatal("read-only diagnosis wrote rules")
	}
}

func TestCampusProbeOptionsRejectsChangedIntegrationPath(t *testing.T) {
	b := &Backend{runtime: &core.Runtime{}}
	b.conn = &clash.Connection{API: networkTestAPI(func(_, _ string, _, out any) error {
		return json.Unmarshal([]byte(`{"mixed-port":7890}`), out)
	})}
	// The caller captured proxy mode, but settings switched to Clash before
	// entry resolution. A different entry must never be accepted as evidence.
	opts, _, err := NewNetworkService(b).campusProbeOptions("proxy", true)
	if err == nil || opts.ProxyURL != "" {
		t.Fatalf("accepted a changed path: %+v / %v", opts, err)
	}
}

func TestDiagnoseCampusCannotInferRouteFromUnreadableRules(t *testing.T) {
	b := &Backend{Dir: t.TempDir(), runtime: &core.Runtime{}}
	b.conn = &clash.Connection{API: networkTestAPI(func(_, _ string, _, _ any) error { return errors.New("unavailable") })}
	if err := os.WriteFile(filepath.Join(b.Dir, "routing-rules.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := NewNetworkService(b).DiagnoseCampus("library.fudan.edu.cn")
	if err != nil || got.Rule.Known || !strings.Contains(got.Rule.Message, "无法读取") || !strings.Contains(got.ExpectedPath, "无法判断") {
		t.Fatalf("unknown rules presented as valid: %+v / %v", got, err)
	}
	for _, suggestion := range got.Suggestions {
		if strings.Contains(suggestion, "添加") {
			t.Fatalf("unknown rules prompted adding a rule: %s", suggestion)
		}
	}
}
