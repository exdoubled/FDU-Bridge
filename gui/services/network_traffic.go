package services

import (
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/mythologyli/zju-connect/gui/internal/clash"
	"github.com/mythologyli/zju-connect/routing"
)

type TrafficBucket struct {
	Upload       uint64 `json:"upload"`
	Download     uint64 `json:"download"`
	UploadRate   uint64 `json:"uploadRate"`
	DownloadRate uint64 `json:"downloadRate"`
	Connections  int    `json:"connections"`
}

type NetworkConnection struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Host     string `json:"host"`
	Port     string `json:"port"`
	Network  string `json:"network"`
	Route    string `json:"route"`
	TargetIP string `json:"targetIP"`
	IPKind   string `json:"ipKind"`
	Source   string `json:"source"`
	Node     string `json:"node"`
	DNSMode  string `json:"dnsMode"`
	Upload   uint64 `json:"upload"`
	Download uint64 `json:"download"`
	Started  int64  `json:"started"`
}

type NetworkView struct {
	FDU            TrafficBucket       `json:"fdu"`
	NonFDU         TrafficBucket       `json:"nonFdu"`
	Connections    []NetworkConnection `json:"connections"`
	CoreAvailable  bool                `json:"coreAvailable"`
	ClashAvailable bool                `json:"clashAvailable"`
	Truncated      bool                `json:"truncated"`
	Scope          string              `json:"scope"`
	SampledAt      int64               `json:"sampledAt"`
}

func counterDelta(value, previous uint64) uint64 {
	if value < previous {
		return value
	}
	return value - previous
}

func routerHop(c clash.ObservedConnection) bool {
	for _, name := range c.Chains {
		if name == "FDU-ROUTER" {
			return true
		}
	}
	return false
}

func fallbackHop(c clash.ObservedConnection, port string) bool {
	return strings.EqualFold(c.Metadata.InboundName, "FDU-FALLBACK") || port != "" && c.Metadata.InboundPort.String() == port
}

