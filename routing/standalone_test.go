package routing

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/mythologyli/zju-connect/configs"
)

func TestStandaloneRoutesAndNeverFallsBackFromCampus(t *testing.T) {
	for _, mode := range []string{"rule", "global"} {
		t.Run(mode, func(t *testing.T) {
			r, err := New(configs.RoutingConfig{UpstreamProxy: "direct", Mode: mode, Domains: []string{"campus.invalid"}})
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			directErr, vpnErr := errors.New("physical"), errors.New("vpn")
			directCalls := 0
			r.SetControl(func(context.Context, string, string) (net.Conn, error) { directCalls++; return nil, directErr }, []string{"login.invalid:443"}, []string{"127.0.0.1:1080"})
			for _, network := range []string{"tcp", "udp"} {
				if _, err := r.Dial(context.Background(), network, "campus.invalid:443"); !errors.Is(err, ErrVPNUnavailable) {
					t.Fatal(err)
				}
			}
			if directCalls != 0 {
				t.Fatal("offline campus leaked to direct")
			}
			r.SetVPN(func(context.Context, string, string) (net.Conn, error) { return nil, vpnErr })
			for _, network := range []string{"tcp", "udp"} {
				if _, err := r.Dial(context.Background(), network, "campus.invalid:443"); !errors.Is(err, vpnErr) {
					t.Fatal(err)
				}
				want := directErr
				if mode == "global" {
					want = vpnErr
				}
				if _, err := r.Dial(context.Background(), network, "public.invalid:443"); !errors.Is(err, want) {
					t.Fatalf("%s %s: %v", mode, network, err)
				}
			}
			if _, err := r.Dial(context.Background(), "tcp", "login.invalid:443"); !errors.Is(err, directErr) {
				t.Fatal("login must use physical underlay", err)
			}
			if _, err := r.Dial(context.Background(), "tcp", "127.0.0.1:1080"); err == nil || errors.Is(err, directErr) || errors.Is(err, vpnErr) {
				t.Fatal("own listener was not blocked")
			}
		})
	}
}

func TestStandaloneConfigurationCombinations(t *testing.T) {
	for _, tun := range []bool{false, true} {
		for _, mode := range []string{"rule", "global"} {
			c := configs.Default()
			c.Protocol, c.AutoDetectInterface, c.TUNMode = "atrust", true, tun
			c.SocksBind = "127.0.0.1:1080"
			c.HTTPBind = "127.0.0.1:1081"
			c.Routing = configs.RoutingConfig{Enabled: true, UpstreamProxy: "direct", Mode: mode}
			if err := Validate(c); err != nil {
				t.Fatalf("TUN=%v mode=%s: %v", tun, mode, err)
			}
			c.Routing.UpstreamProxy = "socks5://127.0.0.1:7902"
			if err := Validate(c); (err != nil) != (tun || mode == "global") {
				t.Fatalf("Clash mode accepted incompatible combination: %v", err)
			}
		}
	}
}
