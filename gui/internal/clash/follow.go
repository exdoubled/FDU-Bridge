package clash

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/mythologyli/zju-connect/gui/internal/fsx"
	"gopkg.in/yaml.v3"
)

var ErrClashChanging = errors.New("Clash 仍在切换，等待最新模式和订阅就绪")

// CampusTarget is comparable so the watcher can wait for a stable Clash switch.
type CampusTarget struct {
	ProfileUID string
	Mode       string
	Revision   string
	Installed  bool
}

func campusPlanCurrent(api API, root string, plan Plan) error {
	var live map[string]any
	if err := api.Do("GET", "/configs", nil, &live); err != nil {
		return err
	}
	match, err := campusProfile(CampusAttachment{Root: root, ProfileUID: plan.ProfileUID})
	if err != nil {
		return err
	}
	if !match || !strings.EqualFold(text(live["mode"]), plan.ClashMode) {
		return ErrClashChanging
	}
	return nil
}

func (c *Connection) CurrentCampus(root string) (CampusTarget, error) {
	var target CampusTarget
	api, err := c.api(root)
	if err != nil {
		return target, err
	}
	var live map[string]any
	var response proxyResponse
	if err = api.Do("GET", "/configs", nil, &live); err != nil {
		return target, err
	}
	if err = api.Do("GET", "/proxies", nil, &response); err != nil {
		return target, err
	}
	raw, err := fsx.ReadBounded(filepath.Join(root, "profiles.yaml"), 8*1024*1024)
	if err != nil {
		return target, err
	}
	var profiles struct{ Current string }
	if err = yaml.Unmarshal(raw, &profiles); err != nil {
		return target, err
	}
	if profiles.Current == "" {
		return target, errors.New("等待 Clash 选定订阅")
	}
	target.ProfileUID, target.Mode = profiles.Current, strings.ToLower(text(live["mode"]))
	runtime, err := fsx.ReadBounded(filepath.Join(root, "clash-verge.yaml"), 8*1024*1024)
	if err != nil {
		return target, err
	}
	target.Revision = fsx.Hash(runtime)
	target.Installed = response.Proxies["GLOBAL"] != nil && response.Proxies["INTERNET"] != nil && response.Proxies["FDU-ROUTER"] != nil
	return target, nil
}

// FollowCampus reuses the authenticated core and installs only the current profile.
// Ownership progress is returned on error, just as in ActivateCampus.
func (c *Connection) FollowCampus(a CampusAttachment) (CampusAttachment, error) {
	target, err := c.CurrentCampus(a.Root)
	if err != nil {
		return a, err
	}
	if target.Mode != "global" && target.Mode != "rule" {
		return a, errors.New("等待 Clash 返回全局或规则模式")
	}
	if _, _, err = c.acknowledgedCampusRules(a); err != nil {
		return a, err
	}
	plan, err := c.PrepareCampus(a.Root, a.Config, nil)
	if err != nil {
		return a, err
	}
	if plan.NeedsFduStart {
		return a, errors.New("VPN 配置已变化，无法复用当前连接")
	}
	if plan.ProfileUID != target.ProfileUID || plan.ClashMode != target.Mode {
		return a, ErrCampusPending
	}
	if plan.ProfileUID == a.ProfileUID {
		a, err = c.DeactivateCampus(a)
	} else {
		// The old provider belongs to another profile: clear its file without
		// reloading the new profile's provider or changing its selected exit.
		a, err = replaceCampusProvider(nil, a, []byte("payload: []\n"), false)
		if err == nil {
			a.Active, a.RulesHash = false, ""
		}
	}
	if err != nil {
		return a, err
	}
	// Deactivation changes the provider and may restore GLOBAL; take a fresh plan.
	plan, err = c.PrepareCampus(a.Root, a.Config, nil)
	if err != nil {
		return a, err
	}
	if plan.NeedsFduStart || plan.ProfileUID != target.ProfileUID || plan.ClashMode != target.Mode {
		return a, ErrCampusPending
	}
	_, next, err := c.ImportCampus(plan)
	if err != nil {
		return a, err
	}
	return c.ActivateCampus(next)
}
