package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mythologyli/zju-connect/client/authchallenge"
	"github.com/pquerna/otp/totp"
)

type uisTestTransport func(*http.Request) (*http.Response, error)

func (f uisTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFudanUISAuthentication(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	const secret = "JBSWY3DPEHPK3PXP"
	const callback = "https://vpn.example.com/passport/v1/auth/httpsOauth2?code=oauth-code&state=null"
	for _, tc := range []struct {
		name, passwordReply, otpReply, secret, callback, wantError string
		otpCalls, engineCalls                                      int
		header                                                     bool
	}{
		{name: "totp", otpCalls: 1, engineCalls: 1},
		{name: "header-callback", otpCalls: 1, engineCalls: 1, header: true},
		{name: "no-second-factor", passwordReply: `{"code":200,"loginToken":"login-token"}`, wantError: "未要求二次验证"},
		{name: "password-rejected", passwordReply: `{"code":401,"message":"private-password"}`, wantError: "账号密码认证失败"},
		{name: "totp-unavailable", passwordReply: `{"code":200,"second":true,"moduleCodes":["sms"]}`, wantError: "未提供 TOTP"},
		{name: "missing-context", passwordReply: `{"code":200,"second":true,"moduleCodes":["userAndOtp"]}`, wantError: "上下文不完整"},
		{name: "code-is-not-secret", secret: "123456", wantError: "Base32"},
		{name: "missing-secret", secret: " ", wantError: "TOTP 密钥"},
		{name: "totp-rejected", otpCalls: 1, otpReply: `{"code":401}`, wantError: "TOTP 验证失败"},
		{name: "additional-factor", otpCalls: 1, otpReply: `{"code":200,"second":true,"loginToken":"login-token"}`, wantError: "TOTP 验证失败"},
		{name: "missing-token", otpCalls: 1, otpReply: `{"code":200}`, wantError: "TOTP 验证失败"},
		{name: "wrong-callback", otpCalls: 1, engineCalls: 1, callback: "https://other.example/passport/v1/auth/httpsOauth2?code=private-code", wantError: "预期的 VPN"},
		{name: "wrong-callback-path", otpCalls: 1, engineCalls: 1, callback: "https://vpn.example.com/other?code=private-code", wantError: "预期的 VPN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			passwordCalls, otpCalls, engineCalls := 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/oauth/authorize" {
					if _, err := r.Cookie("uis-context"); err != nil {
						t.Error("UIS cookie was lost")
					}
				}
				switch r.URL.Path {
				case "/oauth/authorize":
					http.SetCookie(w, &http.Cookie{Name: "uis-context", Value: "session", Path: "/", Secure: true})
					http.Redirect(w, r, fudanUISOrigin+"/ac/?lck=context-key", http.StatusFound)
				case "/ac/":
					fmt.Fprint(w, "UIS login")
				case "/idp/authn/queryAuthMethods":
					var p map[string]string
					_ = json.NewDecoder(r.Body).Decode(&p)
					if p["lck"] != "context-key" || p["entityId"] != "vpn-client" {
						t.Error("missing OAuth context")
					}
					fmt.Fprint(w, `{"code":"200","requestType":"chain_type","data":[{"moduleCode":"userAndPwd","authChainCode":"password-chain"}]}`)
				case "/idp/authn/getJsPublicKey":
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": base64.StdEncoding.EncodeToString(der)})
				case "/idp/authn/authExecute":
					var p struct {
						Module  string            `json:"authModuleCode"`
						Chain   string            `json:"authChainCode"`
						Request string            `json:"requestNumber"`
						LCK     string            `json:"lck"`
						Entity  string            `json:"entityId"`
						Type    string            `json:"requestType"`
						Para    map[string]string `json:"authPara"`
					}
					if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
						t.Fatal(err)
					}
					if p.LCK != "context-key" || p.Entity != "vpn-client" || p.Type != "chain_type" {
						t.Error("lost auth context")
					}
					if p.Module == "userAndPwd" {
						passwordCalls++
						cipher, _ := base64.StdEncoding.DecodeString(p.Para["password"])
						plain, err := rsa.DecryptPKCS1v15(rand.Reader, key, cipher)
						if err != nil || string(plain) != "private-password" || p.Para["loginName"] != "student" || p.Chain != "password-chain" {
							t.Error("incorrect encrypted password payload")
						}
						if tc.passwordReply != "" {
							fmt.Fprint(w, tc.passwordReply)
						} else {
							fmt.Fprint(w, `{"code":200,"second":true,"moduleCodes":["userAndOtp"],"requestNumber":"request-2","authChainCode":"otp-chain","userName":"canonical-student"}`)
						}
					} else if p.Module == "userAndOtp" {
						otpCalls++
						if !totp.Validate(p.Para["otpCode"], secret) || p.Para["loginName"] != "canonical-student" || p.Chain != "otp-chain" || p.Request != "request-2" || p.Para["password"] != "" {
							t.Error("incorrect TOTP payload")
						}
						if tc.otpReply != "" {
							fmt.Fprint(w, tc.otpReply)
						} else {
							fmt.Fprint(w, `{"code":"200","loginToken":"login-token"}`)
						}
					} else {
						t.Error("unexpected auth module")
					}
				case "/idp/authCenter/authnEngine":
					engineCalls++
					if r.FormValue("loginToken") != "login-token" {
						t.Error("missing login token")
					}
					location := callback
					if tc.callback != "" {
						location = tc.callback
					}
					if tc.header {
						w.Header().Set("Location", location)
						w.WriteHeader(http.StatusFound)
					} else {
						fmt.Fprintf(w, `<script>var locationValue = "%s";</script>`, strings.ReplaceAll(location, "&", "&amp;"))
					}
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
				}
			}))
			defer server.Close()
			target, _ := url.Parse(server.URL)
			s := NewSession("vpn.example.com", nil)
			s.totpSecret = "jbsw y3dp ehpk 3pxp"
			if tc.secret != "" {
				s.totpSecret = tc.secret
			}
			s.uisTransport = uisTestTransport(func(req *http.Request) (*http.Response, error) {
				if !isFudanUISURL(req.URL) {
					t.Fatal("visited OAuth callback instead of returning it")
				}
				copy := req.Clone(req.Context())
				copy.URL.Scheme, copy.URL.Host = target.Scheme, target.Host
				resp, err := server.Client().Transport.RoundTrip(copy)
				if resp != nil {
					resp.Request = req
				}
				return resp, err
			})
			got, err := s.loginFudanUIS(fudanUISOrigin+"/oauth/authorize?client_id=vpn-client", " student ", "private-password")
			if tc.wantError == "" {
				if err != nil || got != callback {
					t.Fatalf("callback = %q, error = %v", got, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("error = %v", err)
			}
			if err != nil && (strings.Contains(err.Error(), "private-password") || strings.Contains(err.Error(), "private-code")) {
				t.Fatal("error exposed credentials")
			}
			if passwordCalls != map[bool]int{true: 0, false: 1}[tc.secret == "123456" || tc.secret == " "] || otpCalls != tc.otpCalls || engineCalls != tc.engineCalls {
				t.Fatalf("request counts = %d, %d, %d", passwordCalls, otpCalls, engineCalls)
			}
		})
	}
}

