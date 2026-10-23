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

	"github.com/mythologyli/zju-connect/gui/internal/clash"
	"github.com/mythologyli/zju-connect/gui/internal/netcheck"
)

type NetworkService struct {
	b         *Backend
	probeBusy sync.Mutex
}

func NewNetworkService(b *Backend) *NetworkService {
	return &NetworkService{b: b}
}

type ProbeReport struct {
	Result netcheck.Result `json:"result"`
	Via    string          `json:"via"`
}

func (s *NetworkService) Targets() []netcheck.Target { return netcheck.Targets() }

// controller captures the selected local API without changing client selection.
func (s *NetworkService) controller() (clash.API, error) {
	if !s.b.clashBusy.TryLock() {
		return nil, errBusy
	}
	defer s.b.clashBusy.Unlock()
	if s.b.proxyOnly() {
		return nil, errors.New("仅本地代理，未连接 Clash 控制接口")
	}
	root, err := s.b.campusRoot()
	if err != nil {
		return nil, err
	}
	api := s.b.conn.Controller(root)
	if api == nil {
		return nil, clash.ErrControllerUnavailable
	}
	if local, ok := api.(clash.ControlAPI); ok {
		local.Timeout = 2 * time.Second
		api = local
	}
	return api, nil
}

func lookupClash(api clash.API) func(context.Context, string) ([]string, error) {
	return func(ctx context.Context, host string) ([]string, error) {
		var addresses []string
		for _, kind := range []string{"A", "AAAA"} {
			if err := ctx.Err(); err != nil {
				return addresses, err
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
				return addresses, err
			}
			if reply.Status != 0 {
				return addresses, fmt.Errorf("Clash DNS Rcode %d", reply.Status)
			}
			for _, answer := range reply.Answer {
				if (answer.Type == 1 || answer.Type == 28) && net.ParseIP(answer.Data) != nil {
					addresses = append(addresses, answer.Data)
				}
			}
		}
		if len(addresses) == 0 {
			return nil, errors.New("Clash DNS 未返回地址")
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
	opts, via := netcheck.Options{}, "系统路由"
	api, controllerErr := s.controller()
	if controllerErr == nil {
		var config map[string]any
		if err := api.Do("GET", "/configs", nil, &config); err != nil {
			return ProbeReport{}, err // Never silently change the path when Clash fails.
		}
		port, _ := config["mixed-port"].(float64)
		if port == 0 {
			port, _ = config["port"].(float64)
		}
		if port <= 0 || port > 65535 {
			return ProbeReport{}, errors.New("当前 Clash 未提供 HTTP/mixed 检测入口")
		}
		opts.ProxyURL, opts.Lookup = "http://127.0.0.1:"+strconv.Itoa(int(port)), lookupClash(api)
		via = "Clash 当前配置"
	} else if errors.Is(controllerErr, errBusy) {
		return ProbeReport{}, controllerErr
	} else if s.b.runtime.Running() {
		s.b.mu.Lock()
		bind := text(s.b.saved, "http_bind")
		s.b.mu.Unlock()
		host, _, err := net.SplitHostPort(bind)
		if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			return ProbeReport{}, errors.New("当前 FDU 未提供本机 HTTP 检测入口")
		}
		opts.ProxyURL, via = "http://"+bind, "FDU 本地入口"
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
