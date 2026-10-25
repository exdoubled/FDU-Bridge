package updates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testFeedURL = "https://api.github.com/repos/exdoubled/FDU-Bridge/releases?per_page=20"
const testRepoURL = "https://github.com/exdoubled/FDU-Bridge/releases/"

type releaseTransport func(*http.Request) (*http.Response, error)

func (f releaseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func releaseClient(t *testing.T, routes map[string]string) *http.Client {
	t.Helper()
	return &http.Client{Transport: releaseTransport(func(r *http.Request) (*http.Response, error) {
		body, ok := routes[r.URL.String()]
		if !ok {
			return nil, fmt.Errorf("unexpected request: %s", r.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), ContentLength: -1, Request: r}, nil
	})}
}

func releaseJSON(t *testing.T, releases ...map[string]any) string {
	t.Helper()
	b, err := json.Marshal(releases)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func releaseFixture(version, name string) map[string]any {
	return map[string]any{
		"tag_name": version, "html_url": testRepoURL + "tag/" + version,
		"body": "Release notes", "draft": false, "prerelease": strings.Contains(version, "-"),
		"assets": []map[string]any{{"name": name, "browser_download_url": testRepoURL + "download/" + version + "/" + name, "size": 7, "digest": "sha256:" + strings.Repeat("a", 64)}},
	}
}

func TestCheckSelectsNewerAllowedSemver(t *testing.T) {
	stable := releaseFixture("v1.0.0", "FDU-Connect-v1.0.0-Windows-x64.zip")
	beta2 := releaseFixture("v1.1.0-beta.2", "FDU-Connect-v1.1.0-beta.2-Windows-x64.zip")
	beta10 := releaseFixture("v1.1.0-beta.10", "FDU-Connect-v1.1.0-beta.10-Windows-x64.zip")
	draft := releaseFixture("v9.0.0", "FDU-Connect-v9.0.0-Windows-x64.zip")
	draft["draft"] = true
	feed := releaseJSON(t, beta2, draft, stable, beta10)
	for _, tc := range []struct{ current, want string }{
		{"v0.9.0", "v1.0.0"}, {"v1.0.0", ""}, {"v2.0.0", ""},
		{"v1.1.0-beta.1", "v1.1.0-beta.10"}, {"v0.9.0-beta.1", "v1.1.0-beta.10"},
	} {
		t.Run(tc.current, func(t *testing.T) {
			got, err := Check(context.Background(), releaseClient(t, map[string]string{testFeedURL: feed}), tc.current, "windows", "amd64")
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if got != nil {
					t.Fatalf("unexpected update: %+v", got)
				}
			} else if got == nil || got.Version != tc.want || got.Notes != "Release notes" || got.URL != testRepoURL+"tag/"+tc.want || got.Size != 7 || got.SHA256 != strings.Repeat("a", 64) {
				t.Fatalf("release = %+v, want %s", got, tc.want)
			}
		})
	}
	got, err := Check(context.Background(), releaseClient(t, map[string]string{testFeedURL: releaseJSON(t, stable)}), "v1.0.0-beta.1", "windows", "amd64")
	if err != nil || got == nil || got.Version != "v1.0.0" {
		t.Fatalf("beta to stable: %+v, %v", got, err)
	}
}

func TestCheckExactPlatformAssets(t *testing.T) {
	for _, tc := range []struct{ goos, arch, name string }{
		{"windows", "amd64", "FDU-Connect-v1.0.0-Windows-x64.zip"},
		{"linux", "amd64", "FDU-Connect-v1.0.0-linux-amd64.tar.gz"},
		{"linux", "arm64", "FDU-Connect-v1.0.0-linux-arm64.tar.gz"},
		{"darwin", "amd64", "FDU-Connect-v1.0.0-macOS-universal.zip"},
		{"darwin", "arm64", "FDU-Connect-v1.0.0-macOS-universal.zip"},
	} {
		t.Run(tc.goos+"/"+tc.arch, func(t *testing.T) {
			fixture := releaseFixture("v1.0.0", tc.name)
			got, err := Check(context.Background(), releaseClient(t, map[string]string{testFeedURL: releaseJSON(t, fixture)}), "v0.9.0", tc.goos, tc.arch)
			if err != nil || got == nil || got.AssetName != tc.name || got.AssetURL != testRepoURL+"download/v1.0.0/"+tc.name {
				t.Fatalf("release = %+v, %v", got, err)
			}
		})
	}
}

func TestCheckWindowsSelectsLightweightPackageAlongsideOfflinePackage(t *testing.T) {
	const name = "FDU-Connect-v1.0.0-Windows-x64.zip"
	for _, offlineFirst := range []bool{true, false} {
		fixture := releaseFixture("v1.0.0", name)
		assets := fixture["assets"].([]map[string]any)
		offline := map[string]any{
			"name":                 "FDU-Connect-v1.0.0-Windows-x64-offline.zip",
			"browser_download_url": testRepoURL + "download/v1.0.0/FDU-Connect-v1.0.0-Windows-x64-offline.zip",
			"size":                 300 << 20, "digest": "sha256:" + strings.Repeat("b", 64),
		}
		if offlineFirst {
			fixture["assets"] = append([]map[string]any{offline}, assets...)
		} else {
			fixture["assets"] = append(assets, offline)
		}
		got, err := Check(context.Background(), releaseClient(t, map[string]string{testFeedURL: releaseJSON(t, fixture)}), "v0.9.0", "windows", "amd64")
		if err != nil || got == nil || got.AssetName != name || got.Size != 7 || got.SHA256 != strings.Repeat("a", 64) {
			t.Fatalf("offlineFirst=%v: update = %+v, %v", offlineFirst, got, err)
		}
	}
}

func TestCheckChecksumFallback(t *testing.T) {
	const name = "FDU-Connect-v1.0.0-Windows-x64.zip"
	const sums = "unrelated entry\n" + "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  *" + name + "\n"
	fixture := releaseFixture("v1.0.0", name)
	assets := fixture["assets"].([]map[string]any)
	delete(assets[0], "digest")
	fixture["assets"] = append(assets, map[string]any{"name": "SHA256SUMS.txt", "browser_download_url": testRepoURL + "download/v1.0.0/SHA256SUMS.txt", "size": len(sums)})
	client := releaseClient(t, map[string]string{testFeedURL: releaseJSON(t, fixture), testRepoURL + "download/v1.0.0/SHA256SUMS.txt": sums})
	got, err := Check(context.Background(), client, "v0.9.0", "windows", "amd64")
	if err != nil || got == nil || got.SHA256 != strings.Repeat("b", 64) {
		t.Fatalf("release = %+v, %v", got, err)
	}
}

func TestCheckRejectsUnsafeMetadata(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"wrong asset", func(r map[string]any) { r["assets"].([]map[string]any)[0]["name"] = "other.zip" }},
		{"external URL", func(r map[string]any) {
			r["assets"].([]map[string]any)[0]["browser_download_url"] = "https://example.com/update.zip"
		}},
		{"HTTP URL", func(r map[string]any) {
			r["assets"].([]map[string]any)[0]["browser_download_url"] = "http://github.com/exdoubled/FDU-Bridge/releases/download/v1.0.0/FDU-Connect-v1.0.0-Windows-x64.zip"
		}},
		{"oversized", func(r map[string]any) { r["assets"].([]map[string]any)[0]["size"] = 256<<20 + 1 }},
		{"empty asset", func(r map[string]any) { r["assets"].([]map[string]any)[0]["size"] = 0 }},
		{"no checksum", func(r map[string]any) { delete(r["assets"].([]map[string]any)[0], "digest") }},
		{"bad checksum", func(r map[string]any) { r["assets"].([]map[string]any)[0]["digest"] = "sha256:wrong" }},
		{"metadata limit", func(r map[string]any) { r["body"] = strings.Repeat("x", 4<<20) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := releaseFixture("v1.0.0", "FDU-Connect-v1.0.0-Windows-x64.zip")
			tc.edit(fixture)
			if got, err := Check(context.Background(), releaseClient(t, map[string]string{testFeedURL: releaseJSON(t, fixture)}), "v0.9.0", "windows", "amd64"); err == nil || got != nil {
				t.Fatalf("unsafe release accepted: %+v, %v", got, err)
			}
		})
	}
	for _, tc := range []struct{ current, goos, arch string }{{"dev", "windows", "amd64"}, {"", "windows", "amd64"}, {"v0.9.0", "windows", "arm64"}} {
		if _, err := Check(context.Background(), releaseClient(t, nil), tc.current, tc.goos, tc.arch); err == nil {
			t.Fatalf("invalid input accepted: %+v", tc)
		}
	}
}

