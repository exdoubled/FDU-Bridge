package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/mythologyli/zju-connect/gui/internal/appconf"
	"github.com/mythologyli/zju-connect/gui/internal/fsx"
	"github.com/mythologyli/zju-connect/gui/internal/updates"
	"github.com/wailsapp/wails/v3/pkg/application"
	"gopkg.in/yaml.v3"
)

type UpdateStatus struct {
	Current     string  `json:"current"`
	BaseVersion string  `json:"baseVersion"`
	Latest      string  `json:"latest"`
	Phase       string  `json:"phase"`
	Message     string  `json:"message"`
	Notes       string  `json:"notes"`
	URL         string  `json:"url"`
	Progress    float64 `json:"progress"`
	AutoCheck   bool    `json:"autoCheck"`
}

func init() { application.RegisterEvent[UpdateStatus]("app:update") }

type UpdateService struct {
	b       *Backend
	mu      sync.Mutex
	status  UpdateStatus
	release *updates.Release
	pending *updates.Pending
	busy    bool
	closed  bool
	ctx     context.Context
	cancel  context.CancelFunc
}

func NewUpdateService(b *Backend) *UpdateService {
	ctx, cancel := context.WithCancel(context.Background())
	s := &UpdateService{b: b, ctx: ctx, cancel: cancel,
		status: UpdateStatus{Current: updates.Version, BaseVersion: updates.BaseVersion, Phase: "idle"}}
	b.updater = s
	return s
}

// Checks are silent: only the status event changes, never a dialog or download.
func (s *UpdateService) ServiceStartup(context.Context, application.ServiceOptions) error {
	go func() {
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-timer.C:
				if s.Status().AutoCheck {
					_, _ = s.Check()
				}
				timer.Reset(24 * time.Hour)
			}
		}
	}()
	return nil
}

func (s *UpdateService) Status() UpdateStatus {
	s.mu.Lock()
	status := s.status
	s.mu.Unlock()
	s.b.mu.Lock()
	status.AutoCheck = s.b.config.CheckUpdates
	s.b.mu.Unlock()
	return status
}

func (s *UpdateService) publish() { s.b.emit("app:update", s.Status()) }

// The first rendered page acknowledges startup before the installer drops its backups.
func (s *UpdateService) Ready() error { return updates.ConfirmStartup() }

func (s *UpdateService) finish(phase string, err error) (UpdateStatus, error) {
	s.mu.Lock()
	s.busy = false
	s.status.Phase, s.status.Message = phase, ""
	if err != nil {
		s.status.Message = err.Error()
		if phase != "ready" {
			s.status.Phase = "error"
		}
	}
	s.mu.Unlock()
	s.publish()
	return s.Status(), err
}

func (s *UpdateService) Check() (UpdateStatus, error) {
	s.mu.Lock()
	// Keep a verified download until the user installs or exits.
	if s.busy || s.closed || s.pending != nil {
		s.mu.Unlock()
		return s.Status(), nil
	}
	s.busy = true
	s.status.Phase, s.status.Message = "checking", ""
	s.mu.Unlock()
	s.publish()
	base := updates.Version
	if base == "dev" {
		base = updates.BaseVersion
	}
	if base == "" {
		return s.finish("error", errors.New("开发构建缺少版本标签，请使用正式发布包检查更新"))
	}
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	client := s.httpClient()
	defer client.CloseIdleConnections()
	release, err := updates.Check(ctx, client, base, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return s.finish("error", fmt.Errorf("检查更新失败: %w", err))
	}
	s.mu.Lock()
	s.release = release
	s.status.Latest, s.status.Notes, s.status.URL = "", "", ""
	if release != nil {
		s.status.Latest, s.status.Notes, s.status.URL = release.Version, release.Notes, release.URL
	}
	s.mu.Unlock()
	if release == nil {
		return s.finish("current", nil)
	}
	return s.finish("available", nil)
}

