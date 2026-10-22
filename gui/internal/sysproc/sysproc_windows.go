package sysproc

import (
	"errors"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func List() ([]Entry, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	var result []Entry
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		result = append(result, Entry{entry.ProcessID, windows.UTF16ToString(entry.ExeFile[:])})
	}
	if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		err = nil
	}
	return result, err
}

func CommandLine(pid uint32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	size := uint32(4096)
	for attempt := 0; attempt < 4; attempt++ {
		buffer := make([]byte, size)
		var needed uint32
		err = windows.NtQueryInformationProcess(h, windows.ProcessCommandLineInformation, unsafe.Pointer(&buffer[0]), size, &needed)
		if err == nil {
			text := (*windows.NTUnicodeString)(unsafe.Pointer(&buffer[0]))
			return text.String(), nil
		}
		if needed <= size {
			return "", err
		}
		size = needed
	}
	return "", err
}

// StartTime reports when a still-running process was created.
func StartTime(pid uint32) (time.Time, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return time.Time{}, err
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err = windows.GetExitCodeProcess(h, &code); err != nil {
		return time.Time{}, err
	}
	if code != 259 { // STILL_ACTIVE
		return time.Time{}, errors.New("process exited")
	}
	var created, exited, kernel, user windows.Filetime
	if err = windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return time.Time{}, err
	}
	return time.Unix(0, created.Nanoseconds()), nil
}

func IsAdmin() bool { return windows.GetCurrentProcessToken().IsElevated() }

// WithMutex serializes fn with other processes sharing the same local mutex name.
func WithMutex(name string, timeout time.Duration, fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	text, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	h, err := windows.CreateMutex(nil, false, text)
	if h == 0 {
		return err
	}
	defer windows.CloseHandle(h)
	event, err := windows.WaitForSingleObject(h, uint32(timeout/time.Millisecond))
	if event != windows.WAIT_OBJECT_0 && event != windows.WAIT_ABANDONED {
		if err == nil {
			err = ErrMutexBusy
		}
		return err
	}
	defer windows.ReleaseMutex(h)
	return fn()
}
