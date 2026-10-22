package services

import (
	"testing"

	"github.com/mythologyli/zju-connect/gui/internal/clash"
)

func TestFollowingClientTracksItsCoreInsteadOfAnotherApp(t *testing.T) {
	old := clash.ClientTarget{Kind: "runtime", RuntimeFile: "A.yaml", Executable: "client/mihomo"}
	next := clash.ClientTarget{Kind: "runtime", RuntimeFile: "B.yaml", Executable: old.Executable, Controller: &clash.ControlAPI{Address: "127.0.0.1:9091", Secret: "new"}}
	other := clash.ClientTarget{Kind: "runtime", RuntimeFile: "other.yaml", Executable: "other/mihomo"}
	got, err := followingClient(old, []clash.ClientTarget{other, next})
	if err != nil || got.RuntimeFile != next.RuntimeFile || got.Controller.Secret != "new" {
		t.Fatalf("stale binding: %+v %v", got, err)
	}
	if _, err := followingClient(old, []clash.ClientTarget{other}); err == nil {
		t.Fatal("followed unrelated app")
	}
	if _, err := followingClient(old, []clash.ClientTarget{old, next}); err == nil {
		t.Fatal("accepted ambiguous instances")
	}
}
