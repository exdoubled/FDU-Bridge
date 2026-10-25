package clash

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mythologyli/zju-connect/gui/internal/appconf"
	"github.com/mythologyli/zju-connect/gui/internal/fsx"
	"github.com/mythologyli/zju-connect/gui/internal/sysproc"
	"gopkg.in/yaml.v3"
)

// ClientTarget exposes only local file metadata. Controller is private binding
// state because some cores receive their address and secret through CLI flags.
type ClientTarget struct {
	Kind        string      `json:"kind"`
	Name        string      `json:"name"`
	Root        string      `json:"root"`
	RuntimeFile string      `json:"runtimeFile"`
	Controller  *ControlAPI `json:"-"`
	Executable  string      `json:"-"`
}

type clientProcess struct{ name, line string }

var ErrClientNotRunning = errors.New("未找到可读取本机控制接口的代理运行配置，请启动客户端，或手动选择 Verge 配置目录或 Mihomo 运行配置文件")

// DiscoverClients reads process arguments and associated client paths. It does not
// connect to controllers, scan subscription caches, or change client files.
func DiscoverClients() ([]ClientTarget, error) {
	entries, err := sysproc.List()
	if err != nil {
		return nil, err
	}
	var processes []clientProcess
	for _, entry := range entries {
		if !clientProcessName(entry.Name) {
			continue
		}
		if line, err := sysproc.CommandLine(entry.PID); err == nil {
			processes = append(processes, clientProcess{entry.Name, line})
		}
	}
	return discoverClientProcesses(processes), nil
}

// ResolveClientTarget honors a remembered manual selection before automatic discovery.
func ResolveClientTarget(preferred string) (ClientTarget, error) {
	targets, err := DiscoverClients()
	if err != nil && strings.TrimSpace(preferred) == "" {
		return ClientTarget{}, err
	}
	return ResolveDiscoveredTarget(preferred, targets)
}

// ResolveDiscoveredTarget resolves against an existing discovery snapshot, so a
// caller can make every decision from a single process scan.
func ResolveDiscoveredTarget(preferred string, targets []ClientTarget) (ClientTarget, error) {
	if preferred = strings.TrimSpace(preferred); preferred != "" {
		return resolvePreferredClientTarget(preferred, targets)
	}
	return selectClientTarget(targets)
}

func resolvePreferredClientTarget(preferred string, targets []ClientTarget) (ClientTarget, error) {
	path, err := filepath.Abs(preferred)
	if err != nil {
		return ClientTarget{}, errors.New("无法解析所选客户端配置路径")
	}
	var matching []ClientTarget
	for _, target := range targets {
		if clientPathKey(path) == clientPathKey(target.RuntimeFile) || (target.Kind == "verge" && clientPathKey(path) == clientPathKey(target.Root)) {
			matching = append(matching, target)
		}
	}
	if len(matching) > 0 {
		return selectClientTarget(matching)
	}
	if appconf.ValidateClashDir(path) == nil {
		return ClientTarget{Kind: "verge", Name: "Clash Verge Rev", Root: path, RuntimeFile: filepath.Join(path, "clash-verge.yaml")}, nil
	}
	controller, err := clientRuntimeController(path, nil)
	if err != nil {
		if errors.Is(err, ErrClientNotRunning) {
			return ClientTarget{}, err
		}
		return ClientTarget{}, errors.New("所选路径不是兼容的 Verge 目录或含本机控制接口的 Mihomo 运行配置，请重新选择")
	}
	return ClientTarget{Kind: "runtime", Name: clientLabel("", path), Root: filepath.Dir(path), RuntimeFile: path, Controller: controller}, nil
}

func selectClientTarget(targets []ClientTarget) (ClientTarget, error) {
	if len(targets) == 1 {
		return targets[0], nil
	}
	if len(targets) > 1 {
		return ClientTarget{}, errors.New("检测到多个代理客户端或内核，请选择要接入的 Verge 配置目录或 Mihomo 运行配置文件")
	}
	return ClientTarget{}, ErrClientNotRunning
}

func clientProcessName(name string) bool {
	name = strings.ToLower(name)
	return strings.Contains(name, "mihomo") || strings.Contains(name, "clash") || strings.Contains(name, "sparkle") || strings.Contains(name, "nyanpasu")
}

func clientCoreProcess(name string) bool {
	name = strings.TrimSuffix(strings.ToLower(filepath.Base(name)), ".exe")
	return strings.Contains(name, "mihomo") || name == "clash" || strings.HasPrefix(name, "clash-meta") || strings.Contains(name, "clashcore") || strings.Contains(name, "clash-core")
}

func clientLabel(name, executable string) string {
	text := strings.ToLower(name + " " + executable)
	switch {
	case strings.Contains(text, "verge"):
		return "Clash Verge Rev"
	case strings.Contains(text, "flclash"):
		return "FlClash"
	case strings.Contains(text, "party"):
		return "Clash Party"
	case strings.Contains(text, "sparkle"):
		return "Sparkle"
	case strings.Contains(text, "nyanpasu"):
		return "Nyanpasu"
	case strings.Contains(text, "clashx"):
		return "ClashX.Meta"
	default:
		return "Mihomo"
	}
}

