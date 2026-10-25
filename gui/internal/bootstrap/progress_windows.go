package bootstrap

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	progressUser32       = windows.NewLazySystemDLL("user32.dll")
	progressGDI32        = windows.NewLazySystemDLL("gdi32.dll")
	progressCreateWindow = progressUser32.NewProc("CreateWindowExW")
	progressSendMessage  = progressUser32.NewProc("SendMessageW")
	progressGetItem      = progressUser32.NewProc("GetDlgItem")
	progressClass        = windows.StringToUTF16Ptr("FDUConnect.WebView2Install")
	progressClassOnce    sync.Once
	progressClassError   error
	progressCallback     = windows.NewCallback(progressWindowProc)
)

type progressWindowClass struct {
	style                  uint32
	procedure              uintptr
	classExtra, winExtra   int32
	instance, icon, cursor uintptr
	background             uintptr
	menu, name             *uint16
}

type progressMessage struct {
	window         uintptr
	message        uint32
	wParam, lParam uintptr
	time           uint32
	x, y           int32
	private        uint32
}

type progressLogFont struct {
	height, width, escapement, orientation, weight int32
	italic, underline, strikeOut, charSet          byte
	outPrecision, clipPrecision, quality, family   byte
	faceName                                       [32]uint16
}

type progressNonclientMetrics struct {
	size                                                                uint32
	borderWidth, scrollWidth, scrollHeight, captionWidth, captionHeight int32
	captionFont                                                         progressLogFont
	smallCaptionWidth, smallCaptionHeight                               int32
	smallCaptionFont                                                    progressLogFont
	menuWidth, menuHeight                                               int32
	menuFont, statusFont, messageFont                                   progressLogFont
	paddedBorderWidth                                                   int32
}

func progressFont(dpi uintptr) (uintptr, error) {
	metrics := progressNonclientMetrics{}
	metrics.size = uint32(unsafe.Sizeof(metrics))
	ok, _, err := progressUser32.NewProc("SystemParametersInfoForDpi").Call(
		0x29, uintptr(metrics.size), uintptr(unsafe.Pointer(&metrics)), 0, dpi) // SPI_GETNONCLIENTMETRICS
	if ok == 0 {
		return 0, fmt.Errorf("读取安装窗口字体: %w", err)
	}
	font, _, _ := progressGDI32.NewProc("CreateFontIndirectW").Call(uintptr(unsafe.Pointer(&metrics.messageFont)))
	if font == 0 {
		return 0, fmt.Errorf("创建安装窗口字体失败")
	}
	return font, nil
}

// showInstallProgress uses only user32 controls, so it also works before WebView2
// is installed and without a Common Controls v6 manifest. The moving indicator
// shows activity rather than an invented installation percentage.
func showInstallProgress() (func(), error) {
	dpi, _, _ := progressUser32.NewProc("GetDpiForSystem").Call()
	return showInstallProgressAtDPI(dpi)
}