func TestDownloadVerifiesBeforeReplacingDestination(t *testing.T) {
	const body = "archive"
	hash := sha256.Sum256([]byte(body))
	release := Release{Version: "v1.0.0", AssetName: "FDU-Connect-v1.0.0-Windows-x64.zip", AssetURL: testRepoURL + "download/v1.0.0/FDU-Connect-v1.0.0-Windows-x64.zip", SHA256: hex.EncodeToString(hash[:]), Size: 7}
	dir := t.TempDir()
	dest := filepath.Join(dir, "update.zip")
	if err := os.WriteFile(dest, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	var done, total int64
	err := Download(context.Background(), releaseClient(t, map[string]string{release.AssetURL: body}), &release, dest, func(n, size int64) { done, total = n, size })
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != body || done != 7 || total != 7 {
		t.Fatalf("download = %q (%d/%d), %v", got, done, total, err)
	}
	for _, tc := range []struct{ name, body, hash string }{
		{"hash mismatch", body, strings.Repeat("0", 64)}, {"short body", "short", release.SHA256}, {"long body", body + "extra", release.SHA256},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := release
			bad.SHA256 = tc.hash
			if err := Download(context.Background(), releaseClient(t, map[string]string{bad.AssetURL: tc.body}), &bad, dest, nil); err == nil {
				t.Fatal("invalid download accepted")
			}
			got, err := os.ReadFile(dest)
			if err != nil || string(got) != body {
				t.Fatalf("existing destination changed: %q, %v", got, err)
			}
			files, err := os.ReadDir(dir)
			if err != nil || len(files) != 1 {
				t.Fatalf("temporary file leaked: %v, %v", files, err)
			}
		})
	}
	bad := release
	bad.AssetURL = "https://example.com/update.zip"
	if err := Download(context.Background(), releaseClient(t, nil), &bad, dest, nil); err == nil {
		t.Fatal("external download URL accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Download(ctx, releaseClient(t, map[string]string{release.AssetURL: body}), &release, dest, nil); err == nil {
		t.Fatal("canceled download accepted")
	}
}

