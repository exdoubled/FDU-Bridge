package netcheck

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/mythologyli/zju-connect/routing"
)

// NormalizeCampusURL keeps the explicitly requested path, but drops query and
// fragment data. Userinfo is rejected rather than silently using credentials.
func NormalizeCampusURL(input string) (Target, error) {
	input = strings.TrimSpace(input)
	invalid := errors.New("请输入 HTTP/HTTPS 网址或域名，不含账号密码；端口须为 1–65535")
	if input == "" || len(input) > 4096 || strings.ContainsAny(input, "\\\r\n\t") {
		return Target{}, invalid
	}
	if !strings.Contains(input, "://") {
		if strings.HasPrefix(input, "/") {
			return Target{}, invalid
		}
		input = "https://" + input
	}
	u, err := url.Parse(input)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return Target{}, invalid
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if net.ParseIP(host) == nil {
		_, err = (routing.RuleSet{Version: 1, Domains: []string{host}, DomainSuffixes: []string{}, IPCIDRs: []string{}}).Canonical()
		if err != nil {
			return Target{}, invalid
		}
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return Target{}, invalid
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return Target{}, invalid
	}
	u.Host = host
	if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	}
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	}
	u.RawQuery, u.Fragment, u.RawFragment, u.ForceQuery = "", "", "", false
	if u.Path == "" {
		u.Path = "/"
	}
	return Target{ID: "campus-url", Name: "校园网址", Host: host, Provider: "campus", URL: u.String()}, nil
}

// ProbeCampus probes only the sanitized URL, with a fresh client and no cookie jar.
func ProbeCampus(ctx context.Context, input string, opts Options) (Result, error) {
	target, err := NormalizeCampusURL(input)
	if err != nil {
		return Result{}, err
	}
	transport, err := newTransport(opts.ProxyURL)
	if err != nil {
		return Result{}, err
	}
	defer transport.CloseIdleConnections()
	return probeTarget(ctx, target, opts, transport), nil
}
