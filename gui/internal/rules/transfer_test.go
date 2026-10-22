package rules

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTransferRoundtripPreservesTypedDisabledRules(t *testing.T) {
	d := Empty()
	d.Domains = []string{"LAB.FUDAN.EDU.CN."}
	d.DomainSuffixes = []string{"lab.fudan.edu.cn"}
	d.IPCIDRs = []string{"10.2.3.4/24"}
	d.Disabled.Domains = []string{"lab.fudan.edu.cn"}
	d.Disabled.IPCIDRs = []string{"10.2.3.0/24"}
	path := filepath.Join(t.TempDir(), "export.json")
	if err := ExportFile(path, d, nil); err != nil {
		t.Fatal(err)
	}
	got, err := ImportFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := Document{[]string{"lab.fudan.edu.cn"}, []string{"lab.fudan.edu.cn"}, []string{"10.2.3.0/24"}, Disabled{[]string{"lab.fudan.edu.cn"}, []string{}, []string{"10.2.3.0/24"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("roundtrip changed rules: %+v", got)
	}
	before, _ := os.ReadFile(path)
	if _, err := ImportFile(path); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("import preview wrote to its source")
	}
}

func TestImportRejectsInvalidOrOversizedDocumentsWithoutWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "import.json")
	for _, content := range []string{"{", strings.Replace(seed, `"version":1`, `"version":3`, 1), seed + "{}", strings.TrimSuffix(seed, "}") + `,"secret":"private"}`, strings.Repeat(" ", limit+1)} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ImportFile(path); err == nil {
			t.Error("accepted invalid import")
		}
		if after, _ := os.ReadFile(path); string(after) != content {
			t.Fatal("invalid import changed its source")
		}
	}
}

func TestExportRefusesManagedPathsAndInvalidDrafts(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, "managed.json")
	if err := os.WriteFile(managed, []byte("private-state"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{managed, strings.ToUpper(managed)} {
		if err := ExportFile(path, Empty(), []string{managed}); err == nil {
			t.Fatal("export overwrote managed state")
		}
	}
	alias := filepath.Join(root, "alias.json")
	if err := os.Link(managed, alias); err != nil {
		t.Fatal(err)
	}
	if err := ExportFile(alias, Empty(), []string{managed}); err == nil {
		t.Fatal("export accepted a managed file alias")
	}
	d := Empty()
	d.Disabled.Domains = []string{"missing.cn"}
	if err := ExportFile(managed, d, nil); err == nil {
		t.Fatal("export accepted invalid draft")
	}
	if after, _ := os.ReadFile(managed); string(after) != "private-state" {
		t.Fatal("refused export changed managed state")
	}
}
