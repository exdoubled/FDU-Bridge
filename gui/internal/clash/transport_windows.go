package clash

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func controllerMissing(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, windows.WSAECONNREFUSED)
}

func dialPipe(ctx context.Context, path string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, path)
}

// defaultControllers lists Verge's mihomo pipes; names vary with the run mode
// (verge-mihomo, verge-mihomo-production-*, verge-mihomo-sidecar-release-*).
func defaultControllers(string) ([]ControlAPI, error) {
	pattern, _ := windows.UTF16PtrFromString(`\\.\pipe\*`)
	var data windows.Win32finddata
	h, err := windows.FindFirstFile(pattern, &data)
	var found []ControlAPI
	if err == nil {
		for {
			if name := windows.UTF16ToString(data.FileName[:]); strings.HasPrefix(name, "verge-mihomo") {
				found = append(found, ControlAPI{Network: "pipe", Address: `\\.\pipe\` + name})
			}
			if windows.FindNextFile(h, &data) != nil {
				break
			}
		}
		windows.FindClose(h)
	}
	if len(found) > 1 {
		return nil, errors.New("发现多个 Clash 控制接口，无法确定当前实例；请在设置中确认 Clash Verge 配置目录")
	}
	return found, nil
}