func TestFudanUISRejectsUntrustedLoginAndDoesNotOpenBrowser(t *testing.T) {
	for _, login := range []string{"http://id.fudan.edu.cn/?client_id=x", "https://other.example/?client_id=x", "https://id.fudan.edu.cn:444/?client_id=x", "https://user@id.fudan.edu.cn/?client_id=x", "https://id.fudan.edu.cn/"} {
		s := NewSession("vpn.example.com", nil)
		s.uisTransport = uisTestTransport(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected request"); return nil, nil })
		if _, err := s.loginFudanUIS(login, "student", "password"); err == nil {
			t.Fatal("accepted invalid login URL")
		}
	}
	s := NewSession("vpn.example.com", nil)
	s.challengeHandler = authchallenge.HandlerFuncs{ExternalLogin: func(authchallenge.ExternalLoginChallenge) (authchallenge.ExternalLoginResponse, error) {
		t.Fatal("opened browser for missing UIS credentials")
		return authchallenge.ExternalLoginResponse{}, nil
	}}
	m, err := NewLoginMethod(LoginMethodOptions{AuthType: "auth/httpsOauth2", Domain: "id.fudan.edu.cn", Username: "student"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.login(s, AuthInfo{LoginURL: fudanUISOrigin + "/?client_id=x"}); err == nil || !strings.Contains(err.Error(), "填写 UIS") {
		t.Fatalf("error = %v", err)
	}
	method := m.(HTTPSOauth2Login)
	if method.Username != "student" {
		t.Fatal("lost UIS credentials")
	}
}

func TestFudanUISCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := NewSessionContext(ctx, "vpn.example.com", nil)
	s.totpSecret = "JBSWY3DPEHPK3PXP"
	entered := make(chan struct{})
	s.uisTransport = uisTestTransport(func(r *http.Request) (*http.Response, error) {
		close(entered)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	done := make(chan error, 1)
	go func() { _, err := s.loginFudanUIS(fudanUISOrigin+"/?client_id=x", "student", "password"); done <- err }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("login returned before sending a request: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("UIS request was never sent")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("UIS request ignored cancellation")
	}
}
