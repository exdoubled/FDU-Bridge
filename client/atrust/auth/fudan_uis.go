package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mythologyli/zju-connect/configs"
	"github.com/mythologyli/zju-connect/log"
	"github.com/pquerna/otp/totp"
)

const fudanUISOrigin = "https://id.fudan.edu.cn"

type uisReply struct {
	Code          json.Number     `json:"code"`
	Data          json.RawMessage `json:"data"`
	RequestType   string          `json:"requestType"`
	Second        bool            `json:"second"`
	ModuleCodes   []string        `json:"moduleCodes"`
	RequestNumber string          `json:"requestNumber"`
	AuthChainCode string          `json:"authChainCode"`
	UserName      string          `json:"userName"`
	LoginToken    string          `json:"loginToken"`
}

// ErrUISRejected marks a terminal UIS failure: rejected or unusable credentials.
// Callers must not retry it automatically, or the account may be locked.
var ErrUISRejected = errors.New("UIS rejected the credentials")

type uisRejection string

func (e uisRejection) Error() string { return string(e) }

func (uisRejection) Is(target error) bool { return target == ErrUISRejected }

func isFudanUISURL(u *url.URL) bool {
	return u.Scheme == "https" && u.Hostname() == "id.fudan.edu.cn" &&
		(u.Port() == "" || u.Port() == "443") && u.User == nil
}

var uisLocation = regexp.MustCompile(`locationValue\s*=\s*"([^"]+)"`)

