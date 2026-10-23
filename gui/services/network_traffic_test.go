package services

import (
	"strconv"
	"testing"
	"time"

	"github.com/mythologyli/zju-connect/gui/internal/clash"
	"github.com/mythologyli/zju-connect/routing"
)

func TestNetworkAccountingExcludesRouterAndFallbackDuplicates(t *testing.T) {
	s := NewNetworkService(nil)
	core := routing.TrafficSnapshot{Started: 10, FDU: routing.TrafficTotals{Upload: 100}, NonFDU: routing.TrafficTotals{Upload: 200}}
	observed := clash.ConnectionSnapshot{Connections: []clash.ObservedConnection{
		{ID: "outer", Upload: 200, Chains: []string{"FDU-ROUTER"}},
		{ID: "fallback", Upload: 200, Chains: []string{"node-a"}, Metadata: clash.ConnectionMetadata{InboundName: "FDU-FALLBACK"}},
		{ID: "direct", Upload: 30, Start: time.Now().Add(time.Second), Chains: []string{"DIRECT"}},
	}}
	view := s.sample(core, true, observed, true, "17902", time.Now())
	if view.FDU.Upload != 100 || view.NonFDU.Upload != 230 {
		t.Fatalf("counted a proxy hop twice: %+v / %+v", view.FDU, view.NonFDU)
	}
	view = s.sample(core, true, observed, true, "17902", time.Now().Add(time.Second))
	if view.NonFDU.Upload != 230 {
		t.Fatal("recounted unchanged snapshot")
	}
}

func TestRetainedCoreTotalsAreCountedAfterStop(t *testing.T) {
	s := NewNetworkService(nil)
	core := routing.TrafficSnapshot{Started: 10, FDU: routing.TrafficTotals{Upload: 100}}
	s.sample(core, true, clash.ConnectionSnapshot{}, false, "", time.Now())
	core.FDU.Upload = 175
	view := s.sample(core, false, clash.ConnectionSnapshot{}, false, "", time.Now())
	if view.FDU.Upload != 175 || view.FDU.Connections != 0 {
		t.Fatalf("lost retained terminal totals: %+v", view.FDU)
	}
}

func TestMixedSelectorAndLargeSnapshotsDoNotLoseOrRecountTraffic(t *testing.T) {
	s := NewNetworkService(nil)
	observed := clash.ConnectionSnapshot{}
	for i := 0; i < 4097; i++ {
		observed.Connections = append(observed.Connections, clash.ObservedConnection{ID: strconv.Itoa(i), Upload: 1, Start: time.Now().Add(time.Second), Chains: []string{"node-a", "mixed-selector"}})
	}
	var view NetworkView
	for pass := 0; pass < 3; pass++ {
		view = s.sample(routing.TrafficSnapshot{}, false, observed, true, "", time.Now())
		for left, right := 0, len(observed.Connections)-1; left < right; left, right = left+1, right-1 {
			observed.Connections[left], observed.Connections[right] = observed.Connections[right], observed.Connections[left]
		}
	}
	if view.NonFDU.Upload != 4097 {
		t.Fatalf("misclassified a selected external node or recounted a changing subset: %d", view.NonFDU.Upload)
	}
}

func TestNetworkRowsAreAllowlistedAndFakeIPsAreLabelled(t *testing.T) {
	s := NewNetworkService(nil)
	observed := clash.ConnectionSnapshot{Connections: []clash.ObservedConnection{
		{ID: "ai", Chains: []string{"node-a"}, Metadata: clash.ConnectionMetadata{Host: "api.openai.com", DestinationIP: "198.18.0.7", Network: "tcp"}},
		{ID: "other", Chains: []string{"DIRECT"}, Metadata: clash.ConnectionMetadata{Host: "private.example", DestinationIP: "1.2.3.4"}},
		{ID: "spoof", Chains: []string{"DIRECT"}, Metadata: clash.ConnectionMetadata{Host: "api.openai.com.evil.example"}},
	}}
	view := s.sample(routing.TrafficSnapshot{}, false, observed, true, "", time.Now())
	if len(view.Connections) != 1 || view.Connections[0].IPKind != "fake" || view.Connections[0].Route != "non-fdu" {
		t.Fatalf("incorrect allowlist/IP classification: %+v", view.Connections)
	}
}