// processArguments preserves Windows path separators and accepts both quote styles
// used by CommandLine and the quoted argv representation on Unix.
func processArguments(line string) []string {
	var args []string
	var arg strings.Builder
	var quote rune
	started := false
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				arg.WriteRune(r)
			}
		case r == '"' || (r == '\'' && (arg.Len() == 0 || strings.HasSuffix(arg.String(), "="))):
			quote, started = r, true
		case unicode.IsSpace(r):
			if started {
				args = append(args, arg.String())
				arg.Reset()
				started = false
			}
		default:
			arg.WriteRune(r)
			started = true
		}
	}
	if quote != 0 {
		return nil
	}
	if started {
		args = append(args, arg.String())
	}
	return args
}

func clientProcessPaths(args []string) (dir, config string, configSet, testOnly bool) {
	for i := 1; i < len(args); i++ {
		flag, value, assigned := strings.Cut(args[i], "=")
		switch flag {
		case "-t", "--test", "-v", "--version", "convert-ruleset":
			testOnly = true
		case "-d", "--dir", "--config-dir", "-f", "--config":
			if !assigned && i+1 < len(args) {
				i++
				value = args[i]
			}
			if flag == "-f" || flag == "--config" {
				config, configSet = value, true
			} else {
				dir = value
			}
		}
	}
	return
}

func discoverClientProcesses(processes []clientProcess) []ClientTarget {
	type candidate struct {
		target   ClientTarget
		endpoint string
		core     bool
	}
	var candidates []candidate
	add := func(kind, name, root, path string, args []string) bool {
		controller, err := clientRuntimeController(path, args)
		if err != nil {
			return false
		}
		target := ClientTarget{Kind: kind, Name: name, Root: root, RuntimeFile: path, Controller: controller}
		if len(args) > 0 {
			target.Executable = args[0]
		}
		candidates = append(candidates, candidate{target, clientControllerKey(*controller), len(args) != 0})
		return true
	}
	for _, process := range processes {
		args := processArguments(process.line)
		if len(args) == 0 || !clientProcessName(process.name) {
			continue
		}
		dir, config, configSet, testOnly := clientProcessPaths(args)
		if testOnly {
			continue
		}
		label := clientLabel(process.name, args[0])
		coreArgs := args
		if !clientCoreProcess(process.name) {
			coreArgs = nil
		}
		vergeFound := false
		vergeRuntime := coreArgs != nil && strings.EqualFold(filepath.Base(config), "clash-verge.yaml")
		if (!configSet && label == "Clash Verge Rev") || (configSet && vergeRuntime) {
			for _, root := range clientVergeRoots(dir, config, args[0]) {
				if configSet && clientPathKey(config) != clientPathKey(filepath.Join(root, "clash-verge.yaml")) {
					continue
				}
				if appconf.ValidateClashDir(root) == nil && add("verge", "Clash Verge Rev", root, filepath.Join(root, "clash-verge.yaml"), coreArgs) {
					vergeFound = true
				}
			}
		}
		if label == "Clash Verge Rev" && !vergeFound && !configSet {
			root := appconf.DefaultClashDir()
			if filepath.IsAbs(root) && appconf.ValidateClashDir(root) == nil {
				add("verge", label, root, filepath.Join(root, "clash-verge.yaml"), coreArgs)
			}
		}
		if coreArgs == nil {
			continue
		}
		if configSet {
			if filepath.IsAbs(config) {
				add("runtime", label, filepath.Dir(config), filepath.Clean(config), args)
			}
		} else if filepath.IsAbs(dir) {
			add("runtime", label, filepath.Clean(dir), filepath.Join(dir, "config.yaml"), args)
		} else if strings.Contains(strings.ToLower(process.name), "flclashcore") {
			// FlClash passes its IPC address to FlClashCore, not -f/-d. Its
			// persisted effective config is in the application's support home.
			if path := flClashRuntimeFile(); path != "" {
				add("runtime", "FlClash", filepath.Dir(path), path, args)
			}
		}
	}
	// Prefer Verge's profile integration and specific app labels when a GUI and
	// its child core describe the same runtime or local controller.
	sort.SliceStable(candidates, func(i, j int) bool {
		rank := func(t ClientTarget) int {
			if t.Kind == "verge" {
				return 0
			}
			if t.Name != "Mihomo" {
				return 1
			}
			return 2
		}
		return rank(candidates[i].target) < rank(candidates[j].target)
	})
	targets := []ClientTarget{}
	corePaths, endpoints := map[string]bool{}, map[string]bool{}
	for _, candidate := range candidates {
		if candidate.core {
			corePaths[clientPathKey(candidate.target.RuntimeFile)] = true
		}
	}
	for _, candidate := range candidates {
		key := clientPathKey(candidate.target.RuntimeFile)
		if (!candidate.core && corePaths[key]) || endpoints[candidate.endpoint] {
			continue
		}
		endpoints[candidate.endpoint] = true
		targets = append(targets, candidate.target)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].RuntimeFile < targets[j].RuntimeFile })
	return targets
}