// loginFudanUIS follows the UIS userAndPwd -> userAndOtp -> OAuth callback flow.
// It never opens a browser or retries rejected credentials/second factors.
func (s *Session) loginFudanUIS(loginURL, username, password string) (string, error) {
	login, err := url.Parse(loginURL)
	if err != nil || !isFudanUISURL(login) || login.Query().Get("client_id") == "" {
		return "", fmt.Errorf("UIS 登录地址或 client_id 无效")
	}
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return "", fmt.Errorf("请在设置中填写 UIS 账号、密码和 TOTP 密钥；命令行请配置 username、password、totp_secret")
	}
	// The second factor is mandatory. Validate the key before any request so a
	// missing or malformed secret never costs a login attempt.
	if strings.TrimSpace(s.totpSecret) == "" {
		return "", uisRejection("请在设置中填写 TOTP 密钥；命令行请配置 totp_secret")
	}
	secret, err := configs.NormalizeTOTPSecret(s.totpSecret)
	if err != nil {
		return "", uisRejection(err.Error())
	}
	jar, _ := cookiejar.New(nil)
	uis := &http.Client{Jar: jar, Timeout: 60 * time.Second}
	// UIS receives the account credentials, so verify its certificate even when
	// the aTrust gateway uses a self-signed certificate. Preserve the underlay,
	// and refuse to send credentials when it cannot be cloned and hardened.
	if s.uisTransport != nil {
		uis.Transport = s.uisTransport
	} else {
		transport, ok := s.client.Transport.(*http.Transport)
		if !ok {
			return "", fmt.Errorf("UIS 登录需要可校验证书的 HTTPS 传输，已拒绝提交凭据")
		}
		secure := transport.Clone()
		if secure.TLSClientConfig == nil {
			secure.TLSClientConfig = &tls.Config{}
		} else {
			secure.TLSClientConfig = secure.TLSClientConfig.Clone()
		}
		secure.TLSClientConfig.InsecureSkipVerify = false
		uis.Transport = secure
	}
	defer uis.CloseIdleConnections()
	uis.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 || !isFudanUISURL(req.URL) {
			return fmt.Errorf("UIS 登录跳转地址无效")
		}
		return nil
	}
	req, _ := http.NewRequestWithContext(s.ctx, http.MethodGet, loginURL, nil)
	req.Header.Set("User-Agent", UserAgent)
	resp, err := uis.Do(req)
	if err != nil {
		return "", uisRequestError(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("UIS 登录上下文请求失败（HTTP %d）", resp.StatusCode)
	}
	lck := resp.Request.URL.Query().Get("lck")
	if lck == "" {
		return "", fmt.Errorf("UIS 登录缺少认证上下文 lck")
	}
	// API requests must not redirect credentials to another endpoint.
	uis.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	entityID := login.Query().Get("client_id")
	var methods uisReply
	if err := s.uisJSON(uis, "/idp/authn/queryAuthMethods", map[string]string{"lck": lck, "entityId": entityID}, &methods); err != nil {
		return "", err
	}
	var available []struct {
		ModuleCode    string `json:"moduleCode"`
		AuthChainCode string `json:"authChainCode"`
	}
	if err := json.Unmarshal(methods.Data, &available); err != nil {
		return "", fmt.Errorf("UIS 认证方式响应无效")
	}
	chain := ""
	for _, method := range available {
		if method.ModuleCode == "userAndPwd" {
			chain = method.AuthChainCode
			break
		}
	}
	if chain == "" {
		return "", fmt.Errorf("UIS 未提供账号密码认证")
	}
	var publicKey uisReply
	if err := s.uisJSON(uis, "/idp/authn/getJsPublicKey", nil, &publicKey); err != nil {
		return "", err
	}
	var encoded string
	if json.Unmarshal(publicKey.Data, &encoded) != nil {
		return "", fmt.Errorf("UIS 公钥响应无效")
	}
	der, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("UIS 公钥编码无效")
	}
	key, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return "", fmt.Errorf("UIS 公钥无效")
	}
	pub, ok := key.(*rsa.PublicKey)
	if !ok {
		return "", fmt.Errorf("UIS 公钥不是 RSA 密钥")
	}
	encrypted, err := rsa.EncryptPKCS1v15(rand.Reader, pub, []byte(password))
	if err != nil {
		return "", fmt.Errorf("UIS 密码加密失败")
	}
	requestType := methods.RequestType
	if requestType == "" {
		requestType = "chain_type"
	}
	payload := map[string]any{
		"authModuleCode": "userAndPwd", "authChainCode": chain,
		"entityId": entityID, "requestType": requestType, "lck": lck,
		"authPara": map[string]string{"loginName": username, "password": base64.StdEncoding.EncodeToString(encrypted), "verifyCode": ""},
	}
	log.Println("UIS 账号密码认证（无需浏览器）")
	var authenticated uisReply
	if err := s.uisJSON(uis, "/idp/authn/authExecute", payload, &authenticated); err != nil {
		return "", err
	}
	if authenticated.Code != "200" {
		return "", uisRejection("UIS 账号密码认证失败，请检查账号、密码或账户验证要求；已停止自动重试")
	}
	// The second factor is mandatory: never accept a login that skipped TOTP.
	if !authenticated.Second {
		return "", uisRejection("UIS 未要求二次验证，已拒绝继续登录；已停止自动重试")
	}
	if !slices.Contains(authenticated.ModuleCodes, "userAndOtp") {
		return "", uisRejection("UIS 要求二次验证，但未提供 TOTP 方式")
	}
	if authenticated.RequestNumber == "" || authenticated.AuthChainCode == "" {
		return "", fmt.Errorf("UIS TOTP 认证上下文不完整")
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		return "", uisRejection("UIS TOTP 生成失败，请检查密钥")
	}
	if authenticated.UserName != "" {
		username = authenticated.UserName
	}
	payload["authModuleCode"], payload["authChainCode"] = "userAndOtp", authenticated.AuthChainCode
	payload["requestNumber"] = authenticated.RequestNumber
	payload["authPara"] = map[string]string{"loginName": username, "otpCode": code}
	log.Println("UIS TOTP 自动验证")
	authenticated = uisReply{}
	if err := s.uisJSON(uis, "/idp/authn/authExecute", payload, &authenticated); err != nil {
		return "", err
	}
	if authenticated.Code != "200" || authenticated.Second || authenticated.LoginToken == "" {
		return "", uisRejection("UIS TOTP 验证失败，请检查绑定密钥和系统时间；已停止自动重试")
	}
	if authenticated.LoginToken == "" {
		return "", fmt.Errorf("UIS 未返回登录令牌")
	}
	resp, err = s.uisRequest(uis, http.MethodPost, "/idp/authCenter/authnEngine", "application/x-www-form-urlencoded",
		strings.NewReader(url.Values{"loginToken": {authenticated.LoginToken}}.Encode()))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("读取 UIS 认证回调失败")
	}
	callback := resp.Header.Get("Location")
	if match := uisLocation.FindSubmatch(body); len(match) > 1 {
		callback = html.UnescapeString(string(match[1]))
	}
	u, err := url.Parse(callback)
	expected, _ := url.Parse(s.baseURL)
	if err != nil || u.Scheme != "https" || u.Host != expected.Host || u.User != nil || validateHTTPSOauth2CallbackURL(u) != nil {
		return "", fmt.Errorf("UIS 未返回预期的 VPN OAuth 回调")
	}
	return u.String(), nil
}

func uisRequestError(err error) error {
	if e, ok := err.(*url.Error); ok {
		err = e.Err // Do not include authentication URLs or query values in logs.
	}
	return fmt.Errorf("UIS 请求失败：%w", err)
}

func (s *Session) uisRequest(c *http.Client, method, path, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(s.ctx, method, fudanUISOrigin+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Referer", fudanUISOrigin+"/ac/")
	req.Header.Set("Origin", fudanUISOrigin)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, uisRequestError(err)
	}
	if resp.StatusCode != http.StatusOK && !(path == "/idp/authCenter/authnEngine" && resp.StatusCode == http.StatusFound) {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("UIS 请求失败（HTTP %d）", resp.StatusCode)
	}
	return resp, nil
}

func (s *Session) uisJSON(c *http.Client, path string, payload any, out *uisReply) error {
	method := http.MethodGet
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body, method = bytes.NewReader(encoded), http.MethodPost
	}
	resp, err := s.uisRequest(c, method, path, "application/json", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return fmt.Errorf("UIS 响应格式无效")
	}
	return nil
}
