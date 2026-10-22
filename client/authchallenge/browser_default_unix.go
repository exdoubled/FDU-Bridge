//go:build !windows

package authchallenge

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func registeredLoginBrowsers() []string { return nil }

func defaultLoginBrowser() string {
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		path := filepath.Join(home, "Library", "Preferences", "com.apple.LaunchServices", "com.apple.launchservices.secure.plist")
		var handlers []struct {
			Scheme string `json:"LSHandlerURLScheme"`
			Bundle string `json:"LSHandlerRoleAll"`
		}
		if json.Unmarshal([]byte(browserQuery("plutil", "-extract", "LSHandlers", "json", "-o", "-", path)), &handlers) != nil {
			return ""
		}
		for _, handler := range handlers {
			if handler.Scheme != "https" || handler.Bundle == "" {
				continue
			}
			apps := browserQuery("mdfind", "kMDItemCFBundleIdentifier == '"+handler.Bundle+"'")
			for _, app := range strings.Split(apps, "\n") {
				executable := browserQuery("plutil", "-extract", "CFBundleExecutable", "raw", "-o", "-", filepath.Join(app, "Contents", "Info.plist"))
				if executable != "" {
					return filepath.Join(app, "Contents", "MacOS", executable)
				}
			}
		}
		return ""
	}
	entry := browserQuery("xdg-settings", "get", "default-web-browser")
	if entry == "" || filepath.Base(entry) != entry {
		return ""
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	dataDirs := os.Getenv("XDG_DATA_DIRS")
	if dataDirs == "" {
		dataDirs = "/usr/local/share:/usr/share"
	}
	for _, dir := range append([]string{dataHome}, filepath.SplitList(dataDirs)...) {
		data, err := os.ReadFile(filepath.Join(dir, "applications", entry))
		if err != nil {
			continue
		}
		inEntry := false
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "[") {
				inEntry = line == "[Desktop Entry]"
			}
			if inEntry && strings.HasPrefix(line, "Exec=") {
				path := browserExecutable(strings.TrimPrefix(line, "Exec="))
				if filepath.IsAbs(path) {
					return path
				}
				path, _ = exec.LookPath(path)
				return path
			}
		}
	}
	return ""
}
