package updates

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStageDownloadedWindowsArchive(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("fixture matches the Windows Compress-Archive release layout")
	}
	for _, kind := range []string{"public files", "with installer", "traversal", "missing core", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			install := filepath.Join(base, "install")
			gui, core := "FDU-Connect-GUI.exe", "fdu-connect.exe"
			for _, name := range []string{gui, core, "config.toml", "profile-lenovo.yaml"} {
				writeInstallFile(t, filepath.Join(install, name), "old-"+name)
			}
			var archive bytes.Buffer
			writer := zip.NewWriter(&archive)
			names := []string{gui, core, "LICENSE", "config.toml.example", "config.toml", "profile-lenovo.yaml"}
			if kind == "with installer" {
				names = append(names, "MicrosoftEdgeWebView2RuntimeInstallerX64.exe", "Uninstall.exe", "unrelated.exe")
			}
			if kind == "traversal" {
				names = append(names, "../escaped.txt")
			}
			if kind == "symlink" {
				names = append(names, "link")
			}
			for _, name := range names {
				if kind == "missing core" && name == core {
					continue
				}
				header := &zip.FileHeader{Name: name, Method: zip.Deflate}
				header.SetMode(0o644)
				if name == "link" {
					header.SetMode(os.ModeSymlink | 0o777)
				}
				entry, err := writer.CreateHeader(header)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := entry.Write([]byte("new-" + name)); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			const name = "FDU-Connect-v1.0.0-Windows-x64.zip"
			assetURL := testRepoURL + "download/v1.0.0/" + name
			digest := sha256.Sum256(archive.Bytes())
			fixture := releaseFixture("v1.0.0", name)
			asset := fixture["assets"].([]map[string]any)[0]
			asset["size"], asset["digest"] = archive.Len(), "sha256:"+hex.EncodeToString(digest[:])
			client := releaseClient(t, map[string]string{testFeedURL: releaseJSON(t, fixture), assetURL: archive.String()})
			release, err := Check(context.Background(), client, "v0.9.0", "windows", "amd64")
			if err != nil || release == nil {
				t.Fatalf("check = %+v, %v", release, err)
			}
			pending, err := Stage(context.Background(), client, release, filepath.Join(install, gui), nil)
			if kind == "public files" || kind == "with installer" {
				if err != nil || pending == nil {
					t.Fatalf("stage = %+v, %v", pending, err)
				}
				wantFiles := 4
				if len(pending.Files) != wantFiles {
					t.Fatalf("staged files = %+v", pending.Files)
				}
				for _, file := range pending.Files {
					name := filepath.Base(file.Source)
					if name != gui && name != core && name != "LICENSE" && name != "config.toml.example" {
						t.Fatalf("personal file staged: %s", name)
					}
					if value, err := os.ReadFile(file.Source); err != nil || string(value) != "new-"+name || file.Target != filepath.Join(install, name) {
						t.Fatalf("wrong staged file: %+v, %q, %v", file, value, err)
					}
				}
				for _, name := range []string{"config.toml", "profile-lenovo.yaml"} {
					if _, err := os.Stat(filepath.Join(pending.Root, "payload", name)); !os.IsNotExist(err) {
						t.Fatalf("personal file extracted: %s, %v", name, err)
					}
				}
			} else {
				if err == nil || pending != nil {
					t.Fatalf("unsafe archive accepted: %+v, %v", pending, err)
				}
				if roots, err := filepath.Glob(filepath.Join(base, ".fdu-update-*")); err != nil || len(roots) != 0 {
					t.Fatalf("failed staging leaked files: %v, %v", roots, err)
				}
			}
			for _, name := range []string{gui, core, "config.toml", "profile-lenovo.yaml"} {
				if value, err := os.ReadFile(filepath.Join(install, name)); err != nil || string(value) != "old-"+name {
					t.Fatalf("installation changed during staging: %s, %q, %v", name, value, err)
				}
			}
		})
	}
}
