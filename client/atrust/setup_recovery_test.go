package atrust

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mythologyli/zju-connect/client/atrust/auth"
)

func TestResumeOnlyExpiredCookiesNeverStartLogin(t *testing.T) {
	var authRequests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/public/manifest":
			fmt.Fprint(w, `{"code":0,"data":{}}`)
		case "/passport/v1/public/authConfig":
			authRequests.Add(1)
			if r.URL.Query().Get("needTicket") != "0" {
				t.Error("passive resume requested an interactive login ticket")
			}
			fmt.Fprint(w, `{"code":0,"data":{"isLogin":0,"authServerInfoList":[{"authType":"auth/httpsOauth2","loginDomain":"campus","loginUrl":"https://never-open.invalid"}]}}`)
		default:
			t.Errorf("passive resume attempted authentication endpoint %s", r.URL.Path)
			http.Error(w, "unexpected request", 500)
		}
	}))
	defer server.Close()
	vpn := NewClient(ClientOptions{Session: SessionOptions{DeviceID: "device", SID: "expired"}, UnderlayDialer: setupTestDialer{server.Listener.Addr().String()}})
	defer vpn.Close()
	_, err := vpn.SetupContext(context.Background(), SetupOptions{
		ServerAddress: strings.TrimPrefix(server.URL, "https://"), ServerPort: 443,
		ResumeOnly: true, ResourceData: []byte("unused-resource-cache"),
		LoginMethod: auth.HTTPSOauth2Login{Domain: "campus"},
	})
	if !errors.Is(err, auth.ErrSessionInvalid) || authRequests.Load() != 1 {
		t.Fatalf("passive resume = %v, authConfig calls = %d", err, authRequests.Load())
	}
}

func TestForceLoginBypassesOnlySessionCredentialsAndDoesNotSaveFailure(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("sid"); err == nil {
			t.Error("fresh login reused the old SID cookie")
		}
		if r.URL.Path == "/public/manifest" {
			fmt.Fprint(w, `{"code":0,"data":{}}`)
			return
		}
		fmt.Fprint(w, `{"code":0,"data":{"isLogin":0}}`)
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "https://")
	data, err := json.Marshal(auth.ClientAuthData{
		DeviceID: "persistent-device", ServerVersionInfo: json.RawMessage(`{"code":0,"data":{}}`),
		Cookies: []auth.Cookie{{Host: host, Scheme: "https", Name: "sid", Value: "old-cookie"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	original := append([]byte(nil), data...)
	vpn := NewClient(ClientOptions{Session: SessionOptions{DeviceID: "persistent-device", SID: "old-sid", SignKey: "old-signkey"}, UnderlayDialer: setupTestDialer{server.Listener.Addr().String()}})
	defer vpn.Close()
	saves := 0
	_, err = vpn.SetupContext(context.Background(), SetupOptions{
		ServerAddress: host, ServerPort: 443, ForceLogin: true,
		ClientData: data, ResourceData: []byte("old-resource-data"),
		SaveClientData: func([]byte) error { saves++; return nil },
	})
	if err == nil || saves != 0 || vpn.SID != "" || vpn.SignKey == "old-signkey" || vpn.DeviceID != "persistent-device" || !bytes.Equal(data, original) {
		t.Fatalf("failed fresh login = %v, saves=%d, SID=%q, preserved device=%t, preserved input=%t", err, saves, vpn.SID, vpn.DeviceID == "persistent-device", bytes.Equal(data, original))
	}
}

func TestAuthenticatedCookiesRemainAvailableWhenResourceFetchFails(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/public/manifest" {
			fmt.Fprint(w, `{"code":0,"data":{}}`)
			return
		}
		if r.URL.Path == "/controller/v1/user/clientResource" {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close() // Simulate transport loss immediately after auth success.
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "new-sid", Path: "/"})
		fmt.Fprint(w, `{"code":0,"data":{"isLogin":1}}`)
	}))
	defer server.Close()
	vpn := NewClient(ClientOptions{Session: SessionOptions{DeviceID: "device", SID: "old-sid"}, UnderlayDialer: setupTestDialer{server.Listener.Addr().String()}})
	defer vpn.Close()
	var authenticated []byte
	saves := 0
	_, err := vpn.SetupContext(context.Background(), SetupOptions{
		ServerAddress: strings.TrimPrefix(server.URL, "https://"), ServerPort: 443,
		ForceLogin:      true,
		OnAuthenticated: func(data []byte) { authenticated = append([]byte(nil), data...) },
		SaveClientData:  func([]byte) error { saves++; return nil },
	})
	var data auth.ClientAuthData
	if decodeErr := json.Unmarshal(authenticated, &data); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	var networkError net.Error
	if !errors.As(err, &networkError) || saves != 0 || len(data.Cookies) != 1 || data.Cookies[0].Value != "new-sid" {
		t.Fatalf("resource fetch failure = %v, saves=%d, in-memory cookies=%v", err, saves, data.Cookies)
	}
}

