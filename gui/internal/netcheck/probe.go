// Package netcheck provides credential-free diagnostics for a fixed target list.
package netcheck

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"sync"
	"time"
)

type Target struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Host     string `json:"host"`
	Provider string `json:"provider"`
	URL      string `json:"url"`
}

type Check struct {
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Millis int64  `json:"millis"`
	Detail string `json:"detail"`
}

type Result struct {
	TargetID   string   `json:"targetId"`
	Name       string   `json:"name"`
	Host       string   `json:"host"`
	Provider   string   `json:"provider"`
	CheckedAt  int64    `json:"checkedAt"`
	Answers    []string `json:"answers"`
	Checks     []Check  `json:"checks"`
	HTTPStatus int      `json:"httpStatus"`
}

type Options struct {
	ProxyURL string
	Lookup   func(context.Context, string) ([]string, error)
}

// Targets returns a copy so callers cannot modify the probe allowlist.
func Targets() []Target {
	return []Target{
		{ID: "library", Name: "复旦图书馆", Host: "library.fudan.edu.cn", Provider: "campus", URL: "https://library.fudan.edu.cn/"},
		{ID: "icourse", Name: "复旦课程平台", Host: "icourse.fudan.edu.cn", Provider: "campus", URL: "https://icourse.fudan.edu.cn/"},
	}
}

// Probe accepts target IDs only. Network failures are reported in Checks.
func Probe(ctx context.Context, id string, opts Options) (Result, error) {
	var target Target
	for _, candidate := range Targets() {
		if candidate.ID == id {
			target = candidate
			break
		}
	}
	if target.ID == "" {
		return Result{}, errors.New("未知诊断目标")
	}
	transport, err := newTransport(opts.ProxyURL)
	if err != nil {
		return Result{}, err
	}
	defer transport.CloseIdleConnections()
	return probeTarget(ctx, target, opts, transport), nil
}

func newTransport(proxy string) (*http.Transport, error) {
	transport := &http.Transport{
		DialContext:            (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:    4 * time.Second,
		ResponseHeaderTimeout:  4 * time.Second,
		DisableKeepAlives:      true,
		MaxResponseHeaderBytes: 32 << 10,
	}
	if proxy == "" {
		return transport, nil // Deliberately do not inherit environment proxies.
	}
	u, err := url.Parse(proxy)
	if err != nil {
		return nil, errors.New("代理仅支持含端口的回环 HTTP IP 地址")
	}
	ip := net.ParseIP(u.Hostname())
	port, portErr := strconv.Atoi(u.Port())
	if u.Scheme != "http" || ip == nil || !ip.IsLoopback() || portErr != nil || port < 1 || port > 65535 || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("代理仅支持含端口的回环 HTTP IP 地址")
	}
	transport.Proxy = http.ProxyURL(u)
	return transport, nil
}

