package sysproc

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func cmdline(pid string) []string {
	b, err := os.ReadFile(filepath.Join("/proc", pid, "cmdline"))
	if err != nil || len(b) == 0 {
		return nil
	}
	return strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
}

// List names processes by argv[0], which unlike comm is not truncated to 15 bytes.
func List() ([]Entry, error) {
	dirs, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var result []Entry
	for _, dir := range dirs {
		pid, err := strconv.ParseUint(dir.Name(), 10, 32)
		if err != nil {
			continue
		}
		if args := cmdline(dir.Name()); len(args) > 0 {
			result = append(result, Entry{uint32(pid), filepath.Base(args[0])})
		}
	}
	return result, nil
}

func CommandLine(pid uint32) (string, error) {
	args := cmdline(strconv.FormatUint(uint64(pid), 10))
	if args == nil {
		return "", errors.New("process not found")
	}
	return joinArgs(args), nil
}

// StartTime reports when a still-running process was created.
func StartTime(pid uint32) (time.Time, error) {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.FormatUint(uint64(pid), 10), "stat"))
	if err != nil {
		return time.Time{}, err
	}
	// comm may contain spaces and parentheses; the fields we need follow the last ')'.
	fields := strings.Fields(string(b[bytes.LastIndexByte(b, ')')+1:]))
	if len(fields) < 20 || fields[0] == "Z" || fields[0] == "X" {
		return time.Time{}, errors.New("process exited")
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	uptime, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return time.Time{}, err
	}
	seconds, err := strconv.ParseFloat(strings.Fields(string(uptime))[0], 64)
	if err != nil {
		return time.Time{}, err
	}
	// USER_HZ is 100 on every Linux ABI Go supports.
	boot := time.Now().Add(-time.Duration(seconds * float64(time.Second)))
	return boot.Add(time.Duration(ticks) * 10 * time.Millisecond), nil
}

func portChecker() func(int) bool {
	seen := map[int]bool{}
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(table)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 4 || fields[3] != "0A" { // TCP_LISTEN
				continue
			}
			if i := strings.LastIndexByte(fields[1], ':'); i >= 0 {
				if port, err := strconv.ParseUint(fields[1][i+1:], 16, 16); err == nil {
					seen[int(port)] = true
				}
			}
		}
		f.Close()
	}
	return func(port int) bool { return seen[port] }
}

func PortInUse(port int) bool { return portChecker()(port) }