func showInstallProgressAtDPI(dpi uintptr) (func(), error) {
	type readyWindow struct {
		window uintptr
		err    error
	}
	ready := make(chan readyWindow, 1)
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(done)
		instance, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW").Call(0)
		progressClassOnce.Do(func() {
			cursor, _, _ := progressUser32.NewProc("LoadCursorW").Call(0, 32512) // IDC_ARROW
			class := progressWindowClass{procedure: progressCallback, instance: instance, cursor: cursor, background: 16, name: progressClass}
			if registered, _, err := progressUser32.NewProc("RegisterClassW").Call(uintptr(unsafe.Pointer(&class))); registered == 0 {
				progressClassError = fmt.Errorf("注册安装状态窗口: %w", err)
			}
		})
		if progressClassError != nil {
			ready <- readyWindow{err: progressClassError}
			return
		}
		font, err := progressFont(dpi)
		if err != nil {
			ready <- readyWindow{err: err}
			return
		}
		defer progressGDI32.NewProc("DeleteObject").Call(font)
		scale := func(value uintptr) uintptr { return (value*dpi + 48) / 96 }
		width, _, _ := progressUser32.NewProc("GetSystemMetrics").Call(0)
		height, _, _ := progressUser32.NewProc("GetSystemMetrics").Call(1)
		windowWidth, windowHeight := scale(500), scale(220)
		window, _, err := progressCreateWindow.Call(0x40000, uintptr(unsafe.Pointer(progressClass)),
			uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("FDU Connect — 首次启动"))),
			0x00C80000, uintptr((int(width)-int(windowWidth))/2), uintptr((int(height)-int(windowHeight))/2),
			windowWidth, windowHeight, 0, 0, instance, 0)
		if window == 0 {
			ready <- readyWindow{err: fmt.Errorf("创建安装状态窗口: %w", err)}
			return
		}
		defer progressUser32.NewProc("DestroyWindow").Call(window)
		menu, _, _ := progressUser32.NewProc("GetSystemMenu").Call(window, 0)
		progressUser32.NewProc("EnableMenuItem").Call(menu, 0xF060, 1) // SC_CLOSE, MF_GRAYED
		createStatic := func(parent, id, style, x, y, width, height uintptr, text string) (uintptr, error) {
			control, _, err := progressCreateWindow.Call(0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("STATIC"))),
				uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(text))), 0x50000000|style,
				scale(x), scale(y), scale(width), scale(height), parent, id, instance, 0)
			if control == 0 {
				return 0, err
			}
			progressSendMessage.Call(control, 0x30, font, 1) // WM_SETFONT
			return control, nil
		}
		_, textErr := createStatic(window, 1001, 0, 24, 24, 440, 60,
			"正在安装 Microsoft WebView2 Runtime…\r\n通常需要一至数分钟，完成后将自动打开 FDU Connect。")
		track, trackErr := createStatic(window, 1002, 0x12, 24, 100, 440, 20, "") // SS_ETCHEDFRAME
		_, blockErr := createStatic(track, 1003, 5, 2, 2, 80, 16, "")             // SS_GRAYRECT
		_, noteErr := createStatic(window, 1004, 0, 24, 140, 440, 28, "正在离线安装，请勿重复启动。")
		for _, err := range []error{textErr, trackErr, blockErr, noteErr} {
			if err != nil {
				ready <- readyWindow{err: fmt.Errorf("创建安装状态控件: %w", err)}
				return
			}
		}
		if timer, _, err := progressUser32.NewProc("SetTimer").Call(window, 1, 50, 0); timer == 0 {
			ready <- readyWindow{err: fmt.Errorf("启动安装状态动画: %w", err)}
			return
		}
		progressUser32.NewProc("ShowWindow").Call(window, 5)
		progressUser32.NewProc("UpdateWindow").Call(window)
		ready <- readyWindow{window: window}
		var message progressMessage
		for {
			result, _, _ := progressUser32.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
			if int32(result) <= 0 {
				return
			}
			progressUser32.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&message)))
			progressUser32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&message)))
		}
	}()
	result := <-ready
	if result.err != nil {
		<-done
		return nil, result.err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			progressUser32.NewProc("PostMessageW").Call(result.window, 0x8000, 0, 0) // WM_APP
			<-done
		})
	}, nil
}

func progressWindowProc(window, message, wParam, lParam uintptr) uintptr {
	switch message {
	case 0x10: // WM_CLOSE: installation is still running; keep its status visible.
		return 0
	case 0x8000: // WM_APP: the installer has finished.
		progressUser32.NewProc("DestroyWindow").Call(window)
		return 0
	case 2: // WM_DESTROY
		progressUser32.NewProc("PostQuitMessage").Call(0)
		return 0
	case 0x113: // WM_TIMER
		ticks, _, _ := progressUser32.NewProc("GetWindowLongPtrW").Call(window, ^uintptr(20)) // GWLP_USERDATA (-21)
		ticks++
		progressUser32.NewProc("SetWindowLongPtrW").Call(window, ^uintptr(20), ticks)
		track, _, _ := progressGetItem.Call(window, 1002)
		block, _, _ := progressGetItem.Call(track, 1003)
		position := ticks % 140
		if position > 70 {
			position = 140 - position
		}
		var bounds struct{ left, top, right, bottom int32 }
		progressUser32.NewProc("GetClientRect").Call(track, uintptr(unsafe.Pointer(&bounds)))
		progressUser32.NewProc("SetWindowPos").Call(block, 0,
			(2+position*5)*uintptr(bounds.right)/440, 2*uintptr(bounds.bottom)/20, 0, 0, 0x15) // Preserve scaled size.
		if ticks%20 == 0 {
			note, _, _ := progressGetItem.Call(window, 1004)
			text := windows.StringToUTF16Ptr(fmt.Sprintf("已等待 %d 秒，请勿重复启动。", ticks/20))
			progressUser32.NewProc("SetWindowTextW").Call(note, uintptr(unsafe.Pointer(text)))
		}
		return 0
	}
	result, _, _ := progressUser32.NewProc("DefWindowProcW").Call(window, message, wParam, lParam)
	return result
}
