// Package core owns exactly the FDU child it starts; discovery never adopts or stops other cores.
package core

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mythologyli/zju-connect/gui/internal/sysproc"
)

const guiPrefix = "@@FDU_GUI@@"

// Tool actions run the core once with a flag instead of connecting.
var Tools = map[string]bool{"auth-info": true, "trust-device": true, "untrust-device": true}

type Runtime struct {
	Core string   // path to fdu-connect.exe
	Env  []string // extra environment, used by tests

	OnLog       func(line string)
	OnState     func(state string)
	OnChallenge func(challenge map[string]any)

	mu        sync.Mutex
	writeMu   sync.Mutex
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	done      chan struct{}
	secrets   []string
	connected bool
	tool      string
}

// CoreName is the core executable expected beside the GUI.
var CoreName = func() string {
	if runtime.GOOS == "windows" {
		return "fdu-connect.exe"
	}
	return "fdu-connect"
}()

func New(directory string) *Runtime {
	return &Runtime{Core: filepath.Join(directory, CoreName)}
}

func (r *Runtime) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runningLocked()
}

func (r *Runtime) runningLocked() bool {
	if r.done == nil {
		return false
	}
	select {
	case <-r.done:
		return false
	default:
		return true
	}
}

func (r *Runtime) PID() uint32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.runningLocked() {
		return 0
	}
	return uint32(r.cmd.Process.Pid)
}

// Tool reports the running tool action, or "" for a normal connection.
func (r *Runtime) Tool() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.runningLocked() {
		return ""
	}
	return r.tool
}

// ExternalRunning reports FDU cores this GUI did not start.
func (r *Runtime) ExternalRunning() bool { return len(sysproc.FindCores(r.PID())) > 0 }

func lookup(values map[string]any, key string) any {
	var current any = values
	for _, part := range strings.Split(key, ".") {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = m[part]
	}
	return current
}

var secretKey = regexp.MustCompile(`(?i)password|passwd|secret|sid|sign_key|oauth2_code|cas_ticket|twf_id|shadowsocks_url`)

func secretsOf(values map[string]any) []string {
	var result []string
	for key, value := range values {
		if s, ok := value.(string); ok && s != "" && secretKey.MatchString(key) {
			result = append(result, s)
		}
	}
	sort.Slice(result, func(i, j int) bool { return len(result[i]) > len(result[j]) })
	return result
}

func (r *Runtime) preflight(values map[string]any) error {
	if len(sysproc.FindCores(0)) > 0 {
		return errors.New("已有 FDU 在运行，请先自行关闭；本窗口不会接管或关闭它")
	}
	for _, key := range []string{"socks_bind", "http_bind"} {
		bind, _ := lookup(values, key).(string)
		if _, raw, err := net.SplitHostPort(bind); err == nil {
			if port, err := strconv.Atoi(raw); err == nil && port != 0 && sysproc.PortInUse(port) {
				return fmt.Errorf("%s 的端口 %d 已被占用，请先在设置中调整", key, port)
			}
		}
	}
	if tun, _ := lookup(values, "tun_mode").(bool); tun {
		if !sysproc.IsAdmin() {
			if runtime.GOOS == "windows" {
				return errors.New("FDU TUN 需要管理员权限，请以管理员身份重新打开本程序")
			}
			return errors.New("FDU TUN 需要 root 权限，请用 sudo 启动本程序或关闭 TUN")
		}
		if runtime.GOOS == "windows" {
			if _, err := os.Stat(filepath.Join(filepath.Dir(r.Core), "wintun.dll")); err != nil {
				return errors.New("FDU TUN 需要与程序同目录的 wintun.dll")
			}
		}
	}
	return nil
}

// Start launches the core for config. action is "" to connect or one of Tools.
func (r *Runtime) Start(config string, values map[string]any, action string) error {
	return r.start(config, values, action, false)
}

// StartFresh requests a new aTrust login without deleting saved settings or cookies.
func (r *Runtime) StartFresh(config string, values map[string]any) error {
	return r.start(config, values, "", true)
}

func (r *Runtime) start(config string, values map[string]any, action string, fresh bool) error {
	stdout, stderr, done, err := r.launch(config, values, action, fresh)
	if err != nil {
		return err
	}
	// Readers start after this, so the child's own states always follow it.
	if action == "" {
		r.emitState("connecting")
	} else {
		r.emitState("tool-running")
	}
	readers := make(chan struct{}, 2)
	go func() { r.readStdout(stdout); readers <- struct{}{} }()
	go func() { r.readLines(stderr, r.log); readers <- struct{}{} }()
	go func() {
		r.mu.Lock()
		cmd := r.cmd
		r.mu.Unlock()
		_ = cmd.Wait()
		// Let trailing output drain, but never hang on a pipe a grandchild inherited.
		timeout := time.After(2 * time.Second)
	drain:
		for i := 0; i < 2; i++ {
			select {
			case <-readers:
			case <-timeout:
				break drain
			}
		}
		stdout.Close()
		stderr.Close()
		r.mu.Lock()
		r.connected = false
		close(done)
		r.mu.Unlock()
		if action == "" {
			r.emitState("stopped")
		} else {
			r.emitState("tool-finished")
		}
		r.log(fmt.Sprintf("进程退出，代码 %d。", cmd.ProcessState.ExitCode()))
	}()
	return nil
}

