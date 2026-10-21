package atrust

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mythologyli/zju-connect/client/atrust/auth"
)

func TestSessionInvalidCallbackKeepsProcessAlive(t *testing.T) {
	failed := make(chan error, 1)
	c := NewClient(ClientOptions{OnSessionInvalid: func(err error) { failed <- err }})
	defer c.Close()
	c.startSessionRefresh(func(context.Context) (auth.LoginResult, error) { return auth.LoginResult{}, auth.ErrSessionInvalid }, auth.ClientAuthData{}, nil, time.Millisecond)
	select {
	case err := <-failed:
		if !errors.Is(err, auth.ErrSessionInvalid) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("no invalid-session notification")
	}
	if sid, err := c.sessionSID(); sid != "" || !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("invalid session still usable: %s %v", sid, err)
	}
}

func TestInvalidTunnelAuthReturnsTypedError(t *testing.T) {
	for _, data := range []string{`{"code":10000004,"message":"expired"}`, `{"code":75500002,"message":"invalid"}`} {
		if err := parseIPAuthResponse([]byte(data), true); !errors.Is(err, auth.ErrSessionInvalid) {
			t.Fatalf("IP auth error = %v", err)
		}
		if err := parseTCPTunnelAuthResponse(data, true); !errors.Is(err, auth.ErrSessionInvalid) {
			t.Fatalf("TCP auth error = %v", err)
		}
	}
}
