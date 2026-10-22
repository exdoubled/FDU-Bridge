package clash

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const internetSettings = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

var procInternetSetOption = windows.NewLazySystemDLL("wininet.dll").NewProc("InternetSetOptionW")

// notify asks WinINet clients to reload settings (SETTINGS_CHANGED, then REFRESH).
func notify() bool {
	a, _, _ := procInternetSetOption.Call(0, 39, 0, 0)
	b, _, _ := procInternetSetOption.Call(0, 37, 0, 0)
	return a != 0 && b != 0
}

// ProxySnapshot keeps nil for values that did not exist, so restore can delete them again.
type ProxySnapshot struct {
	Enable *uint32
	Server *string
}

func (s ProxySnapshot) pointsAt(port int) bool {
	if s.Enable == nil || *s.Enable == 0 || s.Server == nil {
		return false
	}
	address := fmt.Sprintf("127.0.0.1:%d", port)
	for _, part := range strings.FieldsFunc(*s.Server, func(r rune) bool { return r == ';' || r == '=' || r == ' ' }) {
		if part == address || strings.TrimPrefix(part, "http://") == address {
			return true
		}
	}
	return false
}

func CaptureProxy() (ProxySnapshot, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, internetSettings, registry.QUERY_VALUE)
	if err != nil {
		return ProxySnapshot{}, err
	}
	defer key.Close()
	var snapshot ProxySnapshot
	if v, _, err := key.GetIntegerValue("ProxyEnable"); err == nil {
		n := uint32(v)
		snapshot.Enable = &n
	}
	if v, _, err := key.GetStringValue("ProxyServer"); err == nil {
		snapshot.Server = &v
	}
	return snapshot, nil
}

func restoreValues(key registry.Key, s ProxySnapshot) {
	if s.Server == nil {
		_ = key.DeleteValue("ProxyServer")
	} else {
		_ = key.SetStringValue("ProxyServer", *s.Server)
	}
	if s.Enable == nil {
		_ = key.DeleteValue("ProxyEnable")
	} else {
		_ = key.SetDWordValue("ProxyEnable", *s.Enable)
	}
}

func RestoreProxy(s ProxySnapshot) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, internetSettings, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	restoreValues(key, s)
	notify()
	return nil
}

// ProxyDescription summarizes the current system proxy for previews.
func ProxyDescription() string {
	server, bypass := "未设置", "无"
	if key, err := registry.OpenKey(registry.CURRENT_USER, internetSettings, registry.QUERY_VALUE); err == nil {
		if v, _, err := key.GetStringValue("ProxyServer"); err == nil && v != "" {
			server = v
		}
		if v, _, err := key.GetStringValue("ProxyOverride"); err == nil && v != "" {
			bypass = v
		}
		key.Close()
	}
	return fmt.Sprintf("系统代理：%s；保留现有绕过列表：%s", server, bypass)
}

// SetProxy points the system proxy at 127.0.0.1:port and keeps the bypass list untouched.
func SetProxy(port int) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, internetSettings, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if pac, _, err := key.GetStringValue("AutoConfigURL"); err == nil && pac != "" {
		return errors.New("当前启用了 PAC 自动配置，请先自行关闭 PAC，再应用连接模式")
	}
	before, err := CaptureProxy()
	if err != nil {
		return err
	}
	err = key.SetStringValue("ProxyServer", fmt.Sprintf("127.0.0.1:%d", port))
	if err == nil {
		err = key.SetDWordValue("ProxyEnable", 1)
	}
	if err == nil && !notify() {
		err = errors.New("Windows 未确认系统代理更新")
	}
	if err != nil {
		restoreValues(key, before)
		notify()
	}
	return err
}
