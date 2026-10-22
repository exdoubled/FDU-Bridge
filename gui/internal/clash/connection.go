package clash

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mythologyli/zju-connect/gui/internal/clashsetup"
	"github.com/mythologyli/zju-connect/gui/internal/fsx"
	"github.com/mythologyli/zju-connect/gui/internal/rules"
	"github.com/mythologyli/zju-connect/gui/internal/sysproc"
	"gopkg.in/yaml.v3"
)

type Plan = clashsetup.Plan

// Connection imports FDU support into the active Clash Verge profile and applies modes.
type Connection struct {
	Dir             string // GUI-owned directory for generated configs and backups
	API             API    // nil discovers the live controller
	feedMu          sync.Mutex
	feed            *campusFeed
	targetMu        sync.RWMutex
	runtimeFile     string
	controller      *ControlAPI
	previousRuntime string // original source while a verified automatic migration is settling
}

// api finds the controller of the Verge instance whose files live in clashDir.
func (c *Connection) api(clashDir string) (API, error) {
	if c.API != nil {
		return c.API, nil
	}
	c.targetMu.RLock()
	file, controller := c.runtimeFile, c.controller
	c.targetMu.RUnlock()
	if controller != nil {
		return *controller, nil
	}
	if file != "" {
		return DiscoverRuntime(file)
	}
	return Discover(clashDir)
}

func UnsafeSet(plan Plan) map[string]bool {
	set := map[string]bool{}
	for _, name := range plan.UnsafeNodes {
		set[name] = true
	}
	return set
}

func (c *Connection) Prepare(root, config, mode string) (Plan, error) {
	return clashsetup.Prepare(clashsetup.Request{Root: root, Config: config, Directory: c.Dir, Mode: mode, OccupiedPorts: sysproc.UsedPorts(17902, 18000)})
}

func profilesRoot(plan Plan) (string, error) {
	if plan.RuntimeFile != "" {
		return filepath.Dir(plan.RuntimeFile), nil
	}
	for path := range plan.Inputs {
		if filepath.Base(path) == "profiles.yaml" {
			return filepath.Dir(path), nil
		}
	}
	return "", errors.New("检查结果缺少 profiles.yaml")
}

func number(v any) int {
	if n, ok := v.(float64); ok {
		return int(n)
	}
	return 0
}

func checkInputs(plan Plan, message string) error {
	for path, hash := range plan.Inputs {
		if fsx.HashFile(path) != hash {
			return errors.New(message)
		}
	}
	return nil
}

func backupName() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

