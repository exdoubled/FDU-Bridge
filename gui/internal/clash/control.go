// Package clash drives Clash Verge through its local control interface with an explicit allowlist.
package clash

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

const responseLimit = 4 * 1024 * 1024

// API is the only path to the controller, so tests can substitute a fake.
type API interface {
	Do(method, path string, payload, out any) error
}

// ControlAPI speaks the mihomo REST API over a named pipe ("pipe"), a Unix socket ("unix")
// or a loopback TCP controller ("tcp", which needs the secret).
type ControlAPI struct {
	Network string
	Address string
	Secret  string
	Timeout time.Duration
}

func allowed(method, path string) bool {
	switch method {
	case http.MethodGet:
		return path == "/configs" || path == "/proxies"
	case http.MethodPut:
		return path == "/proxies/INTERNET" || path == "/proxies/GLOBAL" || path == "/configs?force=true" || path == "/providers/rules/FDU-CAMPUS"
	}
	return false
}

func (c ControlAPI) dial(ctx context.Context) (net.Conn, error) {
	switch c.Network {
	case "pipe":
		return dialPipe(ctx, c.Address)
	case "unix":
		var d net.Dialer
		return d.DialContext(ctx, "unix", c.Address)
	case "tcp":
		var d net.Dialer
		return d.DialContext(ctx, "tcp", c.Address)
	}
	return nil, fmt.Errorf("unknown controller network %q", c.Network)
}

func (c ControlAPI) Do(method, path string, payload, out any) error {
	if !allowed(method, path) {
		return errors.New("不支持的 Clash 控制操作")
	}
	body := []byte{}
	if payload != nil {
		var err error
		if body, err = json.Marshal(payload); err != nil {
			return err
		}
	}
	client := &http.Client{Timeout: c.Timeout, Transport: &http.Transport{
		DisableKeepAlives: true,
		Proxy:             nil,
		DialContext:       func(ctx context.Context, _, _ string) (net.Conn, error) { return c.dial(ctx) },
	}}
	host := "localhost"
	if c.Network == "tcp" {
		host = c.Address
	}
	request, err := http.NewRequest(method, "http://"+host+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if c.Secret != "" {
		request.Header.Set("Authorization", "Bearer "+c.Secret)
	}
	response, err := client.Do(request)
	if err != nil {
		var timeout interface{ Timeout() bool }
		if errors.As(err, &timeout) && timeout.Timeout() {
			return errors.New("Clash 控制接口响应超时，请稍后刷新")
		}
		return fmt.Errorf("无法连接 Clash 控制接口：%w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil {
		return errors.New("Clash 控制接口返回了不完整的数据，请刷新")
	}
	if len(data) > responseLimit {
		return errors.New("Clash 返回的数据过大，已停止读取")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Clash 控制接口拒绝请求（HTTP %d）", response.StatusCode)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return errors.New("Clash 控制接口返回格式异常")
		}
	}
	return nil
}

// runtimeController is the part of clash-verge.yaml, the config Verge hands to mihomo,
// that says where the running core listens for control requests.
type runtimeController struct {
	Pipe   string `yaml:"external-controller-pipe"`
	Unix   string `yaml:"external-controller-unix"`
	TCP    string `yaml:"external-controller"`
	Secret string `yaml:"secret"`
}

func readRuntimeController(clashDir string) (runtimeController, error) {
	var rc runtimeController
	b, err := os.ReadFile(filepath.Join(clashDir, "clash-verge.yaml"))
	if err == nil {
		err = yaml.Unmarshal(b, &rc)
	}
	return rc, err
}

func loopbackTCP(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}

func reachable(api ControlAPI) bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := api.dial(ctx)
	if err == nil {
		conn.Close()
	}
	return err == nil
}

// Discover prefers the address the running Verge gave mihomo, then the defaults
// Verge versions have used, then a loopback TCP controller if the user enabled one.
func Discover(clashDir string) (API, error) {
	var candidates []ControlAPI
	rc, _ := readRuntimeController(clashDir)
	if rc.Pipe != "" {
		candidates = append(candidates, ControlAPI{Network: "pipe", Address: rc.Pipe})
	}
	if rc.Unix != "" {
		candidates = append(candidates, ControlAPI{Network: "unix", Address: rc.Unix})
	}
	defaults, err := defaultControllers(clashDir)
	candidates = append(candidates, defaults...)
	if loopbackTCP(rc.TCP) {
		candidates = append(candidates, ControlAPI{Network: "tcp", Address: rc.TCP, Secret: rc.Secret})
	}
	for _, c := range candidates {
		if reachable(c) {
			c.Timeout = 5 * time.Second
			return c, nil
		}
	}
	if err != nil {
		return nil, err
	}
	return nil, errors.New("未找到正在运行的 Clash Verge 控制接口，请确认 Clash 已打开后刷新")
}
