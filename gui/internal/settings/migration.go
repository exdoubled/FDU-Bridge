package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mythologyli/zju-connect/gui/internal/appconf"
	"github.com/mythologyli/zju-connect/gui/internal/clashsetup"
	"github.com/mythologyli/zju-connect/gui/internal/fsx"
	"github.com/mythologyli/zju-connect/routing"
	"github.com/pelletier/go-toml/v2"
)

// Migration is a bounded preview, retained only until the explicit confirmation.
// Credentials and file contents never enter the frontend preview.
type Migration struct {
	Directory    string   `json:"directory"`
	Files        []string `json:"files"`
	Conflicts    []string `json:"conflicts"`
	workspace    *Workspace
	values       map[string]any
	preferences  appconf.Config
	settings     []byte
	sources      map[string]string
	destinations map[string]string
}

func migrationPath(root, path string, missing bool) error {
	w, _ := New(root)
	if !w.Private(path) {
		return errors.New("只迁移旧 GUI 目录内的受管文件")
	}
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !(missing && os.IsNotExist(err)) {
			return err
		}
		if err == nil && (info.Mode()&os.ModeSymlink != 0 || current == path && !info.Mode().IsRegular()) {
			return errors.New("迁移不支持链接或非常规文件")
		}
		// Ancestors above the managed root may be OS aliases, such as macOS /var.
		if current == w.Dir || filepath.Dir(current) == current {
			break
		}
	}
	return nil
}

// PreviewMigration reads only the selected GUI's preferences, selected managed
// TOML and its private rule/login files. It never scans/copies a whole directory.
func PreviewMigration(source, destination string, autoStart bool) (*Migration, error) {
	source, err := filepath.Abs(source)
	if err != nil {
		return nil, err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return nil, err
	}
	w, _ := New(destination)
	old, _ := New(source)
	if strings.EqualFold(source, destination) || old.Private(destination) || w.Private(source) {
		return nil, errors.New("新旧 GUI 目录不能相同或互相包含")
	}
	p := &Migration{Directory: source, Files: []string{}, Conflicts: []string{}, workspace: w, sources: map[string]string{}, destinations: map[string]string{}}
	read := func(path string, limit int64, missing bool) ([]byte, error) {
		if e := migrationPath(source, path, missing); e != nil {
			return nil, e
		}
		raw, e := fsx.ReadBounded(path, limit)
		if missing && os.IsNotExist(e) {
			p.sources[path] = ""
			return nil, nil
		}
		if e == nil {
			p.sources[path] = fsx.Hash(raw)
		}
		return raw, e
	}
	prefsPath := filepath.Join(source, "gui-settings.json")
	raw, err := read(prefsPath, 65536, false)
	if err != nil {
		return nil, fmt.Errorf("请选择包含 gui-settings.json 的旧 GUI 目录：%w", err)
	}
	if json.Unmarshal(raw, &p.preferences) != nil {
		return nil, errors.New("旧 GUI 设置无效")
	}
	if p.preferences.Theme == "" {
		p.preferences.Theme = "system"
	}
	if p.preferences.Theme != "system" && p.preferences.Theme != "light" && p.preferences.Theme != "dark" {
		return nil, errors.New("旧 GUI 主题设置无效")
	}
	switch p.preferences.Integration.Mode {
	case "", "auto", "manual", "proxy":
	default:
		return nil, errors.New("旧 GUI 接入设置无效")
	}
	journal := filepath.Join(source, "campus-session.json")
	if raw, err = read(journal, 65536, true); err != nil {
		return nil, err
	} else if raw != nil {
		var state struct {
			Active  bool `json:"active"`
			Pending bool `json:"pending"`
		}
		if json.Unmarshal(raw, &state) != nil || state.Active || state.Pending {
			return nil, errors.New("旧目录有未恢复的 Clash 接入，请先在旧程序中停用")
		}
	}
	config := p.preferences.ConfigPath
	if config == "" {
		config = filepath.Join(source, "client.toml")
	} else if !filepath.IsAbs(config) {
		config = filepath.Join(source, config)
	}
	raw, err = read(config, 1024*1024, false)
	if err != nil {
		return nil, err
	}
	var original map[string]any
	if toml.Unmarshal(raw, &original) != nil {
		return nil, errors.New("旧 TOML 配置无效")
	}
	resolve := func(value any) string {
		path, _ := value.(string)
		if path != "" && !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(config), path)
		}
		return path
	}
	route, _ := original["routing"].(map[string]any)
	for _, file := range []struct {
		path  string
		rules bool
	}{{resolve(original["client_data_file"]), false}, {resolve(route["rules_file"]), true}} {
		if file.path == "" {
			continue
		}
		data, e := read(file.path, 4*1024*1024, !file.rules)
		if e != nil {
			return nil, e
		}
		if file.rules {
			if len(data) > 256*1024 {
				return nil, errors.New("旧规则文件超过 256 KiB")
			}
			if _, e = routing.ParseRules(data); e != nil {
				return nil, e
			}
		} else if data != nil && !json.Valid(data) {
			return nil, errors.New("旧登录状态文件无效")
		}
	}
	view, err := w.Load(config, false)
	if err != nil {
		return nil, err
	}
	p.values = view.Values
	// Workspace normalizes private state/rules. Preserve all external output
	// paths: moving this GUI does not give it ownership of those external files.
	for _, key := range []string{"graph_code_file", "debug_pcap_file", "debug_tls_log_file"} {
		path := resolve(original[key])
		if path != "" && !old.Private(path) {
			p.values[key] = path
		}
	}
	rendered, err := clashsetup.RenderSettings(p.values)
	if err != nil {
		return nil, err
	}
	p.preferences.ConfigPath, p.preferences.FduConfig = w.Target, w.Target
	p.preferences.CampusConsent = false
	p.preferences.AutoStart = autoStart // copy data without changing this machine's OS registration
	// Client-owned YAML is deliberately not copied. A binding into the old
	// portable directory must be selected again; genuine external bindings stay.
	if file := p.preferences.Integration.RuntimeFile; file != "" && (old.Private(file) || strings.EqualFold(filepath.Clean(file), source)) {
		p.preferences.Integration = appconf.Integration{Mode: "auto"}
	}
	if root := p.preferences.ClashDir; root != "" && (old.Private(root) || strings.EqualFold(filepath.Clean(root), source)) {
		p.preferences.ClashDir = appconf.DefaultClashDir()
	}
	p.settings, err = json.MarshalIndent(p.preferences, "", "  ")
	if err != nil {
		return nil, err
	}
	outputs := map[string][]byte{w.Target: []byte(rendered.Content), filepath.Join(destination, "gui-settings.json"): p.settings}
	for _, copy := range w.snapshot.Copies {
		if copy.Remove {
			if err = migrationPath(destination, copy.Destination, true); err != nil {
				return nil, err
			}
			p.destinations[copy.Destination] = fsx.HashFile(copy.Destination)
			continue
		}
		data, e := read(copy.Source, 4*1024*1024, false)
		if e != nil {
			return nil, e
		}
		outputs[copy.Destination] = data
	}
	for path, data := range outputs {
		if err = migrationPath(destination, path, true); err != nil {
			return nil, err
		}
		existing, e := fsx.ReadBounded(path, 4*1024*1024)
		if e != nil && !os.IsNotExist(e) {
			return nil, e
		}
		hash := ""
		if e == nil {
			hash = fsx.Hash(existing)
		}
		p.destinations[path] = hash
		p.Files = append(p.Files, path)
		if hash != "" && hash != fsx.Hash(data) {
			p.Conflicts = append(p.Conflicts, path)
		}
		// Workspace.Save also backs up existing state/config files. Those
		// sidecars obey the same no-overwrite and no-link migration boundary.
		if hash != "" && path != filepath.Join(destination, "gui-settings.json") {
			backup := path + ".bak"
			if err = migrationPath(destination, backup, true); err != nil {
				return nil, err
			}
			previous, e := fsx.ReadBounded(backup, 4*1024*1024)
			if e != nil && !os.IsNotExist(e) {
				return nil, e
			}
			backupHash := ""
			if e == nil {
				backupHash = fsx.Hash(previous)
			}
			p.destinations[backup] = backupHash
			if backupHash != "" && backupHash != hash {
				p.Conflicts = append(p.Conflicts, backup)
			}
		}
	}
	// A missing old login cache must never cause Workspace.Save to remove
	// existing destination credentials, even if other files are conflict-free.
	for _, copy := range w.snapshot.Copies {
		if copy.Remove && fsx.HashFile(copy.Destination) != "" {
			p.Conflicts = append(p.Conflicts, copy.Destination)
		}
	}
	slices.Sort(p.Files)
	slices.Sort(p.Conflicts)
	return p, nil
}

