package updates

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func detachInstallCommand(cmd *exec.Cmd, hidden bool) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: hidden, CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP}
}

func openInstallParent(pid int) (func(time.Duration) error, func(), error) {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return func(time.Duration) error { return nil }, func() {}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return func(timeout time.Duration) error {
		state, err := windows.WaitForSingleObject(handle, uint32(timeout/time.Millisecond))
		if err != nil {
			return err
		}
		if state != windows.WAIT_OBJECT_0 {
			return fmt.Errorf("parent GUI did not exit before update timeout (status %d)", state)
		}
		return nil
	}, func() { _ = windows.CloseHandle(handle) }, nil
}