func probeClient(transport *http.Transport) *http.Client {
	return &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func probeTarget(ctx context.Context, target Target, opts Options, transport *http.Transport) Result {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	result := Result{TargetID: target.ID, Name: target.Name, Host: target.Host, Provider: target.Provider, CheckedAt: time.Now().UnixMilli(), Answers: []string{}, Checks: []Check{}}
	lookup := opts.Lookup
	source := "指定 DNS"
	if lookup == nil {
		lookup = net.DefaultResolver.LookupHost
		source = "系统 DNS"
	}
	dnsCtx, dnsCancel := context.WithTimeout(ctx, 3*time.Second)
	start := time.Now()
	answers, dnsErr := lookup(dnsCtx, target.Host)
	dnsCancel()
	for _, answer := range answers {
		if ip := net.ParseIP(answer); ip != nil && len(result.Answers) < 16 {
			value := ip.String()
			duplicate := false
			for _, existing := range result.Answers {
				duplicate = duplicate || existing == value
			}
			if !duplicate {
				result.Answers = append(result.Answers, value)
			}
		}
	}
	dns := Check{Kind: "dns", Status: "ok", Millis: time.Since(start).Milliseconds(), Detail: source + "；IP 仅为候选，非实际连接地址"}
	if opts.ProxyURL != "" {
		dns.Detail += "；代理 DNS 可能不同"
	}
	if dnsErr != nil || len(result.Answers) == 0 {
		dns.Status = "error"
		dns.Detail += "；解析失败或无 IP"
	}
	result.Checks = append(result.Checks, dns)

	client := probeClient(transport)
	httpCtx, httpCancel := context.WithTimeout(ctx, 8*time.Second)
	trace := &connectionTrace{starts: make(map[string]time.Time)}
	req, _ := http.NewRequestWithContext(httptrace.WithClientTrace(httpCtx, trace.trace()), http.MethodGet, target.URL, nil)
	start = time.Now()
	response, requestErr := client.Do(req)
	elapsed := time.Since(start).Milliseconds()
	httpCancel()
	result.Checks = append(result.Checks, trace.checks(opts.ProxyURL != "")...)
	check := Check{Kind: "http", Status: "error", Millis: elapsed, Detail: "HTTP 请求失败：" + errorDetail(requestErr)}
	if response != nil {
		result.HTTPStatus = response.StatusCode
		check.Status, check.Detail = responseDetail(response)
		response.Body.Close()
	}
	result.Checks = append(result.Checks, check)

	return result
}

type connectionTrace struct {
	mu       sync.Mutex
	starts   map[string]time.Time
	tcp, tls Check
	tlsStart time.Time
}

func (t *connectionTrace) trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		ConnectStart: func(network, address string) {
			t.mu.Lock()
			defer t.mu.Unlock()
			t.starts[network+address] = time.Now()
		},
		ConnectDone: func(network, address string, err error) {
			t.mu.Lock()
			defer t.mu.Unlock()
			if t.tcp.Status == "ok" {
				return
			}
			t.tcp = Check{Kind: "tcp", Status: "ok", Millis: time.Since(t.starts[network+address]).Milliseconds()}
			if err != nil {
				t.tcp.Status, t.tcp.Detail = "error", errorDetail(err)
			}
		},
		TLSHandshakeStart: func() { t.mu.Lock(); defer t.mu.Unlock(); t.tlsStart = time.Now() },
		TLSHandshakeDone: func(state tls.ConnectionState, err error) {
			t.mu.Lock()
			defer t.mu.Unlock()
			t.tls = Check{Kind: "tls", Status: "ok", Millis: time.Since(t.tlsStart).Milliseconds(), Detail: tls.VersionName(state.Version) + "；证书已验证"}
			if err != nil {
				t.tls.Status, t.tls.Detail = "error", "TLS 握手失败："+errorDetail(err)
			}
		},
	}
}

func (t *connectionTrace) checks(proxy bool) []Check {
	t.mu.Lock()
	defer t.mu.Unlock()
	tcp, tlsCheck := t.tcp, t.tls
	if tcp.Kind == "" {
		tcp = Check{Kind: "tcp", Status: "unsupported", Detail: "尚未进入 TCP 阶段"}
	}
	if tlsCheck.Kind == "" {
		tlsCheck = Check{Kind: "tls", Status: "unsupported", Detail: "尚未进入 TLS 阶段"}
	}
	label := "目标 TCP；DNS IP 仅为候选"
	if proxy {
		label = "本机 HTTP 代理入口 TCP；未观测目标 IP"
	}
	if tcp.Detail != "" {
		label += "；" + tcp.Detail
	}
	tcp.Detail = label
	return []Check{tcp, tlsCheck}
}

func responseDetail(response *http.Response) (string, string) {
	code := response.StatusCode
	detail := fmt.Sprintf("HTTP %d，已响应（请求总耗时）", code)
	if code == http.StatusUnauthorized {
		return "auth", detail + "；需要认证"
	}
	if code == http.StatusForbidden {
		return "auth", detail + "；认证、访问策略或地区限制可能拒绝访问"
	}
	if code >= 300 && code < 400 {
		detail += "；未跟随跳转"
		if destination, err := response.Location(); err == nil {
			host := destination.Hostname()
			if len(host) > 128 {
				host = host[:128]
			}
			if host != "" {
				detail += "；跳转域名：" + host
			}
		}
	}
	if code >= 200 && code < 400 {
		return "ok", detail
	}
	return "error", detail
}

func errorDetail(err error) string {
	if err == nil {
		return "未收到响应"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "请求超时"
	}
	if errors.Is(err, context.Canceled) {
		return "请求已取消"
	}
	var certificateErr *tls.CertificateVerificationError
	if errors.As(err, &certificateErr) {
		return "TLS 证书验证失败"
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) && networkErr.Timeout() {
		return "网络操作超时"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "DNS 解析失败"
	}
	var operationErr *net.OpError
	if errors.As(err, &operationErr) {
		switch operationErr.Op {
		case "dial":
			return "TCP 连接失败"
		case "read":
			return "网络读取失败"
		case "write":
			return "网络写入失败"
		}
	}
	return "网络请求失败"
}
