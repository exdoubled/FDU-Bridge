package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mythologyli/zju-connect/client/authchallenge"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestInteractiveCASUsesExternalLoginChallenge(t *testing.T) {
	session := NewSession("vpn.example.com", nil)
	session.challengeHandler = authchallenge.HandlerFuncs{
		ExternalLogin: func(challenge authchallenge.ExternalLoginChallenge) (authchallenge.ExternalLoginResponse, error) {
			if challenge.Kind != authchallenge.ExternalLoginCAS || challenge.LoginURL != "https://idp.example/login" {
				t.Fatalf("unexpected challenge: %+v", challenge)
			}
			return authchallenge.ExternalLoginResponse{
				CallbackURL: "https://vpn.example.com/passport/v1/auth/cas?ticket=cas-ticket",
			}, nil
		},
	}
	callback, err := session.interactiveCas("https://idp.example/login")
	if err != nil {
		t.Fatal(err)
	}
	if callback != "https://vpn.example.com/passport/v1/auth/cas?ticket=cas-ticket" {
		t.Fatalf("callback = %q", callback)
	}
}

func TestSecondarySMSUsesStructuredChallengeResponse(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/passport/v1/auth/sms" || r.URL.Query().Get("action") != "checkcode" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		var payload struct {
			Code              string `json:"code"`
			SkipSecondaryAuth string `json:"skipSecondaryAuth"`
			AuthID            string `json:"authId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.Code != "654321" || payload.SkipSecondaryAuth != "1" || payload.AuthID != "sms-id" {
			t.Errorf("unexpected payload: %+v", payload)
		}
		fmt.Fprint(w, `{"code":0,"data":{}}`)
	}))
	defer server.Close()

	session := newTLSTestSession(server)
	session.challengeHandler = authchallenge.HandlerFuncs{
		Code: func(challenge authchallenge.CodeChallenge) (authchallenge.CodeResponse, error) {
			if challenge.Kind != authchallenge.CodeSMS || !challenge.CanSkipSecondaryAuth {
				t.Fatalf("unexpected challenge: %+v", challenge)
			}
			return authchallenge.CodeResponse{Code: "654321", SkipSecondaryAuth: true}, nil
		},
	}
	step, err := session.smsCheckCode(authStep{Service: "auth/sms", AuthID: "sms-id", SMSMode: smsWithAuthID})
	if err != nil {
		t.Fatal(err)
	}
	if step.Service != "" {
		t.Fatalf("unexpected next step: %+v", step)
	}
}

func TestInteractiveOAuthUsesExternalLoginChallenge(t *testing.T) {
	session := NewSession("vpn.example.com", nil)
	session.challengeHandler = authchallenge.HandlerFuncs{
		ExternalLogin: func(challenge authchallenge.ExternalLoginChallenge) (authchallenge.ExternalLoginResponse, error) {
			if challenge.Kind != authchallenge.ExternalLoginOAuth2 || challenge.LoginURL != "https://idp.example/oauth" {
				t.Fatalf("unexpected challenge: %+v", challenge)
			}
			if challenge.CallbackURL != "https://vpn.example.com/passport/v1/auth/httpsOauth2" {
				t.Fatalf("missing expected callback endpoint: %q", challenge.CallbackURL)
			}
			return authchallenge.ExternalLoginResponse{
				CallbackURL: "https://vpn.example.com/passport/v1/auth/httpsOauth2?code=oauth-code",
			}, nil
		},
	}
	callback, err := session.interactiveHttpsOauth2("https://idp.example/oauth")
	if err != nil {
		t.Fatal(err)
	}
	if callback != "https://vpn.example.com/passport/v1/auth/httpsOauth2?code=oauth-code" {
		t.Fatalf("callback = %q", callback)
	}
}

func TestRefreshUsesLatestCookiesAndCSRF(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/passport/v1/public/authConfig" || r.URL.Query().Get("needTicket") != "0" || r.URL.Query().Has("mod") {
			t.Errorf("unexpected refresh request: %s %s", r.Method, r.URL)
		}
		cookie, err := r.Cookie("sid")
		if err != nil || cookie.Value != fmt.Sprintf("sid-%d", calls-1) {
			t.Errorf("cookie = %v, %v", cookie, err)
		}
		if got := r.Header.Get("x-csrf-token"); got != fmt.Sprintf("csrf-%d", calls-1) {
			t.Errorf("csrf = %q", got)
		}
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: fmt.Sprintf("sid-%d", calls), Path: "/"})
		fmt.Fprintf(w, `{"code":0,"data":{"isLogin":1,"security":{"csrfToken":"csrf-%d"}}}`, calls)
	}))
	defer server.Close()
	s := NewSession(strings.TrimPrefix(server.URL, "https://"), nil)
	s.Restore("device", "sid-0", nil)
	s.csrfToken = "csrf-0"
	for i := 1; i <= 2; i++ {
		result, err := s.Refresh(context.Background())
		if err != nil || result.SID != fmt.Sprintf("sid-%d", i) {
			t.Fatalf("refresh %d = %+v, %v", i, result, err)
		}
		if len(result.Cookies) != 1 || result.Cookies[0].Value != result.SID {
			t.Fatalf("persisted cookies = %+v", result.Cookies)
		}
	}
}

func TestRefreshRejectsInvalidResponses(t *testing.T) {
	for _, tt := range []struct {
		name      string
		status    int
		body      string
		deleteSID bool
		invalid   bool
	}{
		{"logged out", 200, `{"code":0,"data":{"isLogin":0}}`, false, true},
		{"invalid SID", 200, `{"code":10000004}`, false, true},
		{"expired session", 200, `{"code":75500002}`, false, true},
		{"unauthorized", 401, `{}`, false, true},
		{"forbidden", 403, `{}`, false, true},
		{"server error", 503, `{"code":0,"data":{"isLogin":1}}`, false, false},
		{"business error", 200, `{"code":123,"data":{"isLogin":1}}`, false, false},
		{"missing code", 200, `{"data":{"isLogin":1}}`, false, false},
		{"missing login state", 200, `{"code":0,"data":{}}`, false, false},
		{"bad JSON", 200, `{`, false, false},
		{"deleted cookie", 200, `{"code":0,"data":{"isLogin":1}}`, true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.deleteSID {
					http.SetCookie(w, &http.Cookie{Name: "sid", Path: "/", MaxAge: -1})
				}
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()
			s := NewSession(strings.TrimPrefix(server.URL, "https://"), nil)
			s.Restore("device", "old", nil)
			result, err := s.Refresh(context.Background())
			if err == nil || result.SID != "" || errors.Is(err, ErrSessionInvalid) != tt.invalid {
				t.Fatalf("refresh = %+v, %v", result, err)
			}
		})
	}
}

func TestSnapshotAfterResourceResponseAndRestorePrecedence(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("sid")
		if err != nil || cookie.Value != "explicit" {
			t.Errorf("cookie = %v, %v", cookie, err)
		}
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "resource-sid", Path: "/"})
		fmt.Fprint(w, `{"code":0,"data":{}}`)
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "https://")
	s := NewSession(host, nil)
	s.Restore("device", "explicit", []Cookie{{Host: host, Scheme: "https", Name: "sid", Value: "stale"}})
	if _, err := s.ClientResource(); err != nil {
		t.Fatal(err)
	}
	result, err := s.Snapshot()
	if err != nil || result.SID != "resource-sid" {
		t.Fatalf("snapshot = %+v, %v", result, err)
	}
}

func TestRefreshCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	s := NewSession(strings.TrimPrefix(server.URL, "https://"), nil)
	s.Restore("device", "sid", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.Refresh(ctx); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request did not arrive")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("refresh did not cancel")
	}
}

func newTLSTestSession(server *httptest.Server) *Session {
	return NewSession(strings.TrimPrefix(server.URL, "https://"), nil)
}

func TestAuthenticationTLSAcceptsSelfSignedCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
	}))
	defer server.Close()

	session := newTLSTestSession(server)
	if _, err := session.accessCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestAuthConfigAcceptsMatchingAntiMITMCertificate(t *testing.T) {
	var encodedCertificate string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"code":0,"data":{"antiMITMAttackData":%s}}`, signedAntiMITMJSON(t, 1, encodedCertificate, ""))
	}))
	defer server.Close()
	encodedCertificate = base64.StdEncoding.EncodeToString(server.Certificate().Raw)

	session := newTLSTestSession(server)
	if _, _, err := session.authConfig(false, true); err != nil {
		t.Fatal(err)
	}
}

