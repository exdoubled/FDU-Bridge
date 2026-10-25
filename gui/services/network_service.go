package services

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mythologyli/zju-connect/gui/internal/appconf"
	"github.com/mythologyli/zju-connect/gui/internal/clash"
	"github.com/mythologyli/zju-connect/gui/internal/netcheck"
	"github.com/mythologyli/zju-connect/routing"
)

type NetworkService struct {
	b             *Backend
	mu            sync.Mutex
	probeBusy     sync.Mutex
	started       int64
	coreEpoch     int64
	corePrevious  [2]routing.TrafficTotals
	totals        [2]TrafficBucket
	rates         [3]trafficRate
	clashTotals   [2]uint64
	clashPrevious map[string][2]uint64
	controllerMu  sync.Mutex
	controllerKey networkControllerKey
	controllerAPI clash.API
	diagnosisMu   sync.Mutex
	diagnosis     *CampusDiagnosis
	report        diagnosisReportSnapshot
}

type networkControllerKey struct {
	settings   appconf.Integration
	root, file string
	controller clash.ControlAPI
}

func NewNetworkService(b *Backend) *NetworkService {
	return &NetworkService{b: b, started: time.Now().UnixMilli(), clashPrevious: map[string][2]uint64{}}
}

type ProbeReport struct {
	Result netcheck.Result `json:"result"`
	Via    string          `json:"via"`
}

func (s *NetworkService) Targets() []netcheck.Target { return netcheck.Targets() }

// controller captures the selected local API without changing client selection.
func (s *NetworkService) controller() (clash.API, error) {
	s.b.mu.Lock()
	key := networkControllerKey{settings: s.b.config.Integration, root: s.b.config.ClashDir}
	if s.b.campus != nil && s.b.runtime.Running() {
		key.root = s.b.campus.Root
	}
	s.b.mu.Unlock()
	if key.settings.Mode == "proxy" {
		return nil, errors.New("仅本地代理，未连接 Clash 控制接口")
	}
	key.file, key.controller = s.b.conn.ControllerBinding()
	if key.settings.Mode == "manual" || !s.b.runtime.Running() && key.file != key.settings.RuntimeFile {
		key.file, key.controller = key.settings.RuntimeFile, clash.ControlAPI{}
	}
	s.controllerMu.Lock()
	defer s.controllerMu.Unlock()
	if key == s.controllerKey && s.controllerAPI != nil {
		return s.controllerAPI, nil
	}
	var api clash.API
	if s.b.conn.API != nil {
		api = s.b.conn.API
	} else if key.settings.Mode == "manual" || key.controller.Network == "" && key.file != "" {
		target, err := clash.ManualTarget(key.file, key.settings.Controller, key.settings.Secret)
		if err != nil {
			return nil, err
		}
		api = *target.Controller
	} else if key.controller.Network != "" {
		api = key.controller
	} else {
		api = s.b.conn.Controller(key.root)
	}
	if api == nil {
		return nil, clash.ErrControllerUnavailable
	}
	if local, ok := api.(clash.ControlAPI); ok {
		local.Timeout = 2 * time.Second
		api = local
	}
	s.controllerKey, s.controllerAPI = key, api
	return api, nil
}

func (s *NetworkService) Observe() (NetworkView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var observed clash.ConnectionSnapshot
	readFailed := false
	api, err := s.controller()
	if err == nil {
		err = api.Do("GET", "/connections", nil, &observed)
		if err != nil {
			readFailed = true
			s.forgetController()
		}
	}
	core, received := s.b.runtime.Traffic()
	now := time.Now()
	fresh := s.b.runtime.Running() && received > 0 && now.UnixMilli()-received < 5000
	s.b.mu.Lock()
	values := s.b.saved
	s.b.mu.Unlock()
	fallback := ""
	if route, ok := values["routing"].(map[string]any); ok {
		if upstream, e := url.Parse(text(route, "upstream_proxy")); e == nil {
			fallback = upstream.Port()
		}
	}
	view := s.sample(core, fresh, observed, err == nil, fallback, now)
	if readFailed {
		// The failed read may be the oversized or slow snapshot itself. Retry at
		// the slower cadence instead of repeating the heaviest request every second.
		view.PollIntervalMS = 5000
	}
	return view, nil
}

