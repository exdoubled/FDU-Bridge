package authchallenge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"runtime"
	"sync"
	"time"
)

const guiPrefix = "@@FDU_GUI@@"

type guiResponse struct {
	ID        int     `json:"id"`
	Operation string  `json:"operation"`
	Error     string  `json:"error"`
	Code      string  `json:"code"`
	Skip      bool    `json:"skip"`
	Callback  string  `json:"callback"`
	Points    []Point `json:"points"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
}
type guiHandler struct {
	output    io.Writer
	responses chan guiResponse
	stop      chan struct{}
	requestMu sync.Mutex
	writeMu   sync.Mutex
	sequence  int
}

var guiOnce sync.Once
var defaultGUI *guiHandler

func newGUIHandler(input io.Reader, output io.Writer) *guiHandler {
	h := &guiHandler{output: output, responses: make(chan guiResponse, 1), stop: make(chan struct{})}
	go func() {
		defer close(h.stop)
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		for scanner.Scan() {
			var response guiResponse
			if json.Unmarshal(scanner.Bytes(), &response) != nil {
				return
			}
			if response.Operation == "stop" {
				return
			}
			select {
			case h.responses <- response:
			default:
				return
			}
		}
	}()
	return h
}
func guiSession() *guiHandler {
	if os.Getenv("FDU_CONNECT_GUI") != "1" {
		return nil
	}
	guiOnce.Do(func() { defaultGUI = newGUIHandler(os.Stdin, os.Stdout) })
	return defaultGUI
}

// DefaultHandler leaves CLI behavior unchanged unless the owning GUI opts in.
func DefaultHandler() Handler {
	if h := guiSession(); h != nil {
		return h
	}
	return NewCLIHandler(CLIOptions{})
}
func GUIStop() <-chan struct{} {
	if h := guiSession(); h != nil {
		return h.stop
	}
	return nil
}
func GUIState(state string) {
	if h := guiSession(); h != nil {
		_ = h.emit(map[string]any{"kind": "state", "state": state})
	}
}
func (h *guiHandler) emit(event map[string]any) error {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	b, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = h.output.Write(append(append([]byte(guiPrefix), b...), '\n'))
	return err
}
func (h *guiHandler) ask(kind string, event map[string]any) (guiResponse, error) {
	h.requestMu.Lock()
	defer h.requestMu.Unlock()
	h.sequence++
	event["kind"], event["type"], event["id"] = "challenge", kind, h.sequence
	if err := h.emit(event); err != nil {
		return guiResponse{}, err
	}
	select {
	case <-h.stop:
		return guiResponse{}, errors.New("GUI authentication cancelled")
	case response := <-h.responses:
		if response.ID != h.sequence {
			return guiResponse{}, errors.New("stale GUI authentication response")
		}
		if response.Error != "" {
			return guiResponse{}, errors.New("GUI authentication cancelled")
		}
		return response, nil
	}
}
func (h *guiHandler) HandleCodeChallenge(c CodeChallenge) (CodeResponse, error) {
	r, err := h.ask("code", map[string]any{"code_kind": c.Kind, "message": c.Message, "can_skip": c.CanSkipSecondaryAuth})
	if err == nil && r.Code == "" {
		err = errors.New("authentication code is empty")
	}
	return CodeResponse{Code: r.Code, SkipSecondaryAuth: r.Skip && c.CanSkipSecondaryAuth}, err
}
func (h *guiHandler) HandleTextCaptcha(c TextCaptchaChallenge) (TextCaptchaResponse, error) {
	r, err := h.ask("text-captcha", map[string]any{"image": c.Image, "message": c.Message})
	response := TextCaptchaResponse{Code: r.Code}
	if err == nil {
		err = c.Validate(response)
	}
	return response, err
}
func (h *guiHandler) HandleClickCaptcha(c ClickCaptchaChallenge) (ClickCaptchaResponse, error) {
	r, err := h.ask("click-captcha", map[string]any{"image": c.Image, "message": c.Message})
	response := ClickCaptchaResponse{Points: r.Points, Width: r.Width, Height: r.Height}
	if err == nil {
		response, err = validatePoints(r.Points)
		response.Width, response.Height = r.Width, r.Height
	}
	return response, err
}
func (h *guiHandler) HandleExternalLogin(c ExternalLoginChallenge) (ExternalLoginResponse, error) {
	if runtime.GOOS == "windows" && c.Kind == ExternalLoginOAuth2 && c.CallbackURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		go func() {
			select {
			case <-h.stop:
				cancel()
			case <-ctx.Done():
			}
		}()
		callback, err := captureBrowserLogin(ctx, c.LoginURL, c.CallbackURL, os.Stderr)
		cancel()
		if err == nil {
			return ExternalLoginResponse{CallbackURL: callback}, nil
		}
		select {
		case <-h.stop:
			return ExternalLoginResponse{}, errors.New("GUI authentication cancelled")
		default:
		}
	}
	r, err := h.ask("external-login", map[string]any{"login_url": c.LoginURL, "message": "在浏览器登录后，粘贴回调地址。"})
	if err == nil && r.Callback == "" {
		err = errors.New("callback URL is empty")
	}
	return ExternalLoginResponse{CallbackURL: r.Callback}, err
}
