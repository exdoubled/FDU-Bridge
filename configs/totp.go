package configs

import (
	"encoding/base32"
	"fmt"
	"strings"
)

// NormalizeTOTPSecret accepts a Base32 authenticator key, not a six-digit code.
// Never include the secret in validation errors.
func NormalizeTOTPSecret(secret string) (string, error) {
	secret = strings.TrimRight(strings.ToUpper(strings.Join(strings.Fields(secret), "")), "=")
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil || len(key) < 10 {
		return "", fmt.Errorf("请填写绑定验证器时的 Base32 TOTP 密钥，而非六位动态验证码")
	}
	return secret, nil
}
