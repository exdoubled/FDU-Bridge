// Package sysproc reads other processes and ports without adopting or signalling them.
package sysproc

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// CoreNames are the executables the GUI treats as an FDU core it must not take over.
var CoreNames = func() []string {
	if runtime.GOOS == "windows" {
		return []string{"fdu-connect.exe", "fdu-connect-rules.exe"}
	}
	return []string{"fdu-connect", "fdu-connect-rules"}
}()

type Entry struct {
	PID  uint32
	Name string
}

func matches(name string, names []string) bool {
	for _, candidate := range names {
		if strings.EqualFold(name, candidate) {
			return true
		}
	}
	return false
}

// FindCores lists FDU cores other than the excluded PID (normally the GUI's own child).
func FindCores(exclude uint32) []uint32 {
	entries, _ := List()
	var pids []uint32
	for _, entry := range entries {
		if entry.PID != exclude && entry.PID != uint32(os.Getpid()) && matches(entry.Name, CoreNames) {
			pids = append(pids, entry.PID)
		}
	}
	return pids
}

var configArgument = regexp.MustCompile(`(?i)(?:--?config)\s+(?:"([^"]+)"|(\S+))`)

func ConfigArgument(commandLine string) string {
	m := configArgument.FindStringSubmatch(commandLine)
	if m == nil {
		return ""
	}
	if m[1] != "" {
		return m[1]
	}
	return m[2]
}

// joinArgs quotes arguments with spaces so ConfigArgument reads them like a Windows command line.
func joinArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if strings.ContainsAny(arg, " \t") {
			arg = `"` + arg + `"`
		}
		quoted[i] = arg
	}
	return strings.Join(quoted, " ")
}

// RunningConfig returns the single absolute, existing config used by the given cores.
func RunningConfig(pids ...uint32) string {
	if len(pids) == 0 {
		pids = FindCores(0)
	}
	seen := map[string]string{}
	for _, pid := range pids {
		line, err := CommandLine(pid)
		if err != nil {
			continue
		}
		path := ConfigArgument(line)
		if path == "" || !filepath.IsAbs(path) {
			continue
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			key := path
			if runtime.GOOS == "windows" {
				key = strings.ToUpper(path)
			}
			seen[key] = path
		}
	}
	if len(seen) != 1 {
		return ""
	}
	for _, path := range seen {
		return path
	}
	return ""
}

// UsedPorts reports which ports in [from, to) are already taken by a TCP listener.
func UsedPorts(from, to int) []int {
	var used []int
	inUse := portChecker()
	for port := from; port < to; port++ {
		if inUse(port) {
			used = append(used, port)
		}
	}
	return used
}

var ErrMutexBusy = errors.New("mutex busy")
