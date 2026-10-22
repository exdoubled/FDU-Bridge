package clash

import (
	"errors"
	"fmt"
	"strings"
)

// Switcher orders selector writes so GLOBAL never points at FDU while FDU's
// fallback listener still exits through GLOBAL.
type Switcher struct {
	API       API
	Configure func(front bool) error
	Front     bool
	Unsafe    map[string]bool
}

func (s *Switcher) Read(requireGlobal bool) (Proxies, error) {
	var config map[string]any
	if err := s.API.Do("GET", "/configs", nil, &config); err != nil {
		return nil, err
	}
	if requireGlobal && !strings.EqualFold(text(config["mode"]), "global") {
		return nil, errors.New("请先在 Clash 中选择全局模式")
	}
	var response proxyResponse
	if err := s.API.Do("GET", "/proxies", nil, &response); err != nil {
		return nil, err
	}
	if response.Proxies["GLOBAL"] == nil || response.Proxies["INTERNET"] == nil {
		return nil, errors.New("请先在设置中一键导入当前订阅")
	}
	return response.Proxies, nil
}

func (s *Switcher) safe(proxies Proxies, name string) bool {
	return SafeTarget(proxies, name, map[string]bool{}, s.Unsafe)
}

func (s *Switcher) set(group, name, expected string, requireGlobal bool) error {
	fresh, err := s.Read(requireGlobal)
	if err != nil {
		return err
	}
	node := fresh[group]
	if node.Now != expected || node.All == nil || !contains(node.All, name) {
		return errors.New("Clash 的选择或节点列表已变化，请刷新后重试")
	}
	if group == "GLOBAL" && name == "FDU-ROUTER" && !s.safe(fresh, fresh["INTERNET"].Now) {
		return errors.New("INTERNET 尚未选择安全的外网节点，拒绝形成回环")
	}
	if expected == name {
		return nil
	}
	if err := s.API.Do("PUT", "/proxies/"+group, map[string]string{"name": name}, nil); err != nil {
		return err
	}
	after, err := s.Read(requireGlobal)
	if err != nil {
		return err
	}
	if after[group].Now != name {
		return errors.New("未收到 Clash 的切换确认")
	}
	return nil
}

func (s *Switcher) current(group string, requireGlobal bool) (string, error) {
	proxies, err := s.Read(requireGlobal)
	if err != nil {
		return "", err
	}
	return proxies[group].Now, nil
}

// EnableRouter keeps the current Internet exit and changes only GLOBAL.
func (s *Switcher) EnableRouter() error {
	if s.Front {
		return errors.New("FDU 前置模式的 GLOBAL 应保持选择外网节点")
	}
	proxies, err := s.Read(true)
	if err != nil {
		return err
	}
	return s.set("GLOBAL", "FDU-ROUTER", proxies["GLOBAL"].Now, true)
}

// Restore puts back the selections in before unless someone else (not owned*) changed them.
func (s *Switcher) Restore(before Proxies, ownedGlobal, ownedInternet string) error {
	now, err := s.Read(false)
	if err != nil {
		return err
	}
	global := before["GLOBAL"].Now
	internet := now["INTERNET"].Now
	if before["INTERNET"] != nil {
		internet = before["INTERNET"].Now
	}
	if (ownedGlobal != "" && now["GLOBAL"].Now != global && now["GLOBAL"].Now != ownedGlobal) ||
		(ownedInternet != "" && now["INTERNET"].Now != internet && now["INTERNET"].Now != ownedInternet) {
		return errors.New("策略已被其他操作改变，未覆盖；请刷新确认")
	}
	if !s.safe(now, internet) {
		return errors.New("原始外网选择不安全或已失效，未恢复")
	}
	if err := s.set("INTERNET", internet, now["INTERNET"].Now, false); err != nil {
		return err
	}
	currentGlobal, err := s.current("GLOBAL", false)
	if err != nil {
		return err
	}
	return s.set("GLOBAL", global, currentGlobal, false)
}

func (s *Switcher) Switch(front bool, target string) error {
	before, err := s.Read(true)
	if err != nil {
		return err
	}
	global, internet := before["GLOBAL"], before["INTERNET"]
	if global.Type != "Selector" || internet.Type != "Selector" || global.All == nil || internet.All == nil || !contains(global.All, "FDU-ROUTER") {
		return errors.New("当前订阅尚未导入两种模式支持，请先在设置中一键导入")
	}
	if !s.safe(before, target) || !contains(global.All, target) || !contains(internet.All, target) {
		return errors.New("请选择两种模式共用的外网节点；可先在“节点”页选择一个具体节点")
	}
	if s.Configure == nil {
		return errors.New("缺少专用监听配置操作，未切换模式")
	}
	oldFront, configurationAttempted := s.Front, false
	err = func() error {
		// Keep the fallback's GLOBAL destination outside FDU during every reload.
		if err := s.set("GLOBAL", target, global.Now, true); err != nil {
			return err
		}
		configurationAttempted = true
		if err := s.Configure(front); err != nil {
			return err
		}
		s.Front = front
		if !front {
			now, err := s.current("INTERNET", true)
			if err == nil {
				err = s.set("INTERNET", target, now, true)
			}
			if err == nil {
				now, err = s.current("GLOBAL", true)
			}
			if err == nil {
				err = s.set("GLOBAL", "FDU-ROUTER", now, true)
			}
			if err != nil {
				return err
			}
		}
		confirmed, err := s.Read(true)
		if err != nil {
			return err
		}
		want := target
		if !front {
			want = "FDU-ROUTER"
		}
		if confirmed["GLOBAL"].Now != want || (!front && confirmed["INTERNET"].Now != target) {
			return errors.New("模式切换的最终状态未确认")
		}
		return nil
	}()
	if err == nil {
		return nil
	}
	rollback := func() error {
		if configurationAttempted {
			now, err := s.Read(false)
			if err != nil {
				return err
			}
			if g := now["GLOBAL"].Now; g != global.Now && g != target && g != "FDU-ROUTER" {
				return errors.New("GLOBAL 已被其他操作改变，未覆盖；请刷新确认")
			}
			if err := s.set("GLOBAL", target, now["GLOBAL"].Now, false); err != nil {
				return err
			}
			// A failed reload may already have changed the listener. Confirm its restoration
			// before allowing GLOBAL=FDU-ROUTER again; otherwise leave a safe external node.
			if err := s.Configure(oldFront); err != nil {
				return err
			}
			s.Front = oldFront
		}
		ownedInternet := target
		if front {
			ownedInternet = internet.Now
		}
		return s.Restore(before, target, ownedInternet)
	}()
	if rollback != nil {
		return fmt.Errorf("%v\n恢复未确认：%v", err, rollback)
	}
	return fmt.Errorf("%v\n已恢复原选择", err)
}
