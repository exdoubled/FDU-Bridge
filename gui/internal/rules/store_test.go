package rules

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const seed = `{"version":1,"domains":[],"domain_suffixes":[],"ip_cidrs":["10.20.26.253/32"]}`

func fixture(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestNormalizationPreservesDomainBoundariesAndMasksIPv4(t *testing.T) {
	d := Empty()
	d.Domains = []string{" Exact.Fudan.edu.cn. ", "exact.fudan.edu.cn"}
	d.DomainSuffixes = []string{"Lab.Fudan.edu.cn"}
	d.IPCIDRs = []string{"10.20.26.253/24", "10.20.26.0/24", "10.2.3.4"}
	got, err := Normalize(d)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Domains, []string{"exact.fudan.edu.cn"}) || got.DomainSuffixes[0] != "lab.fudan.edu.cn" {
		t.Fatalf("domain normalization changed meaning: %+v", got)
	}
	if !reflect.DeepEqual(got.IPCIDRs, []string{"10.20.26.0/24", "10.2.3.4/32"}) {
		t.Fatalf("IP mask or duplicates wrong: %v", got.IPCIDRs)
	}
}

func TestInvalidEntriesAreRejected(t *testing.T) {
	for _, value := range []string{"*.fudan.edu.cn", "a..fudan.edu.cn", "中文.edu.cn", "10.1.2.3"} {
		if _, err := Entry(Domain, value); err == nil {
			t.Errorf("accepted domain %q", value)
		}
		d := Empty()
		d.Domains = []string{value}
		if _, err := Normalize(d); err == nil {
			t.Errorf("normalized domain %q", value)
		}
	}
	for _, value := range []string{"::1", "10.1.2.3/33", "10.1.2.256", "127.1", "10.1.2.3/-1"} {
		if _, err := Entry(CIDR, value); err == nil {
			t.Errorf("accepted IP %q", value)
		}
	}
	if got, err := Entry(CIDR, " 10.20.26.253 "); err != nil || got != "10.20.26.253/32" {
		t.Fatalf("single IPv4 not canonical: %q %v", got, err)
	}
	for _, entry := range []struct{ kind Kind; input, want string }{
		{Domain, "https://Fudan.edu.cn:443/a?q=b", "fudan.edu.cn"},
		{CIDR, "http://10.20.26.253/page/mg/login", "10.20.26.253/32"},
	} {
		if got, err := Entry(entry.kind, entry.input); err != nil || got != entry.want {
			t.Errorf("URL entry: got %q, %v", got, err)
		}
	}
}

func TestMalformedFilesAreRejected(t *testing.T) {
	s, path := fixture(t)
	for _, text := range []string{"{}", `{"version":1}`, `{"version":1,"domains":null,"domain_suffixes":[],"ip_cidrs":[]}`,
		strings.Replace(seed, `"version":1`, `"version":2`, 1), strings.TrimSuffix(seed, "}") + `,"x":true}`, seed + "{}",
		strings.Replace(seed, `"domains":[]`, `"domains":[123]`, 1),
		strings.Replace(seed, `"domains":[]`, `"domains":[null]`, 1),
		strings.Replace(seed, `"domain_suffixes":[]`, `"domain_suffixes":"lab.fudan.edu.cn"`, 1),
		strings.Replace(seed, `"10.20.26.253/32"`, `{}`, 1)} {
		_ = os.WriteFile(path, []byte(text), 0o600)
		if _, err := s.Load(); err == nil {
			t.Errorf("accepted %s", text)
		}
	}
	_ = os.WriteFile(path, make([]byte, limit+1), 0o600)
	if _, err := s.Load(); err == nil {
		t.Error("accepted oversized file")
	}
}

func TestChangedOrDeletedFilesAreNotOverwritten(t *testing.T) {
	s, path := fixture(t)
	before, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(path, []byte(seed+" "), 0o600)
	if _, err := s.Save(before.Document, before.Hash); err == nil {
		t.Fatal("overwrote external change")
	}
	if b, _ := os.ReadFile(path); !strings.HasSuffix(string(b), " ") {
		t.Fatal("external change lost")
	}
	_ = os.Remove(path)
	if _, err := s.Save(before.Document, before.Hash); err == nil {
		t.Fatal("resurrected deleted file")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("deleted file resurrected")
	}
}