func (s *NetworkService) forgetController() {
	s.controllerMu.Lock()
	s.controllerAPI = nil
	s.controllerMu.Unlock()
}

func lookupClash(api clash.API) func(context.Context, string) ([]string, error) {
	return func(ctx context.Context, host string) ([]string, error) {
		var addresses []string
		lookupErr := errors.New("Clash DNS 未返回地址")
		for _, kind := range []string{"A", "AAAA"} {
			if err := ctx.Err(); err != nil {
				lookupErr = err
				break
			}
			var reply struct {
				Status int `json:"Status"`
				Answer []struct {
					Type int    `json:"type"`
					Data string `json:"data"`
				} `json:"Answer"`
			}
			path := "/dns/query?" + url.Values{"name": {host}, "type": {kind}}.Encode()
			if err := api.Do("GET", path, nil, &reply); err != nil {
				lookupErr = err
				continue
			}
			if reply.Status != 0 {
				lookupErr = fmt.Errorf("Clash DNS Rcode %d", reply.Status)
				continue
			}
			for _, answer := range reply.Answer {
				if (answer.Type == 1 || answer.Type == 28) && net.ParseIP(answer.Data) != nil {
					addresses = append(addresses, answer.Data)
				}
			}
		}
		if len(addresses) == 0 {
			return nil, lookupErr
		}
		return addresses, nil
	}
}

func (s *NetworkService) Probe(id string) (ProbeReport, error) {
	if !s.probeBusy.TryLock() {
		return ProbeReport{}, errBusy
	}
	defer s.probeBusy.Unlock()
	valid := false
	for _, t := range netcheck.Targets() {
		valid = valid || t.ID == id
	}
	if !valid {
		return ProbeReport{}, errors.New("不支持的检测目标")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	opts, via, err := s.probeOptions(s.b.runtime.Running())
	if err != nil {
		return ProbeReport{}, err
	}
	result, err := netcheck.Probe(ctx, strings.TrimSpace(id), opts)
	if opts.Lookup != nil {
		for i := range result.Checks {
			if result.Checks[i].Kind == "dns" {
				result.Checks[i].Detail = "Clash DNS 候选结果；" + result.Checks[i].Detail
			}
		}
	}
	return ProbeReport{Result: result, Via: via}, err
}

func (s *NetworkService) probeOptions(running bool) (netcheck.Options, string, error) {
	opts, via := netcheck.Options{}, "系统路由"
	api, controllerErr := s.controller()
	if controllerErr == nil {
		var config map[string]any
		if err := api.Do("GET", "/configs", nil, &config); err != nil {
			s.forgetController()
			return opts, via, err // Never silently change the path when Clash fails.
		}
		port, _ := config["mixed-port"].(float64)
		if port == 0 {
			port, _ = config["port"].(float64)
		}
		if port <= 0 || port > 65535 {
			return opts, via, errors.New("当前 Clash 未提供 HTTP/mixed 检测入口")
		}
		opts.ProxyURL, opts.Lookup = "http://127.0.0.1:"+strconv.Itoa(int(port)), lookupClash(api)
		via = "Clash 当前配置"
	} else if running {
		s.b.mu.Lock()
		bind := text(s.b.saved, "http_bind")
		s.b.mu.Unlock()
		host, _, err := net.SplitHostPort(bind)
		if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			return opts, via, errors.New("当前 FDU 未提供本机 HTTP 检测入口")
		}
		opts.ProxyURL, via = "http://"+bind, "FDU 本地入口"
	}
	return opts, via, nil
}
