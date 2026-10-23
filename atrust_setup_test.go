//go:build !tun

package main

import (
	"testing"

	"github.com/mythologyli/zju-connect/configs"
)

func TestFreshLoginEnvironmentRequiresOwnedGUI(t *testing.T) {
	c := configs.Config{SID: "sid", DeviceID: "device", AuthType: "auth/httpsOauth2", LoginDomain: "campus"}
	t.Setenv("FDU_CONNECT_GUI_RELOGIN", "1")
	for _, owned := range []bool{false, true} {
		t.Setenv("FDU_CONNECT_GUI", "0")
		if owned {
			t.Setenv("FDU_CONNECT_GUI", "1")
		}
		options, err := aTrustSetupOptions(c, []byte("cached-resource"), nil)
		if err != nil {
			t.Fatal(err)
		}
		if options.ForceLogin != owned || (options.LoginMethod != nil) != owned {
			t.Fatalf("owned=%t: fresh=%t, login method=%T", owned, options.ForceLogin, options.LoginMethod)
		}
	}
}
