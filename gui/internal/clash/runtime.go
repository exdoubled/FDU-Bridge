package clash

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mythologyli/zju-connect/gui/internal/fsx"
	"gopkg.in/yaml.v3"
)

// ParseController accepts only explicit local controllers, never a remote subscription endpoint.
func ParseController(raw, secret string) (ControlAPI, error) {
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || !loopbackTCP(u.Host) {
		return ControlAPI{}, errors.New("控制接口需为本机 HTTP 地址，例如 http://127.0.0.1:9090")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return ControlAPI{}, errors.New("控制接口端口无效")
	}
	return ControlAPI{Network: "tcp", Address: u.Host, Secret: secret, Timeout: 5 * time.Second}, nil
}

// ManualTarget validates source structure even when a client keeps its API settings separately.
func ManualTarget(file, address, secret string) (ClientTarget, error) {
	path, err := filepath.Abs(file)
	if err != nil || file == "" {
		return ClientTarget{}, errors.New("请选择运行配置文件")
	}
	args := []string{"mihomo"}
	if address != "" {
		controller, parseErr := ParseController(address, secret)
		if parseErr != nil {
			return ClientTarget{}, parseErr
		}
		args = append(args, "-ext-ctl-pipe=", "-ext-ctl-unix=", "-ext-ctl", controller.Address)
	}
	if secret != "" {
		args = append(args, "-secret", secret)
	}
	controller, err := clientRuntimeController(path, args)
	if err != nil {
		return ClientTarget{}, err
	}
	return ClientTarget{Kind: "runtime", Name: clientLabel("", path), Root: filepath.Dir(path), RuntimeFile: path, Controller: controller}, nil
}

func (c *Connection) ConfigureTarget(file string, controller *ControlAPI) {
	c.targetMu.Lock()
	c.runtimeFile, c.controller = file, controller
	c.previousRuntime = ""
	c.targetMu.Unlock()
}

// ConfigureFollowingTarget is used only after identifying the same running client.
func (c *Connection) ConfigureFollowingTarget(file string, controller *ControlAPI) {
	c.targetMu.Lock()
	if file != c.runtimeFile && c.previousRuntime == "" {
		c.previousRuntime = c.runtimeFile
	}
	c.runtimeFile, c.controller = file, controller
	c.targetMu.Unlock()
}

func (c *Connection) RuntimePath() string {
	c.targetMu.RLock()
	defer c.targetMu.RUnlock()
	return c.runtimeFile
}

// ControllerBinding snapshots the selected target without discovery or mutation.
func (c *Connection) ControllerBinding() (string, ControlAPI) {
	c.targetMu.RLock()
	defer c.targetMu.RUnlock()
	var controller ControlAPI
	if c.controller != nil {
		controller = *c.controller
	}
	return c.runtimeFile, controller
}

// Controller returns the selected controller without making any mutation request.
func (c *Connection) Controller(root string) API { api, _ := c.api(root); return api }

func DiscoverRuntime(file string) (API, error) {
	controller, err := clientRuntimeController(file, nil)
	if err != nil {
		return nil, err
	}
	if err := probeController(*controller); err != nil {
		return nil, controllerFailure(err)
	} else {
		return *controller, nil
	}
}

func runtimeOverrides(live map[string]any, api API) map[string]any {
	result := map[string]any{}
	for _, key := range []string{"mode", "mixed-port", "port", "socks-port", "redir-port", "tproxy-port", "allow-lan", "bind-address", "log-level", "ipv6", "tun"} {
		if value, ok := live[key]; ok {
			result[key] = value
		}
	}
	if controller, ok := api.(ControlAPI); ok {
		switch controller.Network {
		case "pipe":
			result["external-controller-pipe"] = controller.Address
		case "unix":
			result["external-controller-unix"] = controller.Address
		case "tcp":
			result["external-controller"] = controller.Address
		}
		result["secret"] = controller.Secret
	}
	return result
}

