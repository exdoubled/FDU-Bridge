package authchallenge

import "testing"

func TestOAuthCallbackMatchesOnlyExpectedEndpointWithCode(t *testing.T) {
	const expected = "https://vpn.fudan.edu.cn:443/passport/v1/auth/httpsOauth2"
	for _, tc := range []struct {
		url  string
		want bool
	}{
		{"https://vpn.fudan.edu.cn/passport/v1/auth/httpsOauth2?code=a%2Bb&state=x", true},
		{"https://vpn.fudan.edu.cn:443/passport/v1/auth/httpsOauth2?code=abc", true},
		{"https://vpn.fudan.edu.cn.evil.example/passport/v1/auth/httpsOauth2?code=abc", false},
		{"https://vpn.fudan.edu.cn:8443/passport/v1/auth/httpsOauth2?code=abc", false},
		{"http://vpn.fudan.edu.cn/passport/v1/auth/httpsOauth2?code=abc", false},
		{"https://vpn.fudan.edu.cn/passport/v1/auth/httpsOauth2/extra?code=abc", false},
		{"https://vpn.fudan.edu.cn/passport/v1/auth/httpsOauth2?code=", false},
		{"https://vpn.fudan.edu.cn/passport/v1/auth/httpsOauth2?error=access_denied", false},
	} {
		t.Run(tc.url, func(t *testing.T) {
			if got := matchesOAuthCallback(tc.url, expected); got != tc.want {
				t.Fatalf("matchesOAuthCallback() = %v, want %v", got, tc.want)
			}
		})
	}
}

// Run explicitly: FDU_CONNECT_BROWSER_TEST=1 go test ./client/authchallenge -run Browser -v
// Uses an actual isolated browser and a local redirect, without UIS credentials.