// Import writes the previewed files only if nothing changed since the preview, then reloads Clash.
// Any failure restores the files it wrote and the previous runtime configuration.
func (c *Connection) Import(plan Plan) (string, error) {
	root, err := profilesRoot(plan)
	if err != nil {
		return "", err
	}
	api, err := c.api(root)
	if err != nil {
		return "", err
	}
	var live map[string]any
	if err := api.Do("GET", "/configs", nil, &live); err != nil {
		return "", err
	}
	livePort := number(live["mixed-port"])
	if livePort == 0 {
		livePort = number(live["port"])
	}
	if livePort != plan.MixedPort {
		return "", errors.New("所选目录与当前 Clash 控制接口不匹配，未导入")
	}
	if plan.Campus {
		if err := campusPlanCurrent(api, root, plan); err != nil {
			return "", err
		}
	}
	if err := checkInputs(plan, "配置在预览后发生变化，请重新检查再导入"); err != nil {
		return "", err
	}
	if plan.Campus {
		feed, err := c.campusFeed()
		if err != nil {
			return "", err
		}
		feed.url(plan.ProviderPath)
		if err := feed.save(); err != nil {
			return "", err
		}
	}
	var selections proxyResponse
	if err := api.Do("GET", "/proxies", nil, &selections); err != nil {
		return "", err
	}
	unsafe := UnsafeSet(plan)
	if plan.Mode == "fdu-first" && (selections.Proxies["GLOBAL"] == nil || !SafeTarget(selections.Proxies, selections.Proxies["GLOBAL"].Now, map[string]bool{}, unsafe)) {
		return "", errors.New("FDU 前置的 GLOBAL 必须选择安全外网节点，请先刷新并应用正确模式")
	}
	runtimePath := filepath.Join(root, "clash-verge.yaml")
	if plan.RuntimeFile != "" {
		runtimePath = plan.RuntimeFile
	}
	oldRuntime, err := fsx.ReadBounded(runtimePath, 8*1024*1024)
	if err != nil {
		return "", err
	}
	if plan.RuntimeFile != "" {
		var original map[string]any
		if err = yaml.Unmarshal(oldRuntime, &original); err != nil || original == nil {
			return "", errors.New("客户端运行配置无效")
		}
		for key, value := range runtimeOverrides(live, api) {
			original[key] = value
		}
		oldRuntime, err = yaml.Marshal(original)
		if err != nil {
			return "", err
		}
	}
	changes := make([]cleanupChange, 0, len(plan.Files))
	for _, change := range plan.Files {
		changes = append(changes, cleanupChange{change.Path, change.Hash, []byte(change.Content)})
	}
	backup, original, err := c.backupChanges(changes)
	if err != nil {
		return "", err
	}
	var written []clashsetup.Change
	reloadAttempted := false
	err = func() error {
		for _, change := range plan.Files {
			if fsx.HashFile(change.Path) != change.Hash {
				return errors.New("配置正在被其他程序修改，请稍后重试")
			}
			if err := fsx.AtomicWrite(change.Path, []byte(change.Content)); err != nil {
				return err
			}
			written = append(written, change)
		}
		if plan.Campus {
			if err := campusPlanCurrent(api, root, plan); err != nil {
				return err
			}
		}
		reloadAttempted = true
		if err := api.Do("PUT", "/configs?force=true", map[string]string{"payload": plan.Payload}, nil); err != nil {
			return err
		}
		var now proxyResponse
		if err := api.Do("GET", "/proxies", nil, &now); err != nil {
			return err
		}
		if now.Proxies["FDU-ROUTER"] == nil || now.Proxies["INTERNET"] == nil {
			return errors.New("Clash 未确认导入两种模式支持")
		}
		if plan.Campus {
			if err := campusPlanCurrent(api, root, plan); err != nil {
				return err
			}
		}
		if selections.Proxies["GLOBAL"] != nil {
			if plan.Campus {
				return restoreCampusSelections(api, selections.Proxies, plan.ClashMode, unsafe)
			}
			return (&Switcher{API: api, Unsafe: unsafe}).Restore(selections.Proxies, "", "")
		}
		return nil
	}()
	if err == nil {
		return backup, nil
	}
	var failures []string
	for _, change := range slices.Backward(written) {
		var rollback error
		if fsx.HashFile(change.Path) != fsx.Hash([]byte(change.Content)) {
			rollback = errors.New("文件已被其他程序修改")
		} else if data, existed := original[change.Path]; !existed {
			rollback = os.Remove(change.Path)
		} else {
			rollback = fsx.AtomicWrite(change.Path, data)
		}
		if rollback != nil {
			failures = append(failures, filepath.Base(change.Path)+": "+rollback.Error())
		}
	}
	if reloadAttempted {
		if plan.Campus && campusPlanCurrent(api, root, plan) != nil {
			failures = append(failures, "Clash 已切换或暂不可用，未回写旧运行配置")
		} else if rollback := api.Do("PUT", "/configs?force=true", map[string]string{"payload": string(oldRuntime)}, nil); rollback != nil {
			failures = append(failures, "Clash 恢复未确认："+rollback.Error())
		}
	}
	summary := "已恢复原配置"
	if len(failures) > 0 {
		summary = strings.Join(failures, "\n")
	}
	return "", fmt.Errorf("导入失败：%v\n%s\n备份：%s", err, summary, backup)
}

func fduReady(plan Plan) error {
	if plan.NeedsFduStart {
		return errors.New("请先用生成的分流配置连接 FDU，再重新检查状态")
	}
	store, err := rules.New(plan.Rules)
	if err != nil {
		return err
	}
	snapshot, err := store.Load()
	if err != nil {
		return err
	}
	if !store.ApplyStatus(snapshot.Hash).Applied {
		return errors.New("尚未收到 FDU 加载这份规则的确认，请先连接使用这份配置的 FDU")
	}
	pid, _, _, ok := store.Acknowledgement()
	if !ok || !strings.EqualFold(sysproc.RunningConfig(pid), plan.Config) {
		return errors.New("运行中的 FDU 未使用这份配置，或启动参数不是绝对路径；请用这份配置重新连接后重试")
	}
	started, err := sysproc.StartTime(pid)
	if err != nil {
		return err
	}
	if info, err := os.Stat(plan.Config); err != nil || info.ModTime().After(started) {
		return errors.New("FDU 配置在连接后被修改，请先重新连接 FDU，再应用模式")
	}
	return nil
}

