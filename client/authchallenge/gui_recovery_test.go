package authchallenge

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestGUIReloginCommandsAreSeparateFromChallengeResponses(t *testing.T) {
	input, writer := io.Pipe()
	defer writer.Close()
	events := make(chan []byte, 2)
	h := newGUIHandler(input, guiTestWriter{events})
	defer input.Close()
	result := make(chan error, 1)
	go func() {
		response, err := h.askContext(context.Background(), "code", map[string]any{})
		if err == nil && response.Code != "123456" {
			err = errors.New("challenge response was not delivered")
		}
		result <- err
	}()
	awaitGUI(t, events)
	if _, err := io.WriteString(writer, "{\"operation\":\"relogin\"}\n{\"operation\":\"relogin\"}\n"); err != nil {
		t.Fatal(err)
	}
	awaitGUI(t, h.relogin)
	if _, err := io.WriteString(writer, "{\"id\":1,\"code\":\"123456\"}\n"); err != nil {
		t.Fatal(err)
	}
	if err := awaitGUI(t, result); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.stop:
		t.Fatal("relogin command stopped the core input stream")
	default:
	}
}

func TestGUIChallengeCancellationKeepsInputAvailable(t *testing.T) {
	input, writer := io.Pipe()
	defer writer.Close()
	events := make(chan []byte, 2)
	h := newGUIHandler(input, guiTestWriter{events})
	defer input.Close()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := h.askContext(ctx, "code", map[string]any{}); result <- err }()
	awaitGUI(t, events)
	cancel()
	if err := awaitGUI(t, result); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled challenge = %v", err)
	}
	if _, err := io.WriteString(writer, "{\"operation\":\"relogin\"}\n"); err != nil {
		t.Fatal(err)
	}
	awaitGUI(t, h.relogin)
	_, err := (&guiContextHandler{h, ctx}).HandleExternalLogin(ExternalLoginChallenge{Kind: ExternalLoginOAuth2})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled browser login = %v", err)
	}
}

type guiTestWriter struct{ events chan []byte }

func (w guiTestWriter) Write(data []byte) (int, error) {
	w.events <- append([]byte(nil), data...)
	return len(data), nil
}

func awaitGUI[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		t.Fatal("GUI protocol did not progress")
		var zero T
		return zero
	}
}
