package sysproc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// procArgs reads argv through kern.procargs2; it fails for other users' processes unless root.
func procArgs(pid uint32) ([]string, error) {
	b, err := unix.SysctlRaw("kern.procargs2", int(pid))
	if err != nil {
		return nil, err
	}
	if len(b) < 4 {
		return nil, errors.New("short procargs")
	}
	argc := int(binary.LittleEndian.Uint32(b))
	rest := b[4:]
	// Skip the executable path and the NUL padding after it.
	if i := bytes.IndexByte(rest, 0); i >= 0 {
		rest = rest[i:]
	}
	rest = bytes.TrimLeft(rest, "\x00")
	args := make([]string, 0, argc)
	for len(args) < argc && len(rest) > 0 {
		i := bytes.IndexByte(rest, 0)
		if i < 0 {
			i = len(rest)
		}
		args = append(args, string(rest[:i]))
		rest = rest[min(i+1, len(rest)):]
	}
	return args, nil
}

func commName(p *unix.KinfoProc) string {
	comm := p.Proc.P_comm[:]
	if i := bytes.IndexByte(comm, 0); i >= 0 {
		comm = comm[:i]
	}
	return string(comm)
}

// List names processes by argv[0] when readable; comm alone is cut to 16 bytes,
// which turns fdu-connect-rules into fdu-connect-rul.
func List() ([]Entry, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	result := make([]Entry, 0, len(procs))
	for i := range procs {
		pid := uint32(procs[i].Proc.P_pid)
		name := commName(&procs[i])
		if strings.HasPrefix(name, "fdu-connect") {
			if args, err := procArgs(pid); err == nil && len(args) > 0 {
				name = filepath.Base(args[0])
			} else if len(name) >= 15 && strings.HasPrefix("fdu-connect-rules", name) {
				name = "fdu-connect-rules"
			}
		}
		result = append(result, Entry{pid, name})
	}
	return result, nil
}

func CommandLine(pid uint32) (string, error) {
	args, err := procArgs(pid)
	if err != nil {
		return "", err
	}
	return joinArgs(args), nil
}

// StartTime reports when a still-running process was created.
func StartTime(pid uint32) (time.Time, error) {
	p, err := unix.SysctlKinfoProc("kern.proc.pid", int(pid))
	if err != nil {
		return time.Time{}, err
	}
	if p.Proc.P_pid != int32(pid) || p.Proc.P_stat == 5 { // SZOMB
		return time.Time{}, errors.New("process exited")
	}
	return time.Unix(p.Proc.P_starttime.Unix()), nil
}

// portChecker probes by binding: BSD sockets refuse 0.0.0.0:p or 127.0.0.1:p when another
// socket holds that exact pair, and the two probes together cover wildcard and loopback holders.
func portChecker() func(int) bool {
	return func(port int) bool {
		for _, network := range [][2]string{{"tcp4", "0.0.0.0"}, {"tcp4", "127.0.0.1"}, {"tcp6", "::"}} {
			l, err := net.Listen(network[0], net.JoinHostPort(network[1], itoa(port)))
			if err == nil {
				l.Close()
				continue
			}
			if errors.Is(err, syscall.EADDRINUSE) {
				return true
			}
		}
		return false
	}
}

func PortInUse(port int) bool { return portChecker()(port) }
