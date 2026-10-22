package clash

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

func systemProxyBypass(port int) ([]string, bool, error) {
	snapshot, err := CaptureProxy()
	if err != nil {
		return nil, false, err
	}
	var patterns []string
	for service := range snapshot.services {
		if out, err := run("networksetup", "-getautoproxyurl", service); err != nil {
			return nil, false, err
		} else if strings.Contains(out, "Enabled: Yes") {
			return nil, false, errors.New("当前使用 PAC，实际路径由 PAC 决定")
		}
		out, err := run("networksetup", "-getproxybypassdomains", service)
		if err != nil {
			return nil, false, err
		}
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.Contains(line, " ") {
				patterns = append(patterns, line)
			}
		}
	}
	return patterns, snapshot.pointsAt(port), nil
}

// ProxySnapshot keeps the HTTP and HTTPS proxy of every enabled network service.
type ProxySnapshot struct {
	services map[string][2]webProxy
}

func (s ProxySnapshot) pointsAt(port int) bool {
	for _, proxies := range s.services {
		for _, proxy := range proxies {
			if proxy.Enabled && proxy.Server == "127.0.0.1" && proxy.Port == strconv.Itoa(port) {
				return true
			}
		}
	}
	return false
}

func networkServices() ([]string, error) {
	out, err := run("networksetup", "-listallnetworkservices")
	if err != nil {
		return nil, err
	}
	return parseNetworkServices(out), nil
}

func CaptureProxy() (ProxySnapshot, error) {
	services, err := networkServices()
	if err != nil {
		return ProxySnapshot{}, err
	}
	snapshot := ProxySnapshot{services: map[string][2]webProxy{}}
	for _, service := range services {
		web, err := run("networksetup", "-getwebproxy", service)
		if err != nil {
			return ProxySnapshot{}, err
		}
		secure, err := run("networksetup", "-getsecurewebproxy", service)
		if err != nil {
			return ProxySnapshot{}, err
		}
		snapshot.services[service] = [2]webProxy{parseWebProxy(web), parseWebProxy(secure)}
	}
	return snapshot, nil
}

func setService(service string, proxies [2]webProxy) error {
	for i, kind := range []string{"webproxy", "securewebproxy"} {
		p := proxies[i]
		if p.Server != "" && p.Port != "" && p.Port != "0" {
			if _, err := run("networksetup", "-set"+kind, service, p.Server, p.Port); err != nil {
				return err
			}
		}
		state := "off"
		if p.Enabled {
			state = "on"
		}
		if _, err := run("networksetup", "-set"+kind+"state", service, state); err != nil {
			return err
		}
	}
	return nil
}

func RestoreProxy(s ProxySnapshot) error {
	var failures []string
	for service, proxies := range s.services {
		if err := setService(service, proxies); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "\n"))
	}
	return nil
}

func ProxyDescription() string {
	services, err := networkServices()
	if err != nil {
		return "系统代理：未知"
	}
	for _, service := range services {
		if out, err := run("networksetup", "-getwebproxy", service); err == nil {
			if p := parseWebProxy(out); p.Enabled {
				return fmt.Sprintf("系统代理：%s:%s（%s）", p.Server, p.Port, service)
			}
		}
	}
	return "系统代理：未设置"
}

// SetProxy points HTTP and HTTPS of every enabled network service at 127.0.0.1:port.
func SetProxy(port int) error {
	services, err := networkServices()
	if err != nil {
		return err
	}
	for _, service := range services {
		if out, err := run("networksetup", "-getautoproxyurl", service); err == nil && strings.Contains(out, "Enabled: Yes") {
			return fmt.Errorf("“%s”启用了 PAC 自动配置，请先在系统设置中关闭，再应用连接模式", service)
		}
	}
	before, err := CaptureProxy()
	if err != nil {
		return err
	}
	target := webProxy{Enabled: true, Server: "127.0.0.1", Port: strconv.Itoa(port)}
	for _, service := range services {
		if err := setService(service, [2]webProxy{target, target}); err != nil {
			if restore := RestoreProxy(before); restore != nil {
				return fmt.Errorf("%v\n恢复未确认：%v", err, restore)
			}
			return err
		}
	}
	return nil
}
