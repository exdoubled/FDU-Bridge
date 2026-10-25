package bootstrap

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"unsafe"

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
	s := startupDiagnostics(LogPath, showError)
	s.Logger.Info("Starting FDU Connect")
	return s, checkRuntime(RuntimeAvailable)
}

func startupDiagnostics(logPath func() (string, error), showError func(string)) *Startup {
	path, err := logPath()
	if err != nil {
		s := newStartup(showError)
		s.HandleError(fmt.Errorf("无法定位启动日志目录: %w", err))
		return s
	}
	s, err := openStartup(filepath.Dir(path), showError)
	if err != nil {
		s.HandleError(err)
	}
	return s
}

// RuntimeAvailable only inspects the registration and files used by Wails. The
// setup executable calls it before launching any user interface or opening logs.
func RuntimeAvailable() (bool, error) {
	return findInstalledRuntime(func(root registry.Key) (string, error) {
		key, err := registry.OpenKey(root, runtimeKey, registry.QUERY_VALUE|registry.WOW64_32KEY)
		if err != nil {
			return "", err
		}
		folder, _, err := key.GetStringValue("EBWebView")
		_ = key.Close()
		return folder, err
	})
}

func findInstalledRuntime(readFolder func(registry.Key) (string, error)) (bool, error) {
	var readErr error
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		folder, err := readFolder(root)
		if errors.Is(err, registry.ErrNotExist) {
			continue
		}
		if err != nil {
			readErr = errors.Join(readErr, err)
			continue
		}
		if runtimePresent(folder, runtime.GOARCH) {
			return true, nil
		}
	}
	return false, readErr
}

func showError(message string) { messageBox(message, 0x10) }

func messageBox(message string, icon uintptr) {
	text, _ := windows.UTF16PtrFromString(message)
	title, _ := windows.UTF16PtrFromString("FDU Connect")
	proc := windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW")
	_, _, _ = proc.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), icon|0x10000)
}
