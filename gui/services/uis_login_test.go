package services

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mythologyli/zju-connect/gui/internal/clashsetup"
)

func TestUISCredentialsAutosaveAndReload(t *testing.T) {
	b := startupBackend(t)
	source := filepath.Join(b.Dir, "example.toml")
	if err := os.WriteFile(source, []byte("protocol='atrust'\nserver_address='vpn.fudan.edu.cn'\nauth_type='auth/httpsOauth2'\nlogin_domain='id.fudan.edu.cn'\nhttp_bind='127.0.0.1:1081'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	view, err := b.workspace.Load(source, false)
	if err != nil {
		t.Fatal(err)
	}
	b.setView(view)
	s := NewSettingsService(b)
	var wg sync.WaitGroup
	for key, value := range map[string]string{"username": " student ", "password": " password with spaces ", "totp_secret": "jbsw y3dp ehpk 3pxp"} {
		wg.Go(func() {
			if _, err := s.SaveUISCredential(key, value); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	reloaded, err := clashsetup.ReadSettings(b.workspace.Target, b.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"username": "student", "password": " password with spaces ", "totp_secret": "JBSWY3DPEHPK3PXP", "http_bind": "127.0.0.1:1081", "auth_type": "auth/httpsOauth2"} {
		if reloaded.Values[key] != want || b.saved[key] != want {
			t.Errorf("%s did not persist correctly", key)
		}
	}
	before, _ := os.ReadFile(b.workspace.Target)
	for _, pair := range [][2]string{{"totp_secret", "123456"}, {"server_address", "other.example"}} {
		if _, err := s.SaveUISCredential(pair[0], pair[1]); err == nil {
			t.Error("accepted invalid credential update")
		}
	}
	b.starting = true
	if _, err := s.SaveUISCredential("password", "changed"); err == nil {
		t.Error("changed credentials while connecting")
	}
	b.starting = false
	after, _ := os.ReadFile(b.workspace.Target)
	if string(before) != string(after) {
		t.Fatal("rejected updates changed config")
	}
	if _, err := s.SaveUISCredential("totp_secret", ""); err != nil {
		t.Fatal(err)
	}
	if b.saved["totp_secret"] != "" {
		t.Fatal("could not clear saved secret")
	}
}