func TestTransportStateReportsLossAndFullRestoration(t *testing.T) {
	states := make(chan bool, 4)
	vpn := NewClient(ClientOptions{OnConnectionState: func(connected bool) { states <- connected }})
	defer vpn.Close()
	vpn.setTransportReconnecting("a", false)
	vpn.setTransportReconnecting("a", true)
	if connected := <-states; connected {
		t.Fatal("lost transport reported connected")
	}
	vpn.setTransportReconnecting("a", true)
	vpn.setTransportReconnecting("b", true)
	vpn.setTransportReconnecting("a", false)
	vpn.setTransportReconnecting("b", false)
	if connected := <-states; !connected {
		t.Fatal("full restoration reported disconnected")
	}
}

func TestAuthenticationSnapshotNotifiesOnlyWhenCredentialsChange(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprint(changed), func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/public/manifest":
					fmt.Fprint(w, `{"code":0,"data":{}}`)
				case "/controller/v1/user/clientResource":
					if changed {
						http.SetCookie(w, &http.Cookie{Name: "sid", Value: "latest", Path: "/"})
					}
					fmt.Fprint(w, `{}`)
				default:
					http.SetCookie(w, &http.Cookie{Name: "sid", Value: "authenticated", Path: "/"})
					fmt.Fprint(w, `{"code":0,"data":{"isLogin":1}}`)
				}
			}))
			defer server.Close()
			vpn := NewClient(ClientOptions{UnderlayDialer: setupTestDialer{server.Listener.Addr().String()}})
			defer vpn.Close()
			var snapshots [][]byte
			_, _ = vpn.SetupContext(t.Context(), SetupOptions{
				ServerAddress: strings.TrimPrefix(server.URL, "https://"), ServerPort: 443, ForceLogin: true,
				OnAuthenticated: func(data []byte) { snapshots = append(snapshots, append([]byte(nil), data...)) },
			})
			want := 1
			if changed {
				want = 2
			}
			if len(snapshots) != want {
				t.Fatalf("authentication snapshots=%d, want %d", len(snapshots), want)
			}
		})
	}
}

func TestTransportReadinessReplaysAnEarlierLoss(t *testing.T) {
	states := make(chan bool, 4)
	vpn := NewClient(ClientOptions{OnConnectionState: func(connected bool) { states <- connected }})
	defer vpn.Close()
	vpn.setTransportReconnecting("main", true)
	<-states // The owner was not yet ready and ignored this notification.
	vpn.PublishConnectionState()
	select {
	case connected := <-states:
		if connected {
			t.Fatal("readiness hid an existing transport loss")
		}
	case <-time.After(time.Second):
		t.Fatal("readiness did not publish current transport state")
	}
}

type setupTestDialer struct{ address string }

func (d setupTestDialer) DialContext(ctx context.Context, network, _ string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, d.address)
}
func (setupTestDialer) ExcludeIP(net.IP) {}
