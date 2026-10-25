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

const runtimeKeyPrefix = `Software\Microsoft\EdgeUpdate\ClientState\`

// Match Wails beta.34's default Runtime, Beta, Dev and Canary search order.
var runtimeChannels = [...]string{
	"{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}",
	"{2CD8A007-E189-409D-A2C8-9AF4EF3C72AA}",
	"{0D50BFEC-CD6A-4F9A-964C-C7416E3ACB10}",
	"{65C35B14-6C1D-4122-AC46-7148CC9D6497}",
}

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
	return s, s.checkRuntime(RuntimeAvailable)
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
	return findInstalledRuntime(func(root registry.Key, path string) (string, error) {
		key, err := registry.OpenKey(root, path, registry.QUERY_VALUE|registry.WOW64_32KEY)
		if err != nil {
			return "", err
		}
		folder, _, err := key.GetStringValue("EBWebView")
		_ = key.Close()
		return folder, err
	})
}

func findInstalledRuntime(readFolder func(registry.Key, string) (string, error)) (bool, error) {
	var readErr error
	for _, channel := range runtimeChannels {
		for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
			folder, err := readFolder(root, runtimeKeyPrefix+channel)
			if errors.Is(err, registry.ErrNotExist) {
				continue
			}
			if err != nil {
				readErr = errors.Join(readErr, err)
				continue
			}
			available, err := runtimePresent(folder, runtime.GOARCH)
			if available {
				return true, nil
			}
			readErr = errors.Join(readErr, err)
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
