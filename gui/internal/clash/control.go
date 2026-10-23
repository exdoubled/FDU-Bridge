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
	"strings"
	"time"

	"github.com/mythologyli/zju-connect/gui/internal/core"
	"gopkg.in/yaml.v3"
)

const responseLimit = 4 * 1024 * 1024

var ErrControllerUnavailable = errors.New("Clash 控制接口不可用")

func controllerFailure(err error) error {
	if controllerMissing(err) {
		return fmt.Errorf("%w：%w", ErrControllerUnavailable, err)
	}
	return fmt.Errorf("无法连接 Clash 控制接口：%w", err)
}

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
		return path == "/configs" || path == "/proxies" || path == "/version" || diagnosticRead(path)
	case http.MethodPut:
		return (strings.HasPrefix(path, "/proxies/") && !strings.ContainsAny(strings.TrimPrefix(path, "/proxies/"), "/?#") && len(path) > len("/proxies/")) || path == "/configs?force=true" || path == "/providers/rules/FDU-CAMPUS"
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
			return fmt.Errorf("Clash 控制接口响应超时，请稍后刷新：%w", err)
		}
		return controllerFailure(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil {
		return fmt.Errorf("Clash 控制接口返回了不完整的数据，请刷新：%w", err)
	}
	if len(data) > responseLimit {
		return errors.New("Clash 返回的数据过大，已停止读取")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		context := fmt.Sprintf("Clash 控制接口拒绝 %s %s（HTTP %d）", method, path, response.StatusCode)
		var reply struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &reply) == nil && reply.Message != "" {
			secrets := []string{}
			if c.Secret != "" {
				secrets = append(secrets, c.Secret)
			}
			if request, ok := payload.(map[string]string); ok && request["payload"] != "" {
				var config any
				if yaml.Unmarshal([]byte(request["payload"]), &config) == nil {
					var collect func(any)
					collect = func(value any) {
						switch v := value.(type) {
						case map[string]any:
							for key, item := range v {
								if text, ok := item.(string); ok && text != "" {
									for _, word := range []string{"password", "passwd", "secret", "token", "uuid", "username", "url"} {
										if strings.Contains(strings.ToLower(key), word) {
											secrets = append(secrets, text)
											break
										}
									}
								}
								collect(item)
							}
						case []any:
							for _, item := range v {
								collect(item)
							}
						}
					}
					collect(config)
				}
			}
			detail := []rune(core.Redact(reply.Message, secrets))
			if len(detail) > 800 {
				detail = append(detail[:800], '…')
			}
			return fmt.Errorf("%s：%s", context, string(detail))
		}
		return errors.New(context)
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

func probeController(api ControlAPI) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := api.dial(ctx)
	if err == nil {
		conn.Close()
	}
	return err
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
	var uncertain error
	for _, c := range candidates {
		probeErr := probeController(c)
		if probeErr == nil {
			c.Timeout = 5 * time.Second
			return c, nil
		}
		if !controllerMissing(probeErr) {
			uncertain = probeErr
		}
	}
	if err != nil {
		return nil, err
	}
	if uncertain != nil {
		return nil, controllerFailure(uncertain)
	}
	return nil, fmt.Errorf("%w：未找到正在运行的 Clash Verge 控制接口", ErrControllerUnavailable)
}
