package routing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mythologyli/zju-connect/configs"
)

const initialRules = `{"version":1,"domains":[],"domain_suffixes":[],"ip_cidrs":["10.20.26.253/32"]}`

func TestRuleEntrySwitchesRetainContentAndFilterEachType(t *testing.T) {
	data := `{"version":2,"domains":["Exact.Fudan.edu.cn"],"domain_suffixes":["lab.fudan.edu.cn"],"ip_cidrs":["10.20.26.253"],"disabled":{"domains":["exact.fudan.edu.cn"],"domain_suffixes":["lab.fudan.edu.cn"],"ip_cidrs":["10.20.26.253/32"]}}`
	rules, err := ParseRules([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(rules.Domains) != 1 || len(rules.DomainSuffixes) != 1 || len(rules.IPCIDRs) != 1 {
		t.Fatal("disabled content was deleted")
	}
	r, _ := New(configs.RoutingConfig{UpstreamProxy: "socks5://127.0.0.1:1"})
	defer r.Close()
	if err := r.ReplaceRules(rules); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"exact.fudan.edu.cn", "a.lab.fudan.edu.cn", "10.20.26.253"} {
		if r.policy.Load().match(host) {
			t.Fatalf("disabled rule matched %s", host)
		}
	}
	rules.Disabled = &DisabledRules{Domains: []string{}, DomainSuffixes: []string{}, IPCIDRs: []string{}}
	if err := r.ReplaceRules(rules); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"exact.fudan.edu.cn", "a.lab.fudan.edu.cn", "10.20.26.253"} {
		if !r.policy.Load().match(host) {
			t.Fatalf("re-enabled rule did not match %s", host)
		}
	}
	legacy, _ := ParseRules([]byte(initialRules))
	if err := r.ReplaceRules(legacy); err != nil || !r.policy.Load().match("10.20.26.253") {
		t.Fatal("legacy rules not enabled")
	}
}

