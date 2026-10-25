package bootstrap

import (
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

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
