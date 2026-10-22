package clash

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

func systemProxyBypass(port int) ([]string, bool, error) {
	snapshot, err := CaptureProxy()
	if err != nil {
		return nil, false, err
	}
	if snapshot.kind == "gnome" {
		if snapshot.values["org.gnome.system.proxy mode"] == "auto" {
			return nil, false, errors.New("当前使用 PAC，实际路径由 PAC 决定")
		}
		out, err := run("gsettings", "get", "org.gnome.system.proxy", "ignore-hosts")
		if err != nil {
			return nil, false, err
		}
		var patterns []string
		for _, match := range regexp.MustCompile(`'([^']*)'`).FindAllStringSubmatch(out, -1) {
			patterns = append(patterns, match[1])
		}
		return patterns, snapshot.pointsAt(port), nil
	}
	if snapshot.values["ProxyType"] == "2" {
		return nil, false, errors.New("当前使用 PAC，实际路径由 PAC 决定")
	}
	read, _, _ := kdeTools()
	out, err := run(read, "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "NoProxyFor")
	return strings.FieldsFunc(strings.TrimSpace(out), func(r rune) bool { return r == ',' || r == ';' }), snapshot.pointsAt(port), err
}

// ProxySnapshot keeps the desktop proxy keys we change, by desktop kind.
type ProxySnapshot struct {
	kind   string // "gnome" or "kde"
	values map[string]string
}

func (s ProxySnapshot) pointsAt(port int) bool {
	if s.kind == "gnome" && s.values["org.gnome.system.proxy mode"] == "manual" {
		for _, kind := range []string{"http", "https"} {
			if s.values["org.gnome.system.proxy."+kind+" host"] == "127.0.0.1" && s.values["org.gnome.system.proxy."+kind+" port"] == strconv.Itoa(port) {
				return true
			}
		}
	}
	if s.kind == "kde" && s.values["ProxyType"] == "1" {
		for _, kind := range []string{"httpProxy", "httpsProxy"} {
			if strings.TrimPrefix(s.values[kind], "http://") == "127.0.0.1 "+strconv.Itoa(port) {
				return true
			}
		}
	}
	return false
}

var gnomeKeys = [][2]string{
	{"org.gnome.system.proxy", "mode"},
	{"org.gnome.system.proxy.http", "host"},
	{"org.gnome.system.proxy.http", "port"},
	{"org.gnome.system.proxy.https", "host"},
	{"org.gnome.system.proxy.https", "port"},
}

var kdeKeys = []string{"ProxyType", "httpProxy", "httpsProxy"}

func kdeTools() (string, string, bool) {
	for _, v := range []string{"6", "5"} {
		read, errRead := exec.LookPath("kreadconfig" + v)
		write, errWrite := exec.LookPath("kwriteconfig" + v)
		if errRead == nil && errWrite == nil {
			return read, write, true
		}
	}
	return "", "", false
}

// desktop picks KDE on Plasma sessions and GNOME settings wherever its schema exists,
// which also covers Cinnamon, Budgie and other GNOME-based desktops.
func desktop() string {
	if strings.Contains(strings.ToUpper(os.Getenv("XDG_CURRENT_DESKTOP")), "KDE") {
		if _, _, ok := kdeTools(); ok {
			return "kde"
		}
	}
	if out, err := run("gsettings", "list-schemas"); err == nil && strings.Contains(out, "org.gnome.system.proxy\n") {
		return "gnome"
	}
	return ""
}

func CaptureProxy() (ProxySnapshot, error) {
	switch kind := desktop(); kind {
	case "gnome":
		s := ProxySnapshot{kind: kind, values: map[string]string{}}
		for _, k := range gnomeKeys {
			out, err := run("gsettings", "get", k[0], k[1])
			if err != nil {
				return ProxySnapshot{}, err
			}
			s.values[k[0]+" "+k[1]] = unquoteGSettings(out)
		}
		return s, nil
	case "kde":
		read, _, _ := kdeTools()
		s := ProxySnapshot{kind: kind, values: map[string]string{}}
		for _, key := range kdeKeys {
			out, _ := run(read, "--file", "kioslaverc", "--group", "Proxy Settings", "--key", key)
			s.values[key] = strings.TrimSpace(out)
		}
		return s, nil
	}
	return ProxySnapshot{}, ErrProxyUnsupported
}

func applySnapshot(s ProxySnapshot) error {
	switch s.kind {
	case "gnome":
		// Hosts and ports first, so the mode never points at a half-written proxy.
		for i := len(gnomeKeys) - 1; i >= 0; i-- {
			k := gnomeKeys[i]
			value := s.values[k[0]+" "+k[1]]
			if k[1] == "port" {
				if _, err := strconv.Atoi(value); err != nil {
					value = "0"
				}
			} else {
				value = "'" + strings.ReplaceAll(value, "'", `\'`) + "'" // GVariant string literal
			}
			if _, err := run("gsettings", "set", k[0], k[1], value); err != nil {
				return err
			}
		}
		return nil
	case "kde":
		_, write, _ := kdeTools()
		for _, key := range kdeKeys {
			if _, err := run(write, "--file", "kioslaverc", "--group", "Proxy Settings", "--key", key, s.values[key]); err != nil {
				return err
			}
		}
		// Ask running KDE applications to reload proxy settings; harmless if no session bus.
		_, _ = run("dbus-send", "--type=signal", "/KIO/Scheduler", "org.kde.KIO.Scheduler.reparseSlaveConfiguration", "string:")
		return nil
	}
	return ErrProxyUnsupported
}

func RestoreProxy(s ProxySnapshot) error {
	if s.kind == "" {
		return nil
	}
	return applySnapshot(s)
}

func ProxyDescription() string {
	s, err := CaptureProxy()
	switch {
	case errors.Is(err, ErrProxyUnsupported):
		return "系统代理：当前桌面不支持自动设置"
	case err != nil:
		return "系统代理：未知"
	case s.kind == "gnome" && s.values["org.gnome.system.proxy mode"] == "manual":
		return fmt.Sprintf("系统代理：%s:%s", s.values["org.gnome.system.proxy.http host"], s.values["org.gnome.system.proxy.http port"])
	case s.kind == "kde" && s.values["ProxyType"] == "1":
		return "系统代理：" + strings.Replace(s.values["httpProxy"], " ", ":", 1)
	}
	return "系统代理：未设置"
}

// SetProxy points the desktop's HTTP and HTTPS proxy at 127.0.0.1:port.
func SetProxy(port int) error {
	before, err := CaptureProxy()
	if err != nil {
		return err
	}
	if (before.kind == "gnome" && before.values["org.gnome.system.proxy mode"] == "auto") || (before.kind == "kde" && before.values["ProxyType"] == "2") {
		return errors.New("当前启用了 PAC 自动配置，请先在系统设置中关闭，再应用连接模式")
	}
	next := ProxySnapshot{kind: before.kind, values: map[string]string{}}
	p := strconv.Itoa(port)
	if before.kind == "gnome" {
		next.values = map[string]string{
			"org.gnome.system.proxy mode":       "manual",
			"org.gnome.system.proxy.http host":  "127.0.0.1",
			"org.gnome.system.proxy.http port":  p,
			"org.gnome.system.proxy.https host": "127.0.0.1",
			"org.gnome.system.proxy.https port": p,
		}
	} else {
		next.values = map[string]string{"ProxyType": "1", "httpProxy": "http://127.0.0.1 " + p, "httpsProxy": "http://127.0.0.1 " + p}
	}
	if err := applySnapshot(next); err != nil {
		if restore := applySnapshot(before); restore != nil {
			return fmt.Errorf("%v\n恢复未确认：%v", err, restore)
		}
		return err
	}
	return nil
}
