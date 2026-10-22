//go:build !windows

package core

import "os/exec"

func hideConsole(*exec.Cmd) {}
