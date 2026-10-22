package appconf

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mythologyli/zju-connect/gui/internal/sysproc"
)

// ValidateClashDir requires the profile extension format used by Verge Rev.
// Other mihomo clients must not be modified as if they used this format.
func ValidateClashDir(dir string) error {
	for _, name := range []string{"profiles.yaml", "verge.yaml", "clash-verge.yaml"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err != nil || info.IsDir() {
			return errors.New("未找到兼容的 Clash Verge Rev 配置；支持普通及便携安装，请在设置中选择含 profiles.yaml、verge.yaml 和 clash-verge.yaml 的目录。其他 Clash 客户端暂不支持自动接入")
		}
	}
	return nil
}

var clashPathArg = regexp.MustCompile(`(?:^|\s)(-d|--dir|--config-dir|-f|--config)(?:=|\s+)(?:"([^"]+)"|'([^']+)'|(\S+))`)
var clashExecutable = regexp.MustCompile(`^(?:"([^"]+)"|(\S+))`)

func clashProcessDirs(line string) []string {
	var dirs []string
	for _, match := range clashPathArg.FindAllStringSubmatch(line, -1) {
		path := match[2] + match[3] + match[4]
		if !filepath.IsAbs(path) {
			continue
		}
		if match[1] == "-f" || match[1] == "--config" {
			path = filepath.Dir(path)
		}
		dirs = append(dirs, path)
	}
	if match := clashExecutable.FindStringSubmatch(strings.TrimSpace(line)); match != nil {
		exe := match[1] + match[2]
		if filepath.IsAbs(exe) {
			base := filepath.Dir(exe)
			dirs = append(dirs, base)
			// Portable clients keep application data beside the executable, often under .config.
			for _, path := range []string{filepath.Join(base, ".config"), filepath.Join(base, "data")} {
				dirs = append(dirs, path)
				entries, _ := os.ReadDir(path)
				for _, entry := range entries {
					if entry.IsDir() {
						dirs = append(dirs, filepath.Join(path, entry.Name()))
					}
				}
			}
		}
	}
	return dirs
}

func ResolveClashDir(preferred string) (string, error) {
	entries, _ := sysproc.List()
	var running []string
	seen := map[string]bool{}
	for _, entry := range entries {
		name := strings.ToLower(entry.Name)
		if !strings.Contains(name, "mihomo") && !strings.Contains(name, "clash") {
			continue
		}
		line, err := sysproc.CommandLine(entry.PID)
		if err != nil {
			continue
		}
		for _, dir := range clashProcessDirs(line) {
			dir = filepath.Clean(dir)
			if ValidateClashDir(dir) == nil && !seen[dir] {
				running = append(running, dir)
				seen[dir] = true
			}
		}
	}
	for _, dir := range running {
		if strings.EqualFold(filepath.Clean(preferred), dir) {
			return dir, nil
		}
	}
	if len(running) == 1 {
		return running[0], nil
	}
	if len(running) > 1 {
		return "", errors.New("检测到多个 Clash Verge Rev 实例，请在设置中选择要接入的配置目录")
	}
	for _, dir := range []string{preferred, DefaultClashDir()} {
		if dir != "" && ValidateClashDir(dir) == nil {
			return dir, nil
		}
	}
	return "", ValidateClashDir("")
}
