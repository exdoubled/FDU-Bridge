//go:build !tun

package main

import (
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"time"

	atrustclient "github.com/mythologyli/zju-connect/client/atrust"
	"github.com/mythologyli/zju-connect/client/atrust/auth"
	"github.com/mythologyli/zju-connect/log"
)

type selectiveSessionMode int

const (
	selectiveInitial selectiveSessionMode = iota
	selectiveResume
	selectiveFresh
)

type selectiveConnectFunc func(context.Context, selectiveSessionMode, func()) error

// Only the campus session is replaced. The caller owns the ordinary forwarding
// listeners, which remain available while waiting for networking or the user.
func runSelectiveSessions(ctx context.Context, connect selectiveConnectFunc, relogin <-chan struct{}, state func(string), retryDelay time.Duration) {
	mode := selectiveInitial
	if guiFreshLogin() {
		mode = selectiveFresh
	}
	delay := retryDelay
	for ctx.Err() == nil {
		var connected atomic.Bool
		if mode == selectiveFresh {
			state("authentication")
		}
		attemptCtx, cancel := context.WithCancel(ctx)
		result := make(chan error, 1)
		go func() {
			result <- connect(attemptCtx, mode, func() { connected.Store(true) })
		}()
		var err error
		restart := false
	waitAttempt:
		for {
			select {
			case <-ctx.Done():
				cancel()
				<-result
				return
			case <-relogin:
				cancel()
				<-result // Fully release the old session before replacing it.
				restart = true
				break waitAttempt
			case err = <-result:
				cancel()
				break waitAttempt
			}
		}
		if ctx.Err() != nil {
			return
		}
		if restart {
			mode, delay = selectiveFresh, retryDelay
			continue
		}
		if err != nil {
			log.Printf("Campus VPN unavailable: %v; ordinary Internet forwarding remains available", err)
		}
		if connected.Load() {
			delay = retryDelay
		}
		var retry <-chan time.Time
		var timer *time.Timer
		switch {
		case errors.Is(err, auth.ErrSessionInvalid):
			state("auth-required")
		case selectiveTransientError(err):
			state("reconnecting")
			timer = time.NewTimer(delay)
			retry = timer.C
			delay = min(delay*2, 30*time.Second)
		default:
			state("campus-unavailable")
			// Unknown service/protocol failures may be temporary too. Resume
			// slowly without opening a browser or discarding saved credentials.
			timer = time.NewTimer(max(delay, 30*retryDelay))
			retry = timer.C
		}
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-relogin:
			mode, delay = selectiveFresh, retryDelay
		case <-retry:
			// A retry may only resume cookies/SID. It must never reopen a browser.
			mode = selectiveResume
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

func selectiveTransientError(err error) bool {
	if err == nil || errors.Is(err, auth.ErrSessionInvalid) || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, atrustclient.ErrNoReachableNodes) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError)
}
