package services

import (
	"errors"
	"net"
	"net/url"
	"strconv"

	"github.com/mythologyli/zju-connect/gui/internal/sysproc"
)

// Allocate only before a new connection; refreshing a running session must keep its ports.
func availableProxyPorts(values map[string]any) (map[string]any, error) {
	result := make(map[string]any, len(values))
	for key, value := range values {
		result[key] = value
	}
	used := map[int]bool{}
	for _, port := range sysproc.UsedPorts(1, 65536) {
		used[port] = true
	}
	if route, ok := values["routing"].(map[string]any); ok {
		if upstream, _ := url.Parse(text(route, "upstream_proxy")); upstream != nil {
			port, _ := strconv.Atoi(upstream.Port())
			used[port] = true
		}
	}
	reserved := map[int]bool{}
	for _, key := range []string{"socks_bind", "http_bind"} {
		_, raw, _ := net.SplitHostPort(text(values, key))
		port, _ := strconv.Atoi(raw)
		reserved[port] = true
	}
	for index, key := range []string{"socks_bind", "http_bind"} {
		bind := text(values, key)
		if bind == "" {
			bind = net.JoinHostPort("127.0.0.1", strconv.Itoa(1080+index))
		}
		host, raw, err := net.SplitHostPort(bind)
		port, parseErr := strconv.Atoi(raw)
		if err != nil || parseErr != nil || host != "127.0.0.1" || port < 1 || port > 65535 {
			continue
		}
		if used[port] {
			port = 1080
			for port <= 65535 && (used[port] || reserved[port]) {
				port++
			}
			if port > 65535 {
				return nil, errors.New("找不到可用的 FDU 本地代理端口")
			}
		}
		used[port] = true
		result[key] = net.JoinHostPort(host, strconv.Itoa(port))
	}
	return result, nil
}