func TestMissingFileIsCreatedOnlyWhenExpected(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "new", "rules.json"))
	if err != nil {
		t.Fatal(err)
	}
	snap, err := s.Load()
	if err != nil || !snap.Missing || snap.Hash != "" {
		t.Fatalf("missing file not reported: %+v %v", snap, err)
	}
	d := Empty()
	d.Domains = []string{"a.fudan.edu.cn"}
	saved, err := s.Save(d, "")
	if err != nil || saved.Hash == "" {
		t.Fatalf("save new file: %v", err)
	}
	if _, err := s.Save(d, ""); err == nil {
		t.Fatal("second create overwrote existing file")
	}
}

func writeStatus(t *testing.T, path, hash string, updated time.Time, running bool, failure string, pid int) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"version": 1, "pid": pid, "running": running, "updated_at": updated.Format(time.RFC3339Nano), "loaded_sha256": hash, "error": failure})
	if err := os.WriteFile(path+".status.json", b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStaleOrMismatchedAcknowledgementsDoNotClaimApplied(t *testing.T) {
	s, path := fixture(t)
	snap, _ := s.Load()
	pid := os.Getpid()
	if s.ApplyStatus(snap.Hash).Applied {
		t.Fatal("offline state claims applied")
	}
	writeStatus(t, path, snap.Hash, time.Now().UTC(), true, "", pid)
	if !s.ApplyStatus(snap.Hash).Applied {
		t.Fatal("fresh acknowledgement ignored")
	}
	for name, apply := range map[string]func(){
		"stale":    func() { writeStatus(t, path, snap.Hash, time.Now().Add(-time.Minute), true, "", pid) },
		"stopped":  func() { writeStatus(t, path, snap.Hash, time.Now(), false, "", pid) },
		"rejected": func() { writeStatus(t, path, snap.Hash, time.Now(), true, "invalid JSON", pid) },
		"revision": func() { writeStatus(t, path, "other", time.Now(), true, "", pid) },
		"exited":   func() { writeStatus(t, path, snap.Hash, time.Now(), true, "", math.MaxInt32) },
		"future":   func() { writeStatus(t, path, snap.Hash, time.Now().Add(time.Hour), true, "", pid) },
	} {
		apply()
		if s.ApplyStatus(snap.Hash).Applied {
			t.Errorf("%s acknowledgement accepted", name)
		}
	}
}

func TestDisabledRulesRoundtripWithoutCrossingTypes(t *testing.T) {
	s, _ := fixture(t)
	before, _ := s.Load()
	d := Empty()
	d.Domains = []string{"lab.fudan.edu.cn"}
	d.DomainSuffixes = []string{"lab.fudan.edu.cn"}
	d.IPCIDRs = []string{"10.0.0.0/8", "10.20.26.253"}
	d.Disabled.Domains = []string{"LAB.FUDAN.EDU.CN"}
	d.Disabled.IPCIDRs = []string{"10.0.0.0/8"}
	if _, err := s.Save(d, before.Hash); err != nil {
		t.Fatal(err)
	}
	saved, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	got := saved.Document
	if len(got.Domains) != 1 || len(got.IPCIDRs) != 2 {
		t.Fatalf("disabled content lost: %+v", got)
	}
	if got.Disabled.Domains[0] != "lab.fudan.edu.cn" || len(got.Disabled.DomainSuffixes) != 0 || got.Disabled.IPCIDRs[0] != "10.0.0.0/8" {
		t.Fatalf("typed switches changed meaning: %+v", got.Disabled)
	}
	got.Disabled.IPCIDRs = []string{}
	if _, err := s.Save(got, saved.Hash); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.Load(); len(again.Document.Disabled.IPCIDRs) != 0 {
		t.Fatal("re-enable did not clear disabled flag")
	}
}

func TestDisabledEntryMustExist(t *testing.T) {
	d := Empty()
	d.Disabled.Domains = []string{"missing.fudan.edu.cn"}
	if _, err := Normalize(d); err == nil {
		t.Fatal("accepted orphan disabled entry")
	}
}
