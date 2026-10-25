package bootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartupLogsWailsErrorsAndOnlyShowsFatalOnce(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "logs")
	var messages []string
	s, err := openStartup(directory, func(message string) { messages = append(messages, message) })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Logger == nil {
		t.Fatal("file logger unavailable")
	}
	s.Logger.Info("framework starting")
	s.HandleError(errors.New("nonfatal test error"))
	if len(messages) != 0 {
		t.Fatal("nonfatal error opened a dialog")
	}
	s.Fail(errors.New("fatal startup failure"))
	s.Fail(errors.New("duplicate fatal failure"))
	if len(messages) != 1 || !strings.Contains(messages[0], filepath.Join(directory, "startup.log")) {
		t.Fatalf("fatal dialogs = %#v", messages)
	}
	data, err := os.ReadFile(filepath.Join(directory, "startup.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"framework starting", "nonfatal test error", "fatal startup failure"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("log missing %q", want)
		}
	}
}

func TestStartupLogOpenFailureStillShowsNativeErrorAndPath(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(directory, []byte("a file blocks the directory"), 0600); err != nil {
		t.Fatal(err)
	}
	var message string
	s, err := openStartup(directory, func(text string) { message = text })
	if err == nil {
		t.Fatal("expected log open failure")
	}
	s.Fail(err)
	if !strings.Contains(message, "startup.log") || !strings.Contains(message, "日志") {
		t.Fatalf("dialog = %q", message)
	}
}
