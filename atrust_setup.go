//go:build !tun

package main

import (
	"os"
	"time"

	atrustclient "github.com/mythologyli/zju-connect/client/atrust"
	"github.com/mythologyli/zju-connect/client/atrust/auth"
	"github.com/mythologyli/zju-connect/configs"
)

// aTrustSetupOptions shares authentication inputs without owning either mode's
// lifecycle or changing how it handles errors while reading the cache files.
func aTrustSetupOptions(c configs.Config, resourceData, clientData []byte) (atrustclient.SetupOptions, error) {
	return aTrustSetupOptionsForSession(c, resourceData, clientData, guiFreshLogin(), false)
}

func guiFreshLogin() bool {
	return os.Getenv("FDU_CONNECT_GUI") == "1" && os.Getenv("FDU_CONNECT_GUI_RELOGIN") == "1"
}

func aTrustSetupOptionsForSession(c configs.Config, resourceData, clientData []byte, fresh, resumeOnly bool) (atrustclient.SetupOptions, error) {
	options := atrustclient.SetupOptions{
		ServerAddress: c.ServerAddress, ServerPort: c.ServerPort,
		TOTPSecret: c.TOTPSecret, ClientData: clientData, ResourceData: resourceData,
		BestNodesRefreshInterval: time.Duration(c.UpdateBestNodesInterval) * time.Second,
		SessionRefreshInterval:   time.Duration(c.SessionRefreshInterval) * time.Second,
		ForceLogin:               fresh, ResumeOnly: resumeOnly,
	}
	if !resumeOnly && (fresh || c.SID == "" || c.DeviceID == "" || resourceData == nil) {
		var err error
		options.LoginMethod, err = auth.NewLoginMethod(auth.LoginMethodOptions{
			AuthType: c.AuthType, Username: c.Username, Password: c.Password,
			Phone: c.Phone, Domain: c.LoginDomain, GraphCodeFile: c.GraphCodeFile,
			CASTicket: c.CasTicket, OAuth2Code: c.OAuth2Code,
		})
		if err != nil {
			return options, err
		}
	}
	if c.ClientDataFile != "" {
		options.SaveClientData = func(data []byte) error { return os.WriteFile(c.ClientDataFile, data, 0600) }
	}
	return options, nil
}
