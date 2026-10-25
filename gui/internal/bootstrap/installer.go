package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const WebView2InstallerName = "MicrosoftEdgeWebView2RuntimeInstallerX64.exe"

// WebView2InstallerSHA256 is injected when building a Windows release package.
var WebView2InstallerSHA256 string

type runtimeInstaller struct {
	probe   func() (bool, error)
	run     func(context.Context, string) error
	notify  func()
	timeout time.Duration
}

func (r runtimeInstaller) ensure(directory, digest string) error {
	available, err := r.probe()
	if err != nil {
		return fmt.Errorf("检测 WebView2 Runtime: %w", err)
	}
	if available {
		return nil
	}
	expected, err := hex.DecodeString(digest)
	if err != nil || len(expected) != sha256.Size {
		return errors.New("安装包缺少有效的 WebView2 安装器 SHA256，请重新下载完整 Windows 安装包")
	}
	installer := filepath.Join(directory, WebView2InstallerName)
	file, err := os.Open(installer)
	if err != nil {
		return fmt.Errorf("无法读取 WebView2 离线安装器，请将完整安装包解压后重试: %w", err)
	}
	hash := sha256.New()
	_, err = io.Copy(hash, file)
	closeErr := file.Close()
	if err != nil {
		return fmt.Errorf("读取 WebView2 安装器: %w", err)
	}
	if closeErr != nil {
		return closeErr
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), digest) {
		return errors.New("WebView2 安装器 SHA256 校验失败，请重新下载完整 Windows 安装包")
	}
	r.notify()
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	err = r.run(ctx, installer)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return errors.New("安装 WebView2 Runtime 超时，请稍后重新启动程序")
	}
	if err != nil {
		return fmt.Errorf("安装 WebView2 Runtime 失败: %w", err)
	}
	available, err = r.probe()
	if err != nil {
		return fmt.Errorf("安装后检测 WebView2 Runtime: %w", err)
	}
	if !available {
		return errors.New("安装后 WebView2 Runtime 仍不可用，请重新启动 Windows 后重试")
	}
	return nil
}
