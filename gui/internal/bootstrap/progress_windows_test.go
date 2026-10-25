package bootstrap

import (
	"encoding/binary"
	"fmt"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestInstallProgressScalesWithDPI(t *testing.T) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	gdi32 := windows.NewLazySystemDLL("gdi32.dll")
	type rect struct{ left, top, right, bottom int32 }
	baseFontHeight := 0
	for _, dpi := range []uintptr{96, 144, 192} {
		t.Run(fmt.Sprint(dpi), func(t *testing.T) {
			closeProgress, err := showInstallProgressAtDPI(dpi)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(closeProgress)
			class, _ := windows.UTF16PtrFromString("FDUConnect.WebView2Install")
			window, _, _ := user32.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(class)), 0)
			var bounds rect
			if ok, _, _ := user32.NewProc("GetWindowRect").Call(window, uintptr(unsafe.Pointer(&bounds))); ok == 0 {
				t.Fatal("installation window was not created")
			}
			if bounds.right-bounds.left != int32(500*dpi/96) || bounds.bottom-bounds.top != int32(220*dpi/96) {
				t.Errorf("window at DPI %d = %+v, want scaled 500x220", dpi, bounds)
			}
			text, _, _ := user32.NewProc("GetDlgItem").Call(window, 1001)
			user32.NewProc("GetWindowRect").Call(text, uintptr(unsafe.Pointer(&bounds)))
			if bounds.right-bounds.left != int32(440*dpi/96) || bounds.bottom-bounds.top != int32(60*dpi/96) {
				t.Errorf("text at DPI %d = %+v, want scaled 440x60", dpi, bounds)
			}
			font, _, _ := user32.NewProc("SendMessageW").Call(text, 0x31, 0, 0) // WM_GETFONT
			var logFont [92]byte                                                // LOGFONTW, independent of the production struct layout.
			if size, _, _ := gdi32.NewProc("GetObjectW").Call(font, uintptr(len(logFont)), uintptr(unsafe.Pointer(&logFont[0]))); size != uintptr(len(logFont)) {
				t.Fatal("text control has no valid font")
			}
			fontHeight := int(int32(binary.LittleEndian.Uint32(logFont[:4])))
			if fontHeight < 0 {
				fontHeight = -fontHeight
			}
			if fontHeight == 0 {
				t.Fatal("text control has no explicit message font size")
			}
			if dpi == 96 {
				baseFontHeight = fontHeight
			}
			wantHeight := (baseFontHeight*int(dpi) + 48) / 96
			if delta := fontHeight - wantHeight; delta < -1 || delta > 1 {
				t.Errorf("font height at DPI %d = %d, want about %d", dpi, fontHeight, wantHeight)
			}
			// Animation must retain the scaled block size and stay inside its track.
			user32.NewProc("SendMessageW").Call(window, 0x113, 1, 0) // WM_TIMER
			track, _, _ := user32.NewProc("GetDlgItem").Call(window, 1002)
			block, _, _ := user32.NewProc("GetDlgItem").Call(track, 1003)
			var trackBounds rect
			user32.NewProc("GetWindowRect").Call(track, uintptr(unsafe.Pointer(&trackBounds)))
			user32.NewProc("GetWindowRect").Call(block, uintptr(unsafe.Pointer(&bounds)))
			if bounds.right-bounds.left != int32(80*dpi/96) || bounds.bottom-bounds.top != int32(16*dpi/96) ||
				bounds.left < trackBounds.left || bounds.right > trackBounds.right || bounds.top < trackBounds.top || bounds.bottom > trackBounds.bottom {
				t.Errorf("animation block at DPI %d = %+v, outside or unscaled in %+v", dpi, bounds, trackBounds)
			}
			closeProgress()
			if ok, _, _ := gdi32.NewProc("GetObjectW").Call(font, uintptr(len(logFont)), uintptr(unsafe.Pointer(&logFont[0]))); ok != 0 {
				t.Error("window font was not released after close")
			}
		})
	}
}

func TestInstallProgressStaysVisibleUntilInstallerFinishes(t *testing.T) {
	closeProgress, err := showInstallProgress()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeProgress)
	user32 := windows.NewLazySystemDLL("user32.dll")
	class, _ := windows.UTF16PtrFromString("FDUConnect.WebView2Install")
	window, _, _ := user32.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(class)), 0)
	visible, _, _ := user32.NewProc("IsWindowVisible").Call(window)
	if window == 0 || visible == 0 {
		t.Fatal("installer status window is not visible")
	}
	// Dismissing the status must not leave a silent, still-running installer.
	user32.NewProc("SendMessageW").Call(window, 0x10, 0, 0) // WM_CLOSE
	visible, _, _ = user32.NewProc("IsWindowVisible").Call(window)
	if visible == 0 {
		t.Fatal("status closed while installer was still running")
	}
	var result uintptr
	responsive, _, _ := user32.NewProc("SendMessageTimeoutW").Call(window, 0, 0, 0, 2, 1000, uintptr(unsafe.Pointer(&result)))
	if responsive == 0 {
		t.Fatal("installer status window is not pumping messages")
	}
	track, _, _ := user32.NewProc("GetDlgItem").Call(window, 1002)
	block, _, _ := user32.NewProc("GetDlgItem").Call(track, 1003)
	type rect struct{ left, top, right, bottom int32 }
	var before, after rect
	user32.NewProc("GetWindowRect").Call(block, uintptr(unsafe.Pointer(&before)))
	time.Sleep(150 * time.Millisecond)
	user32.NewProc("GetWindowRect").Call(block, uintptr(unsafe.Pointer(&after)))
	if before == after {
		t.Fatal("installation activity indicator did not animate")
	}
	closed := make(chan struct{})
	go func() {
		closeProgress()
		closeProgress()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("closing installer status window hung")
	}
	remaining, _, _ := user32.NewProc("IsWindow").Call(window)
	if remaining != 0 {
		t.Fatal("installer status window leaked after close")
	}
}