// Apply uses Workspace's checked state copies and rollback. Preferences are
// copied as data, without invoking the OS autostart preference writer.
func (p *Migration) Apply() (View, appconf.Config, error) {
	fail := func(err error) (View, appconf.Config, error) { return View{}, appconf.Config{}, err }
	if len(p.Conflicts) > 0 {
		return fail(errors.New("目标已有不同数据，未覆盖：" + strings.Join(p.Conflicts, "、")))
	}
	for path, hash := range p.sources {
		if err := migrationPath(p.Directory, path, true); err != nil {
			return fail(err)
		}
		if fsx.HashFile(path) != hash {
			return fail(errors.New("旧目录在预览后已变化，请重新预览"))
		}
	}
	for path, hash := range p.destinations {
		if err := migrationPath(p.workspace.Dir, path, true); err != nil {
			return fail(err)
		}
		if fsx.HashFile(path) != hash {
			return fail(errors.New("目标目录在预览后已变化，请重新预览"))
		}
	}
	prefsPath := filepath.Join(p.workspace.Dir, "gui-settings.json")
	created := p.destinations[prefsPath] == ""
	if created {
		if err := fsx.AtomicWrite(prefsPath, p.settings); err != nil {
			return fail(err)
		}
	}
	view, err := p.workspace.Save(p.values)
	if err != nil {
		if created && fsx.HashFile(prefsPath) == fsx.Hash(p.settings) {
			if restore := os.Remove(prefsPath); restore != nil {
				return fail(errors.Join(err, restore))
			}
		}
		return fail(err)
	}
	return view, p.preferences, nil
}