// EnableRouter is used immediately after import; it does not change the system proxy.
func (c *Connection) EnableRouter(plan Plan) error {
	if err := fduReady(plan); err != nil {
		return err
	}
	if err := checkInputs(plan, "配置在导入后发生变化，请重新检查再启用"); err != nil {
		return err
	}
	root, err := profilesRoot(plan)
	if err != nil {
		return err
	}
	api, err := c.api(root)
	if err != nil {
		return err
	}
	return (&Switcher{API: api, Front: plan.Mode == "fdu-first", Unsafe: UnsafeSet(plan)}).EnableRouter()
}

// Apply switches between Clash-first and FDU-first after checking that the running core
// is the one this plan generated, then points the system proxy at the new entry. The note
// is non-empty when this desktop's proxy has to be set by hand.
func (c *Connection) Apply(plan Plan, front bool) (string, error) {
	if err := fduReady(plan); err != nil {
		return "", err
	}
	root, err := profilesRoot(plan)
	if err != nil {
		return "", err
	}
	api, err := c.api(root)
	if err != nil {
		return "", err
	}
	var config map[string]any
	if err := api.Do("GET", "/configs", nil, &config); err != nil {
		return "", err
	}
	if tun, _ := config["tun"].(map[string]any); front && tun != nil && tun["enable"] == true {
		return "", errors.New("FDU 前置模式需要先在 Clash 中关闭 TUN")
	}
	if front && (plan.SystemProxy || plan.ProxyGuard) {
		return "", errors.New("FDU 前置模式需要先在 Clash 中关闭系统代理和代理守卫，避免它把代理入口改回 Clash")
	}
	if err := checkInputs(plan, "配置在检查后发生变化，请重新检查再应用"); err != nil {
		return "", err
	}
	switcher := &Switcher{API: api, Front: plan.Mode == "fdu-first", Unsafe: UnsafeSet(plan), Configure: func(selectedFront bool) error {
		mode := "clash-first"
		if selectedFront {
			mode = "fdu-first"
		}
		selected, err := c.Prepare(root, plan.Config, mode)
		if err == nil {
			_, err = c.Import(selected)
		}
		return err
	}}
	before, err := switcher.Read(true)
	if err != nil {
		return "", err
	}
	target := before["INTERNET"].Now
	if plan.Mode == "fdu-first" {
		target = before["GLOBAL"].Now
	}
	proxyBefore, err := CaptureProxy()
	manualProxy := errors.Is(err, ErrProxyUnsupported)
	if err != nil && !manualProxy {
		return "", err
	}
	port := plan.MixedPort
	if front {
		port = plan.HTTPPort
	}
	setProxy := func() error {
		if manualProxy {
			return nil
		}
		return SetProxy(port)
	}
	switched := false
	err = func() error {
		if front {
			if err := setProxy(); err != nil {
				return err
			}
		}
		if err := switcher.Switch(front, target); err != nil {
			return err
		}
		switched = true
		if !front {
			return setProxy()
		}
		return nil
	}()
	if err == nil {
		if manualProxy {
			return fmt.Sprintf("当前桌面不支持自动设置系统代理，请手动把 HTTP 和 HTTPS 代理设为 127.0.0.1:%d", port), nil
		}
		return "", nil
	}
	rollback := func() error {
		if switched {
			if err := switcher.Switch(plan.Mode == "fdu-first", target); err != nil {
				return err
			}
			if err := switcher.Restore(before, "", ""); err != nil {
				return err
			}
		}
		if manualProxy {
			return nil
		}
		return RestoreProxy(proxyBefore)
	}()
	if rollback != nil {
		return "", fmt.Errorf("%v\n策略恢复未确认：%v", err, rollback)
	}
	return "", err
}
