package authchallenge

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Only launch browsers that implement the interception protocol with a separate profile.
func loginBrowsers() []string {
	paths := append([]string{defaultLoginBrowser()}, registeredLoginBrowsers()...)
	paths = append(paths, standardLoginBrowsers()...)
	var result []string
	seen := map[string]bool{}
	for _, path := range paths {
		name := strings.ToLower(strings.TrimSuffix(filepath.Base(path), ".exe"))
		switch name {
		case "msedge", "microsoft edge", "microsoft-edge", "microsoft-edge-stable", "chrome", "google chrome", "google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "brave", "brave browser", "brave-browser":
			if !seen[path] {
				result = append(result, path)
				seen[path] = true
			}
		}
	}
	return result
}

// Read browser associations only; never execute a desktop entry or a shell command.
func browserQuery(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func browserExecutable(command string) string {
	command = strings.TrimSpace(command)
	if strings.HasPrefix(command, `"`) {
		if end := strings.Index(command[1:], `"`); end >= 0 {
			return command[1 : end+1]
		}
		return ""
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