func (s *UpdateService) Download() (UpdateStatus, error) {
	s.mu.Lock()
	if s.busy || s.closed || s.pending != nil {
		s.mu.Unlock()
		return s.Status(), nil
	}
	release := s.release
	if release == nil {
		s.mu.Unlock()
		return s.Status(), errors.New("请先检查更新")
	}
	s.busy = true
	s.status.Phase, s.status.Message, s.status.Progress = "downloading", "", 0
	s.mu.Unlock()
	s.publish()
	executable, err := os.Executable()
	if err != nil {
		return s.finish("error", err)
	}
	ctx, cancel := context.WithTimeout(s.ctx, 20*time.Minute)
	defer cancel()
	client := s.httpClient()
	defer client.CloseIdleConnections()
	lastProgress := time.Time{}
	pending, err := updates.Stage(ctx, client, release, executable, func(done, total int64) {
		if total <= 0 || (done < total && time.Since(lastProgress) < 200*time.Millisecond) {
			return
		}
		lastProgress = time.Now()
		s.mu.Lock()
		s.status.Progress = 100 * float64(done) / float64(total)
		s.mu.Unlock()
		s.publish()
	})
	if err != nil {
		return s.finish("error", fmt.Errorf("下载更新失败: %w", err))
	}
	s.mu.Lock()
	closed := s.closed
	if !closed {
		s.pending = pending
		s.status.Progress = 100
	}
	s.mu.Unlock()
	if closed {
		_ = updates.RemoveStage(pending)
		return s.finish("error", context.Canceled)
	}
	return s.finish("ready", nil)
}

func (s *UpdateService) Install() error {
	s.mu.Lock()
	if s.busy || s.closed || s.pending == nil {
		s.mu.Unlock()
		return errors.New("请先完成更新下载")
	}
	pending := s.pending
	s.busy = true
	s.mu.Unlock()
	// Serialize with Connect/RunTool, then forbid a new core before normal Quit.
	s.b.clashBusy.Lock()
	status := s.b.Status()
	view := s.b.currentView()
	if status.Running || status.Starting || status.Stopping || status.Tool != "" || s.b.runtime.ExternalRunning() || (view != nil && view.Dirty) {
		s.b.clashBusy.Unlock()
		_, err := s.finish("ready", errors.New("请先关闭校内访问、结束工具并保存设置，再安装更新"))
		return err
	}
	s.b.mu.Lock()
	s.b.updating = true
	s.b.mu.Unlock()
	s.b.clashBusy.Unlock()
	s.mu.Lock()
	s.status.Phase = "installing"
	s.mu.Unlock()
	s.publish()
	cancel, err := updates.StartHelper(pending)
	if err == nil {
		if s.b.Quit == nil {
			err = errors.New("当前窗口无法退出安装")
		} else if stopped, quitErr := s.b.Quit(false); quitErr != nil {
			err = quitErr
		} else if !stopped {
			err = errors.New("无法安全退出，请先停用校内访问")
		}
	}
	if err != nil {
		if cancel != nil {
			cancel()
		}
		s.b.mu.Lock()
		s.b.updating = false
		s.b.mu.Unlock()
		_, _ = s.finish("ready", err)
	}
	return err
}

func (s *UpdateService) SetAutoCheck(enabled bool) (appconf.Config, error) {
	err := s.b.saveConfig(func(c *appconf.Config) { c.CheckUpdates = enabled })
	s.publish()
	return NewAppService(s.b).Config(), err
}

func (s *UpdateService) stop() {
	s.mu.Lock()
	s.closed = true
	pending, installing := s.pending, s.status.Phase == "installing"
	s.mu.Unlock()
	s.cancel()
	if pending != nil && !installing {
		_ = updates.RemoveStage(pending)
	}
}

// Read the existing local HTTP port only; never change the client's settings.
func (s *UpdateService) httpClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	s.b.mu.Lock()
	config := s.b.config
	s.b.mu.Unlock()
	runtimeFile := config.Integration.RuntimeFile
	if runtimeFile == "" {
		runtimeFile = filepath.Join(config.ClashDir, "clash-verge.yaml")
	}
	var ports struct {
		Mixed int `yaml:"mixed-port"`
		HTTP  int `yaml:"port"`
	}
	if raw, err := fsx.ReadBounded(runtimeFile, 16<<20); err == nil && yaml.Unmarshal(raw, &ports) == nil {
		port := ports.Mixed
		if port == 0 {
			port = ports.HTTP
		}
		if port > 0 && port <= 65535 {
			proxy := &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(port)}
			transport.Proxy = func(req *http.Request) (*url.URL, error) {
				if env, err := http.ProxyFromEnvironment(req); env != nil || err != nil {
					return env, err
				}
				return proxy, nil
			}
		}
	}
	return &http.Client{Transport: transport}
}
