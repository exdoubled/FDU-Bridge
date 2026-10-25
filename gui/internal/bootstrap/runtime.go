package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

func (s *Startup) checkRuntime(probe func() (bool, error)) error {
	available, err := probe()
	if err != nil {
		s.Logger.Warn("WebView2 detection failed; continuing with Wails", "error", err)
		return nil
	}
	if !available {
		return errors.New("缺少 Microsoft WebView2 Runtime，请运行 *-Windows-x64-setup.exe 安装或修复后重试")
	}
	return nil
}

// Match Wails' discovery checks; loading the DLL is left to the framework.
func runtimePresent(folder, arch string) (bool, error) {
	versionString := filepath.Base(folder)
	if i := strings.Index(versionString, " "); i > 0 {
		versionString = versionString[:i]
	}
	parts := strings.Split(versionString, ".")
	if len(parts) > 4 {
		return false, nil
	}
	var version [4]int64
	for i, part := range parts {
		n, err := strconv.ParseInt(part, 10, 32)
		if err != nil {
			return false, nil
		}
		version[i] = n
	}
	if slices.Compare(version[:], []int64{86, 0, 616, 0}) < 0 {
		return false, nil
	}
	var subdir string
	switch arch {
	case "amd64":
		subdir = "x64"
	case "arm64":
		subdir = "arm64"
	case "386":
		subdir = "x86"
	default:
		return false, fmt.Errorf("unsupported WebView2 architecture: %s", arch)
	}
	if !filepath.IsAbs(folder) {
		exe, err := os.Executable()
		if err != nil {
			return false, err
		}
		folder = filepath.Join(filepath.Dir(exe), folder)
	}
	_, err := os.Stat(filepath.Join(folder, "EBWebView", subdir, "EmbeddedBrowserWebView.dll"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
