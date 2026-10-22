package clash

import (
	"bufio"
	"errors"
	"os/exec"
	"strings"
)

// ErrProxyUnsupported means this desktop has no system proxy setting we know how to change;
// mode switches still proceed and the user sets the proxy by hand.
var ErrProxyUnsupported = errors.New("无法自动设置系统代理")

func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return "", errors.New(strings.TrimSpace(name + " " + strings.Join(args, " ") + "：" + strings.TrimSpace(string(out))))
	}
	return string(out), nil
}

// parseNetworkServices reads `networksetup -listallnetworkservices`, skipping its header
// and disabled services (marked with *).
func parseNetworkServices(out string) []string {
	var services []string
	scanner := bufio.NewScanner(strings.NewReader(out))
	for first := true; scanner.Scan(); first = false {
		line := strings.TrimSpace(scanner.Text())
		if first && strings.Contains(line, "asterisk") {
			continue
		}
		if line != "" && !strings.HasPrefix(line, "*") {
			services = append(services, line)
		}
	}
	return services
}

// webProxy is one networksetup -get(secure)webproxy result.
type webProxy struct {
	Enabled bool
	Server  string
	Port    string
}

func parseWebProxy(out string) webProxy {
	var p webProxy
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Enabled":
			p.Enabled = strings.EqualFold(strings.TrimSpace(value), "Yes")
		case "Server":
			p.Server = strings.TrimSpace(value)
		case "Port":
			p.Port = strings.TrimSpace(value)
		}
	}
	return p
}

// unquoteGSettings turns gsettings output such as 'manual' or uint32 0 into a plain value.
func unquoteGSettings(out string) string {
	value := strings.TrimSpace(out)
	value = strings.TrimPrefix(value, "uint32 ")
	return strings.Trim(value, "'")
}
