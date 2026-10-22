//go:build !windows

package updates

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

func detachInstallCommand(cmd *exec.Cmd, _ bool) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func openInstallParent(pid int) (func(time.Duration) error, func(), error) {
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.ESRCH) {
		return nil, nil, err
	}
	return func(timeout time.Duration) error {
		deadline := time.Now().Add(timeout)
		for {
			err := syscall.Kill(pid, 0)
			if errors.Is(err, syscall.ESRCH) {
				return nil
			}
			if err != nil {
				return err
			}
			if time.Now().After(deadline) {
				return errors.New("parent GUI did not exit before update timeout")
			}
			time.Sleep(100 * time.Millisecond)
		}
	}, func() {}, nil
}
