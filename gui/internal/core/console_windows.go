package core

import (
	"os/exec"
	"syscall"
)

// hideConsole keeps the console-subsystem core from flashing a window.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