func TestDownloadAllowsOnlyGitHubHTTPSRedirects(t *testing.T) {
	const body = "archive"
	hash := sha256.Sum256([]byte(body))
	release := Release{Version: "v1.0.0", AssetName: "FDU-Connect-v1.0.0-Windows-x64.zip", AssetURL: testRepoURL + "download/v1.0.0/FDU-Connect-v1.0.0-Windows-x64.zip", SHA256: hex.EncodeToString(hash[:]), Size: 7}
	for _, tc := range []struct {
		redirect string
		wantErr  bool
	}{
		{"https://release-assets.githubusercontent.com/github-production-release-asset/test", false},
		{"https://example.com/update.zip", true},
		{"http://github.com/update.zip", true},
	} {
		t.Run(tc.redirect, func(t *testing.T) {
			client := &http.Client{Transport: releaseTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() == release.AssetURL {
					return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {tc.redirect}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), ContentLength: 7, Request: r}, nil
			})}
			dest := filepath.Join(t.TempDir(), "update.zip")
			err := Download(context.Background(), client, &release, dest, nil)
			if (err != nil) != tc.wantErr {
				t.Fatalf("redirect error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				if _, err := os.Stat(dest); !os.IsNotExist(err) {
					t.Fatalf("rejected redirect created destination: %v", err)
				}
			}
		})
	}
}
