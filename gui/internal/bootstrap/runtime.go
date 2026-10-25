package bootstrap

import (
	"debug/pe"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Match the installed Runtime layout used by Wails' WebView2 loader. An Edge
// browser registration alone cannot satisfy this check.
func runtimePresent(folder, arch string) bool {
	if !filepath.IsAbs(folder) {
		return false
	}
	parts := strings.Split(filepath.Base(folder), ".")
	if len(parts) != 4 {
		return false
	}
	var version [4]uint64
	for i, part := range parts {
		n, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return false
		}
		version[i] = n
	}
	if slices.Compare(version[:], []uint64{86, 0, 616, 0}) < 0 {
		return false
	}
	var subdir string
	var machine uint16
	switch arch {
	case "amd64":
		subdir, machine = "x64", pe.IMAGE_FILE_MACHINE_AMD64
	case "arm64":
		subdir, machine = "arm64", pe.IMAGE_FILE_MACHINE_ARM64
	case "386":
		subdir, machine = "x86", pe.IMAGE_FILE_MACHINE_I386
	default:
		return false
	}
	browser, err := os.Stat(filepath.Join(folder, "msedgewebview2.exe"))
	if err != nil || !browser.Mode().IsRegular() || browser.Size() == 0 {
		return false
	}
	dll, err := pe.Open(filepath.Join(folder, "EBWebView", subdir, "EmbeddedBrowserWebView.dll"))
	if err != nil {
		return false
	}
	defer dll.Close()
	return dll.Machine == machine && dll.Characteristics&pe.IMAGE_FILE_DLL != 0
}