func TestAuthConfigReportsButDoesNotPropagateSecondStageFailure(t *testing.T) {
	var encodedCertificate string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/controller/v1/public/antiMITMRequest" {
			_, _ = w.Write([]byte(`{"code":0,"data":{"nonce":"wrong"}}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"code":0,"data":{"antiMITMAttackData":%s}}`, signedAntiMITMJSONWithRequest(t, 1, encodedCertificate, "", false))
	}))
	defer server.Close()
	encodedCertificate = base64.StdEncoding.EncodeToString(server.Certificate().Raw)

	session := newTLSTestSession(server)
	if _, _, err := session.authConfig(false, true); err != nil {
		t.Fatalf("official authentication caller does not propagate the check result: %v", err)
	}
}

func signedAntiMITMJSON(t *testing.T, enable int, rsaCert, sm2EncCert string) string {
	return signedAntiMITMJSONWithRequest(t, enable, rsaCert, sm2EncCert, true)
}

func signedAntiMITMJSONWithRequest(t *testing.T, enable int, rsaCert, sm2EncCert string, antiMITMRequest bool) string {
	t.Helper()
	data := antiMITMAttackData{
		Enable:             enable,
		DevicePubKeyMod:    "A1B2C3",
		DevicePubKeyExp:    "10001",
		RSACert:            rsaCert,
		SM2EncCert:         sm2EncCert,
		Challenge:          "challenge-vector",
		EncryptedChallenge: "E4E065E124F3E6FA5B5125745170A7EE97342BB9E9AE2FF7F523FF5872B9541E",
		Ticket:             "ticket",
		AntiMITMRequest:    antiMITMRequest,
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	data.raw = raw
	data.MITMSignature, err = sangforMITMSignature(data)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
