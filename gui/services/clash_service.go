package services

import (
	"errors"
	"path/filepath"

	"github.com/mythologyli/zju-connect/gui/internal/appconf"
	"github.com/mythologyli/zju-connect/gui/internal/clash"
)

// ClashOverview is a read-only inspection of the active subscription.
type ClashOverview struct {
	Ready         bool     `json:"ready"`
	Error         string   `json:"error"`
	ClashDir      string   `json:"clashDir"`
	FduConfig     string   `json:"fduConfig"`
	Profile       string   `json:"profile"`
	Mode          string   `json:"mode"`
	Config        string   `json:"config"`
	Rules         string   `json:"rules"`
	SocksPort     int      `json:"socksPort"`
	HTTPPort      int      `json:"httpPort"`
	UpstreamPort  int      `json:"upstreamPort"`
	MixedPort     int      `json:"mixedPort"`
	SystemProxy   bool     `json:"systemProxy"`
	ProxyGuard    bool     `json:"proxyGuard"`
	NeedsFduStart bool     `json:"needsFduStart"`
	Files         []string `json:"files"`
	Proxy         string   `json:"proxy"`
}

// ClashService inspects Clash and picks outside exit nodes.
type ClashService struct{ b *Backend }

func NewClashService(b *Backend) *ClashService { return &ClashService{b} }

var errBusy = errors.New("正在处理上一项操作，请稍候")

func (s *ClashService) lock() error {
	if !s.b.clashBusy.TryLock() {
		return errBusy
	}
	return nil
}

func (s *ClashService) paths() (string, string, error) {
	root, err := s.b.campusRoot()
	return root, s.b.workspace.Target, err
}

func overview(root, config string, plan *clash.Plan, err error) ClashOverview {
	o := ClashOverview{ClashDir: root, FduConfig: config, Proxy: clash.ProxyDescription(), Files: []string{}}
	if err != nil {
		o.Error = err.Error()
		return o
	}
	o.Ready, o.Profile, o.Mode, o.Config, o.Rules = true, plan.Profile, plan.Mode, plan.Config, plan.Rules
	o.SocksPort, o.HTTPPort, o.UpstreamPort, o.MixedPort = plan.SocksPort, plan.HTTPPort, plan.UpstreamPort, plan.MixedPort
	o.SystemProxy, o.ProxyGuard, o.NeedsFduStart = plan.SystemProxy, plan.ProxyGuard, plan.NeedsFduStart
	for _, f := range plan.Files {
		o.Files = append(o.Files, f.Path)
	}
	return o
}

func (s *ClashService) inspect() (ClashOverview, *clash.Plan) {
	if s.b.proxyOnly() {
		return ClashOverview{Ready: true, Profile: "仅本地代理", Mode: "proxy", FduConfig: s.b.workspace.Target, Files: []string{}}, nil
	}
	root, config, pathErr := s.paths()
	if pathErr != nil {
		return overview(root, config, nil, pathErr), nil
	}
	view := s.b.currentView()
	if view == nil {
		return overview(root, config, nil, errors.New("未找到默认 VPN 配置")), nil
	}
	plan, err := s.b.conn.PrepareCampus(root, config, view.Values)
	if err != nil {
		return overview(root, config, nil, err), nil
	}
	return overview(root, config, &plan, nil), &plan
}

// Inspect only reads; it never writes Clash or FDU files, so it waits instead of failing.
func (s *ClashService) Inspect() (ClashOverview, error) {
	s.b.clashBusy.Lock()
	defer s.b.clashBusy.Unlock()
	o, _ := s.inspect()
	return o, nil
}

func (s *ClashService) Nodes() (clash.NodeState, error) {
	s.b.clashBusy.Lock()
	defer s.b.clashBusy.Unlock()
	if s.b.proxyOnly() {
		return clash.NodeState{Choices: []string{}, Types: map[string]string{}, Problem: "仅本地代理：请在外部客户端选择出口"}, nil
	}
	picker, err := s.b.picker()
	if err != nil {
		return clash.NodeState{}, err
	}
	return picker.Load()
}

func (s *ClashService) SelectNode(name string) (clash.NodeState, error) {
	if err := s.lock(); err != nil {
		return clash.NodeState{}, err
	}
	defer s.b.clashBusy.Unlock()
	if s.b.proxyOnly() {
		return clash.NodeState{}, errors.New("仅本地代理：请在外部客户端选择出口")
	}
	picker, err := s.b.picker()
	if err != nil {
		return clash.NodeState{}, err
	}
	state, err := picker.Select(name)
	if err == nil {
		s.b.emit(EventChanged, "node")
	}
	return state, err
}

// SetPaths changes where Clash Verge and the integration's FDU config live.
func (s *ClashService) SetPaths(clashDir, fduConfig string) (ClashOverview, error) {
	s.b.clashBusy.Lock()
	defer s.b.clashBusy.Unlock()
	if err := NewSettingsService(s.b).editable(); err != nil {
		return ClashOverview{}, err
	}
	if clashDir != "" {
		if abs, err := filepath.Abs(clashDir); err == nil {
			clashDir = abs
		}
		if err := appconf.ValidateClashDir(clashDir); err != nil {
			return ClashOverview{}, err
		}
	}
	if fduConfig != "" && fduConfig != s.b.workspace.Target {
		view, err := s.b.workspace.Load(fduConfig, s.b.workspace.Private(fduConfig))
		if err != nil {
			return ClashOverview{}, err
		}
		s.b.setView(view)
	}
	if err := s.b.saveConfig(func(c *appconf.Config) {
		if clashDir != "" && c.ClashDir != clashDir {
			c.CampusConsent = false
		}
		if clashDir != "" {
			c.ClashDir = clashDir
		}
	}); err != nil {
		return ClashOverview{}, err
	}
	s.b.emit(EventChanged, "settings")
	o, _ := s.inspect()
	return o, nil
}

// Cached returns the overview of the last inspection without touching any file.
func (s *ClashService) Cached() ClashOverview {
	s.b.clashBusy.Lock()
	defer s.b.clashBusy.Unlock()
	if s.b.proxyOnly() {
		return ClashOverview{Ready: true, Profile: "仅本地代理", Mode: "proxy", FduConfig: s.b.workspace.Target, Files: []string{}}
	}
	root, config, err := s.paths()
	if err != nil {
		return overview(root, config, nil, err)
	}
	if plan := s.b.currentPlan(); plan != nil {
		return overview(root, config, plan, nil)
	}
	return overview(root, config, nil, errors.New("尚未检查 Clash 配置"))
}
