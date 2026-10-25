package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"github.com/mythologyli/zju-connect/gui/internal/sysproc"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// This is the Evergreen Runtime registration, not the Edge browser channel.
const runtimeKey = `Software\Microsoft\EdgeUpdate\ClientState\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`

func LogPath() (string, error) {
	local, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(local, "FDU Connect", "logs", "startup.log"), nil
}

func Prepare() (*Startup, error) {
	logPath, err := LogPath()
	if err != nil {
		return &Startup{path: "无法定位 LocalAppData 启动日志", showError: showError}, fmt.Errorf("无法定位启动日志目录: %w", err)
	}
	s, err := openStartup(filepath.Dir(logPath), showError)
	if err != nil {
		return s, err
	}
	s.Logger.Info("Starting FDU Connect")
	exe, err := os.Executable()
	if err != nil {
		return s, err
	}
	err = sysproc.WithMutex(`Local\FDUConnect.WebView2Bootstrap`, 6*time.Minute, func() error {
		return (runtimeInstaller{
			probe: installedRuntime,
			run:   runInstaller,
			notify: func() {
				s.Logger.Info("Installing bundled WebView2 Runtime")
				messageBox("首次启动需要安装 Microsoft WebView2 Runtime。\n\n点击“确定”后将自动离线安装，通常需要一至数分钟，请勿重复启动。安装完成后 FDU Connect 会自动打开。", 0x40)
			},
			timeout: 5 * time.Minute,
		}).ensure(filepath.Dir(exe), WebView2InstallerSHA256)
	})
	return s, err
}

func installedRuntime() (bool, error) {
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		key, err := registry.OpenKey(root, runtimeKey, registry.QUERY_VALUE|registry.WOW64_32KEY)
		if errors.Is(err, registry.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
		folder, _, err := key.GetStringValue("EBWebView")
		_ = key.Close()
		if errors.Is(err, registry.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
		if runtimePresent(folder, runtime.GOARCH) {
			return true, nil
		}
	}
	return false, nil
}

func runInstaller(ctx context.Context, path string) error {
	cmd := exec.CommandContext(ctx, path, "/silent", "/install")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	// Direct CreateProcess inherits the asInvoker token; never use runas or download.
	return cmd.Run()
}

func showError(message string) { messageBox(message, 0x10) }

func messageBox(message string, icon uintptr) {
	text, _ := windows.UTF16PtrFromString(message)
	title, _ := windows.UTF16PtrFromString("FDU Connect")
	proc := windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW")
	_, _, _ = proc.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), icon|0x10000)
}