func connectionHost(c clash.ObservedConnection) string {
	host := c.Metadata.Host
	if host == "" {
		host = c.Metadata.SniffHost
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

func diagnosticIP(raw, mode string) (string, string) {
	ip, err := netip.ParseAddr(raw)
	if err != nil || ip.IsLoopback() || ip.IsUnspecified() {
		return "", "unknown"
	}
	ip = ip.Unmap()
	if strings.Contains(strings.ToLower(mode), "fake") || netip.MustParsePrefix("198.18.0.0/15").Contains(ip) {
		return ip.String(), "fake"
	}
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return "", "unknown"
	}
	return ip.String(), "actual"
}

// sample counts each logical core flow once. Clash's original FDU-bound flow
// and its internal return listener are excluded, because the core already counts them.
func (s *NetworkService) sample(core routing.TrafficSnapshot, coreFresh bool, observed clash.ConnectionSnapshot, clashOK bool, fallback string, now time.Time) NetworkView {
	view := NetworkView{CoreAvailable: coreFresh, ClashAvailable: clashOK, Connections: []NetworkConnection{}, SampledAt: now.UnixMilli(), Truncated: core.Truncated}
	coreKeys := map[string]bool{}
	if core.Started > 0 {
		if core.Started != s.coreEpoch {
			s.coreEpoch, s.corePrevious = core.Started, [2]routing.TrafficTotals{}
		}
		for i, current := range []routing.TrafficTotals{core.FDU, core.NonFDU} {
			s.totals[i].Upload += counterDelta(current.Upload, s.corePrevious[i].Upload)
			s.totals[i].Download += counterDelta(current.Download, s.corePrevious[i].Download)
			s.corePrevious[i] = current
		}
	}
	if coreFresh {
		for _, c := range core.Connections {
			provider := routing.AIProvider(c.Host)
			if provider == "" || c.Route != "fdu" && c.Route != "non-fdu" {
				continue
			}
			ip, kind := diagnosticIP(c.TargetIP, "")
			node := "FDU"
			if c.Route == "non-fdu" {
				node = "Clash"
			}
			view.Connections = append(view.Connections, NetworkConnection{ID: "core:" + c.ID, Provider: provider, Host: c.Host, Port: c.Port, Network: c.Network, Route: c.Route, TargetIP: ip, IPKind: kind, Source: "FDU", Node: node, Upload: c.Upload, Download: c.Download, Started: c.Started})
			coreKeys[c.Host+":"+c.Port+"/"+c.Network] = true
		}
	}
	plainActive := 0
	if clashOK {
		next := map[string][2]uint64{}
		// The controller response already has a 4 MiB bound. Preserve every
		// active baseline instead of recounting connections when a subset changes.
		for _, c := range observed.Connections {
			if fallbackHop(c, fallback) {
				continue
			}
			hop := routerHop(c)
			if !hop && len(c.Chains) != 0 {
				plainActive++
				previous, seen := s.clashPrevious[c.ID]
				current := [2]uint64{c.Upload, c.Download}
				// Existing long-lived connections start at their first observed baseline.
				if !seen && (c.Start.IsZero() || c.Start.UnixMilli() < s.started) {
					previous = current
				}
				s.totals[1].Upload += counterDelta(current[0], previous[0])
				s.totals[1].Download += counterDelta(current[1], previous[1])
				next[c.ID] = current
			}
			host, port := connectionHost(c), c.Metadata.DestinationPort.String()
			provider := routing.AIProvider(host)
			if provider == "" || hop && coreKeys[host+":"+port+"/"+c.Metadata.Network] {
				continue
			}
			route, node := "unknown", ""
			if !hop && len(c.Chains) > 0 {
				route, node = "non-fdu", c.Chains[0]
			}
			ip, kind := diagnosticIP(c.Metadata.DestinationIP, c.Metadata.DNSMode)
			if hop {
				// An address before FDU's resolver is not proof of the VPN target.
				ip, kind = "", "unknown"
			}
			view.Connections = append(view.Connections, NetworkConnection{ID: "clash:" + c.ID, Provider: provider, Host: host, Port: port, Network: c.Metadata.Network, Route: route, TargetIP: ip, IPKind: kind, Source: "Clash", Node: node, DNSMode: c.Metadata.DNSMode, Upload: c.Upload, Download: c.Download, Started: c.Start.UnixMilli()})
		}
		s.clashPrevious = next
	}
	view.FDU, view.NonFDU = s.totals[0], s.totals[1]
	if coreFresh {
		view.FDU.Connections, view.NonFDU.Connections = core.FDU.Connections, core.NonFDU.Connections
	}
	view.NonFDU.Connections += plainActive
	seconds := now.Sub(s.lastSample).Seconds()
	if seconds > 0 && seconds < 5 {
		for i, bucket := range []*TrafficBucket{&view.FDU, &view.NonFDU} {
			if (i == 0 && coreFresh) || (i == 1 && (coreFresh || clashOK)) {
				bucket.UploadRate = uint64(float64(counterDelta(bucket.Upload, s.previousRates[i][0])) / seconds)
				bucket.DownloadRate = uint64(float64(counterDelta(bucket.Download, s.previousRates[i][1])) / seconds)
			}
		}
	}
	s.previousRates = [2][2]uint64{{view.FDU.Upload, view.FDU.Download}, {view.NonFDU.Upload, view.NonFDU.Download}}
	s.lastSample = now
	view.Scope = "统计当前 FDU 与 Clash；Clash 非 FDU 流量为采样估算，短连接可能漏采。"
	if !coreFresh {
		view.Scope += " FDU 核心统计暂不可用。"
	}
	if !clashOK {
		view.Scope += " Clash 未连接，覆盖不完整。"
	}
	sort.Slice(view.Connections, func(i, j int) bool { return view.Connections[i].Started > view.Connections[j].Started })
	if len(view.Connections) > 128 {
		view.Connections, view.Truncated = view.Connections[:128], true
	}
	return view
}
