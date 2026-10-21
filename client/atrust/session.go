package atrust

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mythologyli/zju-connect/client/atrust/auth"
	"github.com/mythologyli/zju-connect/log"
)

func invalidSessionError(code int64, message string) error {
	return fmt.Errorf("%w (code %d): %s", auth.ErrSessionInvalid, code, message)
}

func (c *Client) reportInvalidSession(err error) {
	if c.onSessionInvalid == nil {
		log.Fatalf("aTrust session failed: %v", err)
		return
	}
	c.setSessionSID("", err)
	c.onSessionInvalid(err)
}

func (c *Client) sessionSID() (string, error) {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()
	return c.SID, c.sessionErr
}

func (c *Client) setSessionSID(sid string, err error) {
	c.sessionMu.Lock()
	c.SID, c.sessionErr = sid, err
	c.sessionMu.Unlock()
}

func (c *Client) startSessionRefresh(refresh func(context.Context) (auth.LoginResult, error), data auth.ClientAuthData, save func([]byte) error, interval time.Duration) {
	c.refreshDone = make(chan struct{})
	go func() {
		defer close(c.refreshDone)
		timer := time.NewTimer(interval)
		defer timer.Stop()
		for {
			select {
			case <-c.lifecycleCtx.Done():
				return
			case <-timer.C:
			}
			snapshot, err := refresh(c.lifecycleCtx)
			if c.lifecycleCtx.Err() != nil {
				return
			}
			if errors.Is(err, auth.ErrSessionInvalid) {
				if c.onSessionInvalid == nil {
					log.Fatalf("aTrust session maintenance failed: %v", err)
				}
				c.reportInvalidSession(err)
				return
			}
			if err != nil {
				log.Printf("aTrust authConfig refresh failed: %v", err)
				timer.Reset(min(interval, time.Minute))
				continue
			}
			oldSID, _ := c.sessionSID()
			c.setSessionSID(snapshot.SID, nil)
			log.Printf("aTrust session refreshed (SID changed: %t)", oldSID != snapshot.SID)
			data.Cookies = snapshot.Cookies
			if save != nil {
				encoded, err := json.Marshal(data)
				if err == nil {
					err = save(encoded)
				}
				if err != nil {
					log.Printf("Failed to save refreshed aTrust cookies: %v", err)
				}
			}
			timer.Reset(interval)
		}
	}()
}