func TestDisabledEntriesAreTypedAndDoNotOverrideOtherMatchingRules(t *testing.T) {
	rules, err := ParseRules([]byte(`{"version":2,"domains":["lab.fudan.edu.cn"],"domain_suffixes":["lab.fudan.edu.cn"],"ip_cidrs":["10.20.26.253/32","10.0.0.0/8"],"disabled":{"domains":["lab.fudan.edu.cn"],"domain_suffixes":[],"ip_cidrs":["10.20.26.253/32"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	r, _ := New(configs.RoutingConfig{UpstreamProxy: "socks5://127.0.0.1:1"})
	defer r.Close()
	if err := r.ReplaceRules(rules); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"lab.fudan.edu.cn", "a.lab.fudan.edu.cn", "10.20.26.253"} {
		if !r.policy.Load().match(host) {
			t.Fatalf("disabled entry overrode another enabled rule for %s", host)
		}
	}
}

func TestInvalidEntrySwitchMetadataIsRejected(t *testing.T) {
	for _, data := range []string{
		`{"version":2,"domains":[],"domain_suffixes":[],"ip_cidrs":[]}`,
		`{"version":2,"domains":[],"domain_suffixes":[],"ip_cidrs":[],"disabled":{}}`,
		`{"version":2,"domains":[],"domain_suffixes":[],"ip_cidrs":[],"disabled":{"domains":["missing.invalid"],"domain_suffixes":[],"ip_cidrs":[]}}`,
		`{"version":1,"domains":[],"domain_suffixes":[],"ip_cidrs":[],"disabled":{"domains":[],"domain_suffixes":[],"ip_cidrs":[]}}`,
	} {
		if _, err := ParseRules([]byte(data)); err == nil {
			t.Fatalf("accepted invalid switch metadata: %s", data)
		}
	}
}

func TestRuleDocumentValidationAndCanonicalization(t *testing.T) {
	rules, err := ParseRules([]byte(`{"version":1,"domains":[" Exact.Fudan.edu.cn. ","exact.fudan.edu.cn"],"domain_suffixes":["Lab.Fudan.edu.cn"],"ip_cidrs":["10.20.26.253/24","10.20.26.0/24","10.2.3.4"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(rules.Domains) != "[exact.fudan.edu.cn]" || fmt.Sprint(rules.DomainSuffixes) != "[lab.fudan.edu.cn]" || fmt.Sprint(rules.IPCIDRs) != "[10.20.26.0/24 10.2.3.4/32]" {
		t.Fatalf("unexpected normalized document: %+v", rules)
	}
	for _, data := range []string{
		`{}`, `{"version":1}`, `{"version":1,"domains":null,"domain_suffixes":[],"ip_cidrs":[]}`,
		`{"version":2,"domains":[],"domain_suffixes":[],"ip_cidrs":[]}`,
		`{"version":1,"domains":[],"domain_suffixes":[],"ip_cidrs":[],"unknown":true}`,
		`{"version":1,"domains":["https://fudan.edu.cn/a"],"domain_suffixes":[],"ip_cidrs":[]}`,
		`{"version":1,"domains":[],"domain_suffixes":["*.fudan.edu.cn"],"ip_cidrs":[]}`,
		`{"version":1,"domains":[],"domain_suffixes":[],"ip_cidrs":["::1/128"]}`,
		`{"version":1,"domains":[],"domain_suffixes":[],"ip_cidrs":["10.1.2.3/33"]}`,
		initialRules + `{}`,
	} {
		if _, err := ParseRules([]byte(data)); err == nil {
			t.Errorf("accepted invalid document %s", data)
		}
	}
	if _, err := ParseRules(make([]byte, 256*1024+1)); err == nil {
		t.Fatal("accepted oversized document")
	}
}

func writeTestRules(t *testing.T, path, content string) {
	t.Helper()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

func waitRuleStatus(t *testing.T, path string, predicate func(RuleFileStatus) bool) RuleFileStatus {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path + ".status.json")
		var status RuleFileStatus
		if err == nil && json.Unmarshal(data, &status) == nil && predicate(status) {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("status acknowledgement timed out")
	return RuleFileStatus{}
}

func TestRulesWatcherLoadsAcknowledgesAndKeepsLastValidRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	writeTestRules(t, path, initialRules)
	r, _ := New(configs.RoutingConfig{UpstreamProxy: "socks5://127.0.0.1:7902"})
	defer r.Close()
	watcher, err := watchRulesFile(context.Background(), path, r, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	digest := sha256.Sum256([]byte(initialRules))
	status := waitRuleStatus(t, path, func(s RuleFileStatus) bool { return s.Running && s.LoadedSHA256 == hex.EncodeToString(digest[:]) })
	if status.PID != os.Getpid() || status.Version != 1 || !r.policy.Load().match("10.20.26.253") {
		t.Fatal("wrong initial status/policy")
	}
	updated := `{"version":1,"domains":[],"domain_suffixes":["lab.fudan.edu.cn"],"ip_cidrs":[]}`
	writeTestRules(t, path, updated)
	digest = sha256.Sum256([]byte(updated))
	waitRuleStatus(t, path, func(s RuleFileStatus) bool { return s.LoadedSHA256 == hex.EncodeToString(digest[:]) })
	if !r.policy.Load().match("a.lab.fudan.edu.cn") || r.policy.Load().match("10.20.26.253") {
		t.Fatal("updated policy not in use")
	}
	writeTestRules(t, path, `{"version":1}`)
	waitRuleStatus(t, path, func(s RuleFileStatus) bool { return s.Error != "" })
	if !r.policy.Load().match("a.lab.fudan.edu.cn") {
		t.Fatal("invalid file discarded good rules")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	waitRuleStatus(t, path, func(s RuleFileStatus) bool { return s.Error != "" && s.LoadedSHA256 == hex.EncodeToString(digest[:]) })
	if !r.policy.Load().match("a.lab.fudan.edu.cn") {
		t.Fatal("removed file discarded good rules")
	}
	writeTestRules(t, path, initialRules)
	waitRuleStatus(t, path, func(s RuleFileStatus) bool { return s.Error == "" && r.policy.Load().match("10.20.26.253") })
	if err := watcher.Close(); err != nil {
		t.Fatal(err)
	}
	waitRuleStatus(t, path, func(s RuleFileStatus) bool { return !s.Running })
}

func TestRulesWatcherRejectsInvalidStartup(t *testing.T) {
	r, _ := New(configs.RoutingConfig{UpstreamProxy: "socks5://127.0.0.1:7902"})
	defer r.Close()
	path := filepath.Join(t.TempDir(), "missing.json")
	if _, err := WatchRulesFile(context.Background(), path, r); err == nil {
		t.Fatal("missing initial file accepted")
	}
	writeTestRules(t, path, `{"version":1}`)
	if _, err := WatchRulesFile(context.Background(), path, r); err == nil {
		t.Fatal("invalid initial file accepted")
	}
}
