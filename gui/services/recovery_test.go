package services

import (
	"path/filepath"
	"testing"
)

func TestAuthenticationStateSurvivesCoreExitUntilExplicitAction(t *testing.T) {
	dir := t.TempDir()
	b, err := NewBackend(dir, filepath.Join(dir, "gui.exe"))
	if err != nil {
		t.Fatal(err)
	}
	b.follow, b.enabled = true, true
	b.challenge = map[string]any{"id": 1}
	b.onCoreState("auth-required")
	if b.challenge != nil {
		t.Fatal("expired login retained a stale authentication challenge")
	}
	b.onCoreState("stopped")
	b.config.Integration.Mode = "proxy"
	b.refreshCampus()
	if status := b.Status(); !status.AuthRequired || status.State != "auth-required" || status.Connected {
		t.Fatalf("expired login was hidden by process exit: %+v", status)
	}
	b.waitingForVPN()
	if b.Status().State != "auth-required" {
		t.Fatal("network watcher hid the explicit login action")
	}
	b.onCoreState("authentication")
	if b.Status().AuthRequired {
		t.Fatal("explicit authentication did not clear expired state")
	}
	b.onCoreState("reconnecting")
	b.waitingForVPN()
	if status := b.Status(); status.State != "reconnecting" || status.AuthRequired {
		t.Fatalf("temporary disconnection incorrectly requested login: %+v", status)
	}
	b.onCoreState("auth-required")
	if stopped, err := b.Disconnect(); err != nil || !stopped || b.Status().AuthRequired {
		t.Fatalf("manual disable did not clear recovery intent: stopped=%v error=%v", stopped, err)
	}
	b.stopping = true
	b.onCoreState("auth-required")
	b.onCoreState("stopped")
	if b.Status().AuthRequired {
		t.Fatal("late core output restored an expired-login prompt during manual stop")
	}
}
