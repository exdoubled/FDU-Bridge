package fsx

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func directoryLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err == nil {
		return
	} else if runtime.GOOS != "windows" {
		t.Skipf("directory links unavailable: %v", err)
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "New-Item -ItemType Junction -Path $env:FDU_TEST_LINK -Target $env:FDU_TEST_TARGET -ErrorAction Stop | Out-Null")
	cmd.Env = append(os.Environ(), "FDU_TEST_LINK="+link, "FDU_TEST_TARGET="+target)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("junction unavailable: %v: %s", err, out)
	}
}

func TestNoLinksBelowSelectedRoot(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "client.toml")
	if err := os.WriteFile(file, []byte("managed"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	directoryLink(t, root, alias)
	for _, path := range []string{filepath.Join(alias, "client.toml"), filepath.Join(alias, "future", "client.toml")} {
		if err := NoLinksBelow(alias, path); err != nil {
			t.Fatalf("explicit root alias: %v", err)
		}
	}
	inside := filepath.Join(root, "linked")
	directoryLink(t, t.TempDir(), inside)
	if err := NoLinksBelow(root, filepath.Join(inside, "future.toml")); err == nil {
		t.Fatal("accepted link inside managed root")
	}
	if err := NoLinksBelow(root, filepath.Join(root, "..", "outside.toml")); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("accepted path outside root: %v", err)
	}
}
