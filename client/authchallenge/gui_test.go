package authchallenge

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestGUIChallengeRoundTripAndStop(t *testing.T) {
	in, send := io.Pipe()
	out, events := io.Pipe()
	defer in.Close()
	defer send.Close()
	defer out.Close()
	defer events.Close()
	h := newGUIHandler(in, events)
	done := make(chan error, 1)
	go func() {
		response, err := h.HandleCodeChallenge(CodeChallenge{Kind: CodeSMS, CanSkipSecondaryAuth: true})
		if err == nil && (response.Code != "123456" || !response.SkipSecondaryAuth) {
			t.Errorf("Incorrect response")
		}
		done <- err
	}()
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, guiPrefix) {
		t.Fatal("Missing event framing")
	}
	var event map[string]any
	if err = json.Unmarshal([]byte(strings.TrimPrefix(line, guiPrefix)), &event); err != nil {
		t.Fatal(err)
	}
	if event["type"] != "code" || event["code_kind"] != "sms" {
		t.Fatal("Challenge lost")
	}
	_, _ = io.WriteString(send, "{\"id\":1,\"code\":\"123456\",\"skip\":true}\n")
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(send, "{\"operation\":\"stop\"}\n")
	<-h.stop
}

func TestGUIRejectsMismatchedAuthenticationResponse(t *testing.T) {
	in, send := io.Pipe()
	out, events := io.Pipe()
	defer in.Close()
	defer send.Close()
	defer out.Close()
	defer events.Close()
	h := newGUIHandler(in, events)
	done := make(chan error, 1)
	go func() { _, err := h.HandleCodeChallenge(CodeChallenge{Kind: CodeTOTP}); done <- err }()
	_, _ = bufio.NewReader(out).ReadString('\n')
	_, _ = io.WriteString(send, "{\"id\":99,\"code\":\"private-code\"}\n")
	if err := <-done; err == nil {
		t.Fatal("Accepted stale challenge response")
	}
}