func clientPathKey(path string) string {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

func clientVergeRoots(dir, config, executable string) []string {
	var roots []string
	if filepath.IsAbs(dir) {
		roots = append(roots, filepath.Clean(dir))
	}
	if filepath.IsAbs(config) {
		roots = append(roots, filepath.Dir(config))
	}
	if !filepath.IsAbs(executable) {
		return roots
	}
	base := filepath.Dir(executable)
	roots = append(roots, base)
	for _, parent := range []string{filepath.Join(base, ".config"), filepath.Join(base, "data")} {
		roots = append(roots, parent)
		// Portable Verge variants use one child directory. Bound the read;
		// discovery never traverses profiles or recursively searches user data.
		if f, err := os.Open(parent); err == nil {
			entries, _ := f.ReadDir(32)
			f.Close()
			for _, entry := range entries {
				if entry.IsDir() {
					roots = append(roots, filepath.Join(parent, entry.Name()))
				}
			}
		}
	}
	return roots
}

func flClashRuntimeFile() string {
	home, _ := os.UserHomeDir()
	var root string
	switch runtime.GOOS {
	case "windows":
		if data := os.Getenv("APPDATA"); filepath.IsAbs(data) {
			root = filepath.Join(data, "com.follow", "clash")
		}
	case "darwin":
		if home != "" {
			root = filepath.Join(home, "Library", "Application Support", "com.follow.clash")
		}
	default:
		data := os.Getenv("XDG_DATA_HOME")
		if data == "" && home != "" {
			data = filepath.Join(home, ".local", "share")
		}
		if filepath.IsAbs(data) {
			root = filepath.Join(data, "com.follow.clash")
		}
	}
	if root == "" {
		return ""
	}
	return filepath.Join(root, "config.yaml")
}

func clientRuntimeController(path string, args []string) (*ControlAPI, error) {
	invalid := errors.New("不是含本机控制接口的 Mihomo 运行配置")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 8*1024*1024 {
		return nil, invalid
	}
	b, err := fsx.ReadBounded(path, 8*1024*1024)
	if err != nil {
		return nil, invalid
	}
	var config struct {
		TCP       string    `yaml:"external-controller"`
		Unix      string    `yaml:"external-controller-unix"`
		Pipe      string    `yaml:"external-controller-pipe"`
		Secret    string    `yaml:"secret"`
		Proxies   yaml.Node `yaml:"proxies"`
		Providers yaml.Node `yaml:"proxy-providers"`
		Groups    yaml.Node `yaml:"proxy-groups"`
	}
	if yaml.Unmarshal(b, &config) != nil || (config.Proxies.Kind != yaml.SequenceNode && config.Providers.Kind != yaml.MappingNode && config.Groups.Kind != yaml.SequenceNode) {
		return nil, invalid
	}
	for i := 1; i < len(args); i++ {
		flag, value, assigned := strings.Cut(args[i], "=")
		var field *string
		switch flag {
		case "-ext-ctl":
			field = &config.TCP
		case "-ext-ctl-unix":
			field = &config.Unix
		case "-ext-ctl-pipe":
			field = &config.Pipe
		case "-secret":
			field = &config.Secret
		}
		if field == nil {
			continue
		}
		if !assigned && i+1 < len(args) {
			i++
			value = args[i]
		}
		*field = value
	}
	controller := &ControlAPI{Secret: config.Secret, Timeout: 5 * time.Second}
	if strings.HasPrefix(strings.ToLower(config.Pipe), `\\.\pipe\`) && len(config.Pipe) > len(`\\.\pipe\`) {
		controller.Network, controller.Address = "pipe", config.Pipe
		return controller, nil
	}
	if filepath.IsAbs(config.Unix) && !strings.HasPrefix(config.Unix, `\\`) {
		controller.Network, controller.Address = "unix", config.Unix
		return controller, nil
	}
	if loopbackTCP(config.TCP) {
		_, port, _ := net.SplitHostPort(config.TCP)
		if n, err := strconv.Atoi(port); err == nil && n > 0 && n <= 65535 {
			controller.Network, controller.Address = "tcp", config.TCP
			return controller, nil
		}
	}
	// Some clients pass their local controller only through core CLI flags.
	// Retry discovery only for a valid runtime with no configured endpoint;
	// malformed or explicitly unsafe endpoints remain terminal errors.
	if len(args) == 0 && config.TCP == "" && config.Unix == "" && config.Pipe == "" {
		return nil, ErrClientNotRunning
	}
	return nil, invalid
}

func clientControllerKey(controller ControlAPI) string {
	address := controller.Address
	switch controller.Network {
	case "pipe":
		address = strings.ToLower(address)
	case "unix":
		address = clientPathKey(address)
	case "tcp":
		host, port, _ := net.SplitHostPort(address)
		if host == "localhost" {
			host = "127.0.0.1"
		}
		address = net.JoinHostPort(host, port)
	}
	return controller.Network + ":" + address
}
