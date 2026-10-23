package rules

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
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
	paths := []string{managed}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		paths = append(paths, strings.ToUpper(managed))
	}
	for _, path := range paths {
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
	d = Empty()
	for i := 0; i < 12000; i++ {
		d.Domains = append(d.Domains, fmt.Sprintf("resource-%05d.fudan.edu.cn", i))
	}
	if err := ExportFile(managed, d, nil); err == nil {
		t.Fatal("export accepted oversized draft")
	}
	if after, _ := os.ReadFile(managed); string(after) != "private-state" {
		t.Fatal("refused export changed managed state")
	}
}

func TestExportProtectsMissingFilesThroughDirectoryLinks(t *testing.T) {
	root := t.TempDir()
	real, link := filepath.Join(root, "real"), filepath.Join(root, "link")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if output, err := exec.Command("cmd", "/c", "mklink", "/J", link, real).CombinedOutput(); err != nil {
			t.Fatalf("create junction: %v: %s", err, output)
		}
	} else if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"campus-session.json", filepath.Join("new", "campus-session.json")} {
		managed := filepath.Join(real, name)
		if err := ExportFile(filepath.Join(link, name), Empty(), nil, real); err == nil {
			t.Errorf("accepted a destination inside an aliased managed directory: %s", name)
		}
		if err := ExportFile(filepath.Join(link, name), Empty(), []string{managed}); err == nil {
			t.Errorf("accepted missing managed file through a directory link: %s", name)
		}
		if _, err := os.Stat(managed); !os.IsNotExist(err) {
			t.Fatalf("export created managed state: %v", err)
		}
	}
}

func TestExportIgnoresUnresolvableUnrelatedProtectedPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "export.json")
	if err := ExportFile(path, Empty(), []string{filepath.Join(t.TempDir(), "bad\x00name", "cert.pem")}); err != nil {
		t.Fatalf("unrelated invalid configuration path blocked export: %v", err)
	}
}

func TestExportAllowsDistinctCaseSensitiveNames(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires a case-sensitive filesystem")
	}
	root := t.TempDir()
	managed := filepath.Join(root, "client.toml")
	if err := os.WriteFile(managed, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ExportFile(filepath.Join(root, "Client.toml"), Empty(), []string{managed}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(managed); string(data) != "private" {
		t.Fatal("changed distinct managed file")
	}
}
