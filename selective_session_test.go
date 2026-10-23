//go:build !tun

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/mythologyli/zju-connect/client/atrust/auth"
)

func TestSelectiveTransientFailureResumesWithoutInteractiveLogin(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attempts := make(chan selectiveSessionMode, 2)
	states := make(chan string, 2)
	done := make(chan struct{})
	go func() {
		defer close(done)
		calls := 0
		runSelectiveSessions(ctx, func(ctx context.Context, mode selectiveSessionMode, ready func()) error {
			attempts <- mode
			calls++
			if calls == 1 {
				return fmt.Errorf("setup: %w", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("offline")})
			}
			ready()
			<-ctx.Done()
			return ctx.Err()
		}, nil, func(state string) { states <- state }, time.Millisecond)
	}()
	if got := awaitSelective(t, attempts); got != selectiveInitial {
		t.Fatalf("first mode = %v", got)
	}
	if got := awaitSelective(t, states); got != "reconnecting" {
		t.Fatalf("state = %q", got)
	}
	if got := awaitSelective(t, attempts); got != selectiveResume {
		t.Fatalf("retry mode = %v; must not request interactive login", got)
	}
	cancel()
	awaitSelective(t, done)
}

func TestSelectiveCancellationStopsPendingRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attempts := make(chan struct{}, 2)
	states := make(chan string, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runSelectiveSessions(ctx, func(context.Context, selectiveSessionMode, func()) error {
			attempts <- struct{}{}
			return &net.OpError{Op: "dial", Err: errors.New("offline")}
		}, nil, func(state string) { states <- state }, time.Hour)
	}()
	awaitSelective(t, attempts)
	awaitSelective(t, states)
	cancel()
	awaitSelective(t, done)
	select {
	case <-attempts:
		t.Fatal("cancelled lifecycle attempted another connection")
	default:
	}
}

func TestSelectiveExpiryWaitsForReloginAndReplacesLiveSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	relogin := make(chan struct{}, 1)
	attempts := make(chan selectiveSessionMode, 3)
	states := make(chan string, 3)
	cleaned := make(chan struct{}, 2)
	connected := make(chan struct{}, 2)
	done := make(chan struct{})
	go func() {
		defer close(done)
		calls := 0
		runSelectiveSessions(ctx, func(ctx context.Context, mode selectiveSessionMode, ready func()) error {
			calls++
			if calls == 3 {
				select {
				case <-cleaned:
				default:
					t.Error("new login started before the old session finished cleanup")
				}
			}
			attempts <- mode
			if calls == 1 {
				return fmt.Errorf("refresh: %w", auth.ErrSessionInvalid)
			}
			ready()
			connected <- struct{}{}
			<-ctx.Done()
			cleaned <- struct{}{}
			return ctx.Err()
		}, relogin, func(state string) { states <- state }, time.Millisecond)
	}()
	awaitSelective(t, attempts)
	if got := awaitSelective(t, states); got != "auth-required" {
		t.Fatalf("state = %q", got)
	}
	select {
	case <-attempts:
		t.Fatal("expired credentials retried without an explicit command")
	case <-time.After(20 * time.Millisecond):
	}
	for range 2 {
		relogin <- struct{}{}
		if got := awaitSelective(t, states); got != "authentication" {
			t.Fatalf("state = %q", got)
		}
		if got := awaitSelective(t, attempts); got != selectiveFresh {
			t.Fatalf("explicit relogin mode = %v", got)
		}
		awaitSelective(t, connected)
	}
	cancel()
	awaitSelective(t, done)
}

func TestSelectiveFreshAttemptCanRestartCancelledBrowser(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	t.Setenv("FDU_CONNECT_GUI", "1")
	t.Setenv("FDU_CONNECT_GUI_RELOGIN", "1")
	relogin := make(chan struct{}, 1)
	attempts := make(chan selectiveSessionMode, 2)
	cleaned := make(chan struct{}, 2)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runSelectiveSessions(ctx, func(ctx context.Context, mode selectiveSessionMode, _ func()) error {
			attempts <- mode
			<-ctx.Done()
			cleaned <- struct{}{}
			return ctx.Err()
		}, relogin, func(string) {}, time.Millisecond)
	}()
	awaitSelective(t, attempts)
	relogin <- struct{}{}
	awaitSelective(t, cleaned)
	if got := awaitSelective(t, attempts); got != selectiveFresh {
		t.Fatalf("browser restart mode = %v", got)
	}
	cancel()
	awaitSelective(t, done)
}

func TestSelectiveUnknownSetupFailureRetriesWithoutInteractiveLogin(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attempts := make(chan selectiveSessionMode, 2)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runSelectiveSessions(ctx, func(ctx context.Context, mode selectiveSessionMode, _ func()) error {
			attempts <- mode
			if mode == selectiveInitial {
				return errors.New("temporary server response could not be decoded")
			}
			<-ctx.Done()
			return ctx.Err()
		}, nil, func(string) {}, time.Millisecond)
	}()
	awaitSelective(t, attempts)
	if got := awaitSelective(t, attempts); got != selectiveResume {
		t.Fatalf("unknown-error retry requested interactive login: %v", got)
	}
	cancel()
	awaitSelective(t, done)
}

func awaitSelective[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("selective lifecycle did not progress")
		var zero T
		return zero
	}
}
