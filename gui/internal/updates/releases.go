package updates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/semver"
)

const (
	releasesAPIURL     = "https://api.github.com/repos/exdoubled/FDU-Bridge/releases?per_page=20"
	releasePagePrefix  = "https://github.com/exdoubled/FDU-Bridge/releases/tag/"
	maxDownloadSize    = 256 << 20
	maxReleaseMetadata = 4 << 20
)

type Release struct {
	Version, Notes, URL, AssetName, AssetURL, SHA256 string
	Size                                             int64
}

type githubAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

type githubRelease struct {
	Tag        string        `json:"tag_name"`
	Notes      string        `json:"body"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	Assets     []githubAsset `json:"assets"`
}

// Check returns the newest release allowed by the installed version's channel.
func Check(ctx context.Context, client *http.Client, current, goos, arch string) (*Release, error) {
	if !semver.IsValid(current) {
		return nil, fmt.Errorf("cannot check updates without a valid release comparison version: %q", current)
	}
	if _, err := platformAssetName(current, goos, arch); err != nil {
		return nil, err
	}
	body, err := readReleaseURL(ctx, client, releasesAPIURL, maxReleaseMetadata)
	if err != nil {
		return nil, err
	}
	var releases []githubRelease
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, fmt.Errorf("decode releases: %w", err)
	}
	var latest *githubRelease
	for i := range releases {
		r := &releases[i]
		if r.Draft || !semver.IsValid(r.Tag) || semver.Compare(r.Tag, current) <= 0 {
			continue
		}
		if semver.Prerelease(current) == "" && (r.Prerelease || semver.Prerelease(r.Tag) != "") {
			continue
		}
		if latest == nil || semver.Compare(r.Tag, latest.Tag) > 0 {
			latest = r
		}
	}
	if latest == nil {
		return nil, nil
	}
	name, _ := platformAssetName(latest.Tag, goos, arch)
	var asset, sums *githubAsset
	for i := range latest.Assets {
		a := &latest.Assets[i]
		if a.Name == name {
			if asset != nil {
				return nil, errors.New("release has duplicate platform assets")
			}
			asset = a
		}
		if a.Name == "SHA256SUMS.txt" {
			if sums != nil {
				return nil, errors.New("release has duplicate checksum assets")
			}
			sums = a
		}
	}
	if asset == nil {
		return nil, fmt.Errorf("release %s does not contain %s", latest.Tag, name)
	}
	if asset.Size <= 0 || asset.Size > maxDownloadSize || !validReleaseAssetURL(asset.URL, latest.Tag, name) {
		return nil, errors.New("release contains an invalid platform asset URL or size")
	}
	digest := strings.TrimPrefix(asset.Digest, "sha256:")
	if asset.Digest != "" {
		if !strings.HasPrefix(asset.Digest, "sha256:") || !validReleaseSHA256(digest) {
			return nil, errors.New("release asset has an invalid SHA256 digest")
		}
	} else {
		if sums == nil || sums.Size <= 0 || sums.Size > maxReleaseMetadata || !validReleaseAssetURL(sums.URL, latest.Tag, sums.Name) {
			return nil, errors.New("release has no valid SHA256 checksum asset")
		}
		body, err := readReleaseURL(ctx, client, sums.URL, maxReleaseMetadata)
		if err != nil {
			return nil, err
		}
		if int64(len(body)) != sums.Size {
			return nil, errors.New("checksum asset size does not match release metadata")
		}
		for _, line := range strings.Split(string(body), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
				if digest != "" || !validReleaseSHA256(fields[0]) {
					return nil, errors.New("release has an invalid or duplicate platform checksum")
				}
				digest = fields[0]
			}
		}
		if digest == "" {
			return nil, errors.New("release has no SHA256 checksum for this platform")
		}
	}
	return &Release{Version: latest.Tag, Notes: latest.Notes, URL: releasePagePrefix + latest.Tag, AssetName: name, AssetURL: asset.URL, SHA256: strings.ToLower(digest), Size: asset.Size}, nil
}

func platformAssetName(version, goos, arch string) (string, error) {
	prefix := "FDU-Connect-" + version
	switch {
	case goos == "windows" && arch == "amd64":
		return prefix + "-Windows-x64.zip", nil
	case goos == "linux" && (arch == "amd64" || arch == "arm64"):
		return prefix + "-linux-" + arch + ".tar.gz", nil
	case goos == "darwin" && (arch == "amd64" || arch == "arm64"):
		return prefix + "-macOS-universal.zip", nil
	default:
		return "", fmt.Errorf("updates are not available for %s/%s", goos, arch)
	}
}

func validReleaseSHA256(digest string) bool {
	decoded, err := hex.DecodeString(digest)
	return err == nil && len(decoded) == sha256.Size
}

func validReleaseAssetURL(raw, version, name string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == "github.com" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.Opaque == "" && u.Path == "/exdoubled/FDU-Bridge/releases/download/"+version+"/"+name
}

func requestReleaseURL(ctx context.Context, client *http.Client, raw string) (*http.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	safeClient := *client
	safeClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many release redirects")
		}
		host := req.URL.Host
		if req.URL.Scheme != "https" || req.URL.User != nil || (host != "api.github.com" && host != "github.com" && host != "release-assets.githubusercontent.com" && host != "objects.githubusercontent.com") {
			return errors.New("release redirected outside GitHub HTTPS hosts")
		}
		if client.CheckRedirect != nil {
			return client.CheckRedirect(req, via)
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "FDU-Connect-Updater")
	if raw == releasesAPIURL {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	response, err := safeClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch release: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("fetch release: HTTP %d", response.StatusCode)
	}
	return response, nil
}

func readReleaseURL(ctx context.Context, client *http.Client, raw string, limit int64) ([]byte, error) {
	response, err := requestReleaseURL(ctx, client, raw)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errors.New("release metadata exceeds size limit")
	}
	return body, ctx.Err()
}

// Download replaces dest only after the archive matches its declared size and SHA256.
func Download(ctx context.Context, client *http.Client, release *Release, dest string, progress func(int64, int64)) error {
	if release == nil || !semver.IsValid(release.Version) || release.Size <= 0 || release.Size > maxDownloadSize || !validReleaseSHA256(release.SHA256) || !validReleaseAssetURL(release.AssetURL, release.Version, release.AssetName) {
		return errors.New("invalid release download metadata")
	}
	validName := false
	for _, platform := range [][2]string{{"windows", "amd64"}, {"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "arm64"}} {
		name, _ := platformAssetName(release.Version, platform[0], platform[1])
		validName = validName || release.AssetName == name
	}
	if !validName || dest == "" {
		return errors.New("invalid release asset name or destination")
	}
	response, err := requestReleaseURL(ctx, client, release.AssetURL)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.ContentLength >= 0 && response.ContentLength != release.Size {
		return errors.New("download content length does not match release metadata")
	}
	file, err := os.CreateTemp(filepath.Dir(dest), ".fdu-update-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	hash := sha256.New()
	writer := &releaseProgressWriter{writer: io.MultiWriter(file, hash), progress: progress, total: release.Size}
	if progress != nil {
		progress(0, release.Size)
	}
	n, err := io.Copy(writer, io.LimitReader(response.Body, release.Size+1))
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if n != release.Size || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), release.SHA256) {
		return errors.New("download size or SHA256 does not match release metadata")
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), dest)
}

type releaseProgressWriter struct {
	writer   io.Writer
	progress func(int64, int64)
	done     int64
	total    int64
}

func (w *releaseProgressWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.done += int64(n)
	if w.progress != nil {
		w.progress(w.done, w.total)
	}
	return n, err
}
