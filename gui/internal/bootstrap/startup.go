package bootstrap

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// Startup keeps framework diagnostics separate from the user's VPN settings.
type Startup struct {
	Logger    *slog.Logger
	path      string
	file      *os.File
	showError func(string)
	fatalOnce sync.Once
}

func openStartup(directory string, showError func(string)) (*Startup, error) {
	s := &Startup{path: filepath.Join(directory, "startup.log"), showError: showError}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return s, fmt.Errorf("无法创建启动日志目录: %w", err)
	}
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return s, fmt.Errorf("无法打开启动日志: %w", err)
	}
	s.file = file
	s.Logger = slog.New(slog.NewTextHandler(file, nil))
	return s, nil
}

func (s *Startup) HandleError(err error) {
	if s.Logger != nil {
		s.Logger.Error(err.Error())
	} else {
		log.Print(err)
	}
}

// Fail reports startup and Wails fatal errors without depending on a WebView.
func (s *Startup) Fail(err error) {
	s.HandleError(err)
	if s.file != nil {
		_ = s.file.Sync()
	}
	s.fatalOnce.Do(func() {
		if s.showError != nil {
			s.showError(fmt.Sprintf("FDU Connect 启动失败：\n%v\n\n请反馈启动日志：\n%s", err, s.path))
		}
	})
}

func (s *Startup) Close() {
	if s.file != nil {
		_ = s.file.Close()
	}
}
