package authchallenge

import (
	"golang.org/x/sys/windows/registry"
	"os"
	"regexp"
)

func browserRegistry(root registry.Key, path, name string) string {
	key, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()
	value, _, _ := key.GetStringValue(name)
	return value
}

func defaultLoginBrowser() string {
	id := browserRegistry(registry.CURRENT_USER, `Software\Microsoft\Windows\Shell\Associations\UrlAssociations\https\UserChoice`, "ProgId")
	if id == "" {
		return ""
	}
	command := browserRegistry(registry.CLASSES_ROOT, id+`\shell\open\command`, "")
	command = regexp.MustCompile(`%([^%]+)%`).ReplaceAllStringFunc(command, func(value string) string {
		return os.Getenv(value[1 : len(value)-1])
	})
	return browserExecutable(command)
}

func registeredLoginBrowsers() []string {
	var paths []string
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		for _, executable := range []string{"msedge.exe", "chrome.exe", "chromium.exe", "brave.exe"} {
			paths = append(paths, browserRegistry(root, `Software\Microsoft\Windows\CurrentVersion\App Paths\`+executable, ""))
		}
	}
	return paths
}
