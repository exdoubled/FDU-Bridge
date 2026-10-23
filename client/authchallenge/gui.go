package authchallenge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
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
	output     io.Writer
	responses  chan guiResponse
	relogin    chan struct{}
	stop       chan struct{}
	requestMu  sync.Mutex
	writeMu    sync.Mutex
	sequence   int
	responseMu sync.Mutex
	pending    int
}

var guiOnce sync.Once
var defaultGUI *guiHandler

func newGUIHandler(input io.Reader, output io.Writer) *guiHandler {
	h := &guiHandler{output: output, responses: make(chan guiResponse, 1), relogin: make(chan struct{}, 1), stop: make(chan struct{})}
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
			if response.Operation == "relogin" {
				select {
				case h.relogin <- struct{}{}:
				default:
				}
				continue
			}
			h.responseMu.Lock()
			if response.ID > 0 && response.ID == h.pending {
				select {
				case h.responses <- response:
				default:
					// Duplicate answers must not close the core control channel.
				}
			}
			h.responseMu.Unlock()
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
	return DefaultHandlerContext(context.Background())
}

// DefaultHandlerContext lets a managed session cancel pending GUI challenges
// and its browser when replacing the session without stopping the whole core.
func DefaultHandlerContext(ctx context.Context) Handler {
	if h := guiSession(); h != nil {
		return &guiContextHandler{guiHandler: h, ctx: ctx}
	}
	return NewCLIHandler(CLIOptions{})
}

// GUIRelogin is separate from challenge responses. Repeated pending commands
// coalesce so a double click cannot queue multiple browser login attempts.
func GUIRelogin() <-chan struct{} {
	if h := guiSession(); h != nil {
		return h.relogin
	}
	return nil
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
	return h.askContext(context.Background(), kind, event)
}

func (h *guiHandler) askContext(ctx context.Context, kind string, event map[string]any) (guiResponse, error) {
	h.requestMu.Lock()
	defer h.requestMu.Unlock()
	if err := ctx.Err(); err != nil {
		return guiResponse{}, err
	}
	h.sequence++
	h.setPendingChallenge(h.sequence)
	defer h.setPendingChallenge(0)
	event["kind"], event["type"], event["id"] = "challenge", kind, h.sequence
	if err := h.emit(event); err != nil {
		return guiResponse{}, err
	}
	for {
		select {
		case <-ctx.Done():
			return guiResponse{}, ctx.Err()
		case <-h.stop:
			return guiResponse{}, errors.New("GUI authentication cancelled")
		case response := <-h.responses:
			if response.ID != h.sequence {
				continue // A cancelled challenge may still have a response in flight.
			}
			if response.Error != "" {
				return guiResponse{}, errors.New("GUI authentication cancelled")
			}
			return response, nil
		}
	}
}

// Switching challenges and dropping queued answers are one operation, so a
// cancelled reply cannot fill the next challenge's response slot.
func (h *guiHandler) setPendingChallenge(id int) {
	h.responseMu.Lock()
	defer h.responseMu.Unlock()
	h.pending = id
	for {
		select {
		case <-h.responses:
		default:
			return
		}
	}
}

type guiContextHandler struct {
	*guiHandler
	ctx context.Context
}

func (h *guiHandler) HandleCodeChallenge(c CodeChallenge) (CodeResponse, error) {
	return (&guiContextHandler{h, context.Background()}).HandleCodeChallenge(c)
}
func (h *guiContextHandler) HandleCodeChallenge(c CodeChallenge) (CodeResponse, error) {
	r, err := h.askContext(h.ctx, "code", map[string]any{"code_kind": c.Kind, "message": c.Message, "can_skip": c.CanSkipSecondaryAuth})
	if err == nil && r.Code == "" {
		err = errors.New("authentication code is empty")
	}
	return CodeResponse{Code: r.Code, SkipSecondaryAuth: r.Skip && c.CanSkipSecondaryAuth}, err
}
func (h *guiHandler) HandleTextCaptcha(c TextCaptchaChallenge) (TextCaptchaResponse, error) {
	return (&guiContextHandler{h, context.Background()}).HandleTextCaptcha(c)
}
func (h *guiContextHandler) HandleTextCaptcha(c TextCaptchaChallenge) (TextCaptchaResponse, error) {
	r, err := h.askContext(h.ctx, "text-captcha", map[string]any{"image": c.Image, "message": c.Message})
	response := TextCaptchaResponse{Code: r.Code}
	if err == nil {
		err = c.Validate(response)
	}
	return response, err
}
func (h *guiHandler) HandleClickCaptcha(c ClickCaptchaChallenge) (ClickCaptchaResponse, error) {
	return (&guiContextHandler{h, context.Background()}).HandleClickCaptcha(c)
}
func (h *guiContextHandler) HandleClickCaptcha(c ClickCaptchaChallenge) (ClickCaptchaResponse, error) {
	r, err := h.askContext(h.ctx, "click-captcha", map[string]any{"image": c.Image, "message": c.Message})
	response := ClickCaptchaResponse{Points: r.Points, Width: r.Width, Height: r.Height}
	if err == nil {
		response, err = validatePoints(r.Points)
		response.Width, response.Height = r.Width, r.Height
	}
	return response, err
}
func (h *guiHandler) HandleExternalLogin(c ExternalLoginChallenge) (ExternalLoginResponse, error) {
	return (&guiContextHandler{h, context.Background()}).HandleExternalLogin(c)
}
func (h *guiContextHandler) HandleExternalLogin(c ExternalLoginChallenge) (ExternalLoginResponse, error) {
	if err := h.ctx.Err(); err != nil {
		return ExternalLoginResponse{}, err
	}
	if c.Kind == ExternalLoginOAuth2 {
		if c.CallbackURL == "" {
			return ExternalLoginResponse{}, errors.New("自动登录缺少回调地址，请重新连接")
		}
		ctx, cancel := context.WithTimeout(h.ctx, 5*time.Minute)
		go func() {
			select {
			case <-h.stop:
				cancel()
			case <-ctx.Done():
			}
		}()
		callback, err := captureBrowserLogin(ctx, c.LoginURL, c.CallbackURL, c.DialContext, os.Stderr)
		cancel()
		if err == nil {
			return ExternalLoginResponse{CallbackURL: callback}, nil
		}
		select {
		case <-h.stop:
			return ExternalLoginResponse{}, errors.New("GUI authentication cancelled")
		default:
		}
		return ExternalLoginResponse{}, fmt.Errorf("自动登录失败，请重新连接：%w", err)
	}
	r, err := h.askContext(h.ctx, "external-login", map[string]any{"login_url": c.LoginURL, "message": "在浏览器登录后，粘贴回调地址。"})
	if err == nil && r.Callback == "" {
		err = errors.New("callback URL is empty")
	}
	return ExternalLoginResponse{CallbackURL: r.Callback}, err
}
