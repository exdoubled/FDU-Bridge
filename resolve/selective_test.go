package resolve

import (
	"context"
	"errors"
	"github.com/mythologyli/zju-connect/client"
	"net"
	"sync/atomic"
	"testing"
)

func failingNetResolver() *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("DNS unavailable")
		},
	}
}

func TestVPNResolverNeverUsesSecondaryOrSystemDNS(t *testing.T) {
	for _, remote := range []bool{true, false} {
		r := NewVPNResolver(nil, "10.0.0.53", 60, client.DomainResources{}, nil, remote)
		r.remoteUDPResolver = failingNetResolver()
		r.remoteTCPResolver = failingNetResolver()
		var calls atomic.Int32
		r.secondaryResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
			calls.Add(1)
			return nil, errors.New("secondary must not be used")
		}}
		_, _, err := r.Resolve(context.Background(), "selected.invalid")
		if err == nil || calls.Load() != 0 {
			t.Fatalf("remote=%v: secondary calls=%d error=%v", remote, calls.Load(), err)
		}
		r.SetPermanentDNS("static.invalid", net.IPv4(10, 0, 0, 7))
		if _, ip, err := r.Resolve(context.Background(), "static.invalid"); err != nil || !ip.Equal(net.IPv4(10, 0, 0, 7)) {
			t.Fatalf("static answer lost: %s %v", ip, err)
		}
		r.Close()
	}
}