// launch validates and starts the child under the lock; callbacks run only after it returns.
func (r *Runtime) launch(config string, values map[string]any, action string, fresh bool) (*os.File, *os.File, chan struct{}, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runningLocked() {
		return nil, nil, nil, errors.New("本窗口的 FDU 已在运行")
	}
	if action != "" && !Tools[action] {
		return nil, nil, nil, errors.New("不支持的客户端操作")
	}
	config, err := filepath.Abs(config)
	if err != nil {
		return nil, nil, nil, err
	}
	if _, err := os.Stat(r.Core); err != nil {
		return nil, nil, nil, fmt.Errorf("未找到 %s，请确认它与本程序位于同一目录", CoreName)
	}
	if _, err := os.Stat(config); err != nil {
		return nil, nil, nil, errors.New("请先在设置页保存配置")
	}
	if action == "" {
		if err := r.preflight(values); err != nil {
			return nil, nil, nil, err
		}
	}
	args := []string{"-config", config}
	if action != "" {
		args = append(args, "-"+action)
	}
	cmd := exec.Command(r.Core, args...)
	cmd.Dir = filepath.Dir(config)
	hideConsole(cmd)
	for _, entry := range os.Environ() {
		upper := strings.ToUpper(entry)
		if !strings.HasPrefix(upper, "ZJU_CONNECT_") && !strings.HasPrefix(upper, "FDU_CONNECT_GUI=") && !strings.HasPrefix(upper, "FDU_CONNECT_GUI_RELOGIN=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(append(cmd.Env, "FDU_CONNECT_GUI=1"), r.Env...)
	if fresh {
		cmd.Env = append(cmd.Env, "FDU_CONNECT_GUI_RELOGIN=1")
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stderr, stderrWriter, err := os.Pipe()
	if err != nil {
		stdout.Close()
		stdoutWriter.Close()
		return nil, nil, nil, err
	}
	cmd.Stdout, cmd.Stderr = stdoutWriter, stderrWriter
	err = cmd.Start()
	stdoutWriter.Close()
	stderrWriter.Close()
	if err != nil {
		stdout.Close()
		stderr.Close()
		return nil, nil, nil, fmt.Errorf("无法启动 FDU：%w", err)
	}
	r.secrets, r.connected, r.tool = secretsOf(values), false, action
	r.cmd, r.stdin, r.done = cmd, stdin, make(chan struct{})
	return stdout, stderr, r.done, nil
}

func (r *Runtime) readLines(reader io.Reader, handle func(string)) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		handle(scanner.Text())
	}
}

func (r *Runtime) readStdout(reader io.Reader) {
	r.readLines(reader, func(line string) {
		payload, ok := strings.CutPrefix(line, guiPrefix)
		if !ok {
			r.log(line)
			return
		}
		var message map[string]any
		if json.Unmarshal([]byte(payload), &message) != nil {
			r.log("客户端事件格式异常；原始认证数据未写入日志。")
			return
		}
		switch message["kind"] {
		case "challenge":
			r.emitState("authentication")
			if r.OnChallenge != nil {
				r.OnChallenge(message)
			}
		case "state":
			state, _ := message["state"].(string)
			r.mu.Lock()
			r.connected = state == "connected"
			r.mu.Unlock()
			r.emitState(state)
		}
	})
}

func (r *Runtime) emitState(state string) {
	if r.OnState != nil {
		r.OnState(state)
	}
}

func (r *Runtime) log(line string) {
	r.mu.Lock()
	secrets := r.secrets
	r.mu.Unlock()
	if r.OnLog != nil {
		r.OnLog(Redact(line, secrets))
	}
}

func (r *Runtime) Connected() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runningLocked() && r.connected
}

// Respond sends one JSON line to the child; it is a no-op once the child exited.
func (r *Runtime) Respond(response map[string]any) error {
	r.mu.Lock()
	stdin, running := r.stdin, r.runningLocked()
	r.mu.Unlock()
	if !running {
		return nil
	}
	b, err := json.Marshal(response)
	if err != nil {
		return err
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	_, err = stdin.Write(append(b, '\n'))
	return err
}

// Stop asks the child to exit and reports whether it did within timeout.
func (r *Runtime) Stop(timeout time.Duration) (bool, error) {
	r.mu.Lock()
	done, running := r.done, r.runningLocked()
	r.mu.Unlock()
	if !running {
		return true, nil
	}
	if err := r.Respond(map[string]any{"operation": "stop"}); err != nil {
		return false, err
	}
	select {
	case <-done:
		return true, nil
	case <-time.After(timeout):
		return false, nil
	}
}

func (r *Runtime) Kill() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.runningLocked() {
		return nil
	}
	return r.cmd.Process.Kill()
}

// Wait blocks until the current child exits.
func (r *Runtime) Wait() {
	r.mu.Lock()
	done := r.done
	r.mu.Unlock()
	if done != nil {
		<-done
	}
}

var (
	queryPattern  = regexp.MustCompile(`(?i)(https?://[^\s?"<>]+)\?[^\s"<>]+`)
	headerPattern = regexp.MustCompile(`(?i)((?:authorization|cookie|set-cookie)["']?\s*[:=]\s*["']?)[^\r\n"'}]+`)
	fieldPattern  = regexp.MustCompile(`(?i)((?:password|passwd|token|authorization|signKey|sign_key|sid|code|ticket)["']?\s*[:=]\s*["']?)[^\s,"'}]+`)
)

// Redact removes credentials and callback parameters before a line reaches the log view.
func Redact(text string, secrets []string) string {
	if strings.Contains(strings.ToLower(text), "given auth data:") {
		return "[认证缓存内容已隐藏]"
	}
	for _, secret := range secrets {
		text = strings.ReplaceAll(text, secret, "***")
	}
	text = queryPattern.ReplaceAllString(text, "${1}?***")
	text = headerPattern.ReplaceAllString(text, "${1}***")
	return fieldPattern.ReplaceAllString(text, "${1}***")
}