// deactivateRuntime restores the latest client-owned source, never a stale subscription snapshot.
func (c *Connection) deactivateRuntime(a CampusAttachment) (CampusAttachment, error) {
	if _, err := campusProviderBytes(a); err != nil {
		return a, err
	}
	c.targetMu.RLock()
	migrating := c.runtimeFile != a.RuntimeFile && c.previousRuntime == a.RuntimeFile
	c.targetMu.RUnlock()
	if migrating {
		api, err := c.api(a.Root)
		if err != nil {
			return a, err
		}
		var response proxyResponse
		if err = api.Do("GET", "/proxies", nil, &response); err != nil {
			return a, err
		}
		if response.Proxies["FDU-ROUTER"] != nil {
			return a, ErrClashChanging
		}
		next, err := replaceCampusProvider(nil, a, []byte("payload: []\n"), false)
		if err == nil {
			next.Active, next.RulesHash = false, ""
		}
		return next, err
	}
	api, err := c.campusAPI(a)
	if err != nil {
		return a, err
	}
	var response proxyResponse
	var live map[string]any
	if err = api.Do("GET", "/proxies", nil, &response); err != nil {
		return a, err
	}
	if response.Proxies["FDU-ROUTER"] != nil {
		if err = api.Do("GET", "/configs", nil, &live); err != nil {
			return a, err
		}
		raw, readErr := fsx.ReadBounded(a.RuntimeFile, 8*1024*1024)
		if readErr != nil {
			return a, fmt.Errorf("无法读取客户端的最新运行配置，已保留连接：%w", readErr)
		}
		var original map[string]any
		if yaml.Unmarshal(raw, &original) != nil || original == nil {
			return a, errors.New("客户端运行配置无效，未恢复旧副本")
		}
		if containsRuntimeFDU(original) {
			// An explicitly installed extension stays in the client's source. Empty
			// its owned feed and restore GLOBAL instead of rewriting that source.
			if fsx.Hash(raw) != a.RuntimeHash {
				return a, ErrClashChanging
			}
			if err = restoreCampusExit(api, &a, response.Proxies); err != nil {
				return a, err
			}
			next, err := replaceCampusProvider(api, a, []byte("payload: []\n"), true)
			if err == nil {
				next.Active, next.RulesHash = false, ""
			}
			return next, err
		}
		mode := text(live["mode"])
		for key, value := range runtimeOverrides(live, api) {
			original[key] = value
		}
		outside := a.Outside
		if internet := response.Proxies["INTERNET"]; internet != nil && SafeCampusTarget(response.Proxies, internet.Now, map[string]bool{}, UnsafeSet(Plan{UnsafeNodes: a.UnsafeNodes})) {
			outside = internet.Now
		}
		if global := response.Proxies["GLOBAL"]; global != nil && global.Now != "FDU-ROUTER" {
			outside = global.Now
		}
		payload, marshalErr := yaml.Marshal(original)
		if marshalErr != nil {
			return a, marshalErr
		}
		if fsx.HashFile(a.RuntimeFile) != fsx.Hash(raw) {
			return a, ErrClashChanging
		}
		var before map[string]any
		if err = api.Do("GET", "/configs", nil, &before); err != nil {
			return a, err
		}
		if text(before["mode"]) != mode {
			return a, ErrClashChanging
		}
		if err = api.Do("PUT", "/configs?force=true", map[string]string{"payload": string(payload)}, nil); err != nil {
			return a, err
		}
		var after proxyResponse
		if err = api.Do("GET", "/proxies", nil, &after); err != nil {
			return a, err
		}
		if after.Proxies["FDU-ROUTER"] != nil {
			return a, errors.New("客户端未确认移除 FDU 运行配置")
		}
		if global := after.Proxies["GLOBAL"]; global != nil && contains(global.All, outside) && SafeCampusTarget(after.Proxies, outside, map[string]bool{}, nil) {
			if err = api.Do("PUT", "/proxies/GLOBAL", map[string]string{"name": outside}, nil); err != nil {
				return a, err
			}
		}
	}
	// The restored configuration no longer references this feed; only our own file is cleared.
	next, err := replaceCampusProvider(nil, a, []byte("payload: []\n"), false)
	if err == nil {
		next.Active, next.RulesHash = false, ""
	}
	return next, err
}

func containsRuntimeFDU(config map[string]any) bool {
	items, _ := config["proxies"].([]any)
	for _, item := range items {
		if node, ok := item.(map[string]any); ok && node["name"] == "FDU-ROUTER" {
			return true
		}
	}
	return false
}

// LocalProxyAddress is shared by the manual setup preview and the standalone status.
func LocalProxyAddress(value string) bool {
	host, raw, err := net.SplitHostPort(value)
	port, e := strconv.Atoi(raw)
	ip := net.ParseIP(host)
	return err == nil && e == nil && port > 0 && port <= 65535 && ip != nil && ip.IsLoopback()
}
