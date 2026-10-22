package sysproc

import (
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestConfigArgument(t *testing.T) {
	for line, want := range map[string]string{
		`fdu-connect.exe -config "C:\a b\c.toml"`:                                 `C:\a b\c.toml`,
		`fdu-connect.exe --config C:\x.toml -v`:                                   `C:\x.toml`,
		`fdu-connect.exe -v`:                                                      "",
		joinArgs([]string{"/opt/fdu/fdu-connect", "-config", "/home/a b/c.toml"}): "/home/a b/c.toml",
	} {
		if got := ConfigArgument(line); got != want {
			t.Errorf("%s: got %q want %q", line, got, want)
		}
	}
}

func TestSelfInspection(t *testing.T) {
	line, err := CommandLine(uint32(os.Getpid()))
	if err != nil || !strings.Contains(strings.ToLower(line), ".test") {
		t.Fatalf("command line %q %v", line, err)
	}
	started, err := StartTime(uint32(os.Getpid()))
	if err != nil || time.Since(started) < -time.Second || time.Since(started) > time.Hour {
		t.Fatalf("start time %v %v", started, err)
	}
	if _, err := StartTime(0x7fffffff); err == nil {
		t.Fatal("missing process reported alive")
	}
	entries, err := List()
	found := false
	for _, e := range entries {
		found = found || e.PID == uint32(os.Getpid())
	}
	if err != nil || !found {
		t.Fatalf("own process not listed: %v", err)
	}
}

func TestListeningPorts(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if !PortInUse(port) {
		t.Fatalf("listener %d not reported", port)
	}
	if used := UsedPorts(port, port+1); len(used) != 1 || used[0] != port {
		t.Fatalf("UsedPorts missed %d: %v", port, used)
	}
	l.Close()
	if PortInUse(port) {
		t.Fatalf("closed listener %d still reported", port)
	}
}

func TestMutexSerializes(t *testing.T) {
	name := `Local\FduGuiTest-` + time.Now().Format("150405.000000")
	entered := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = WithMutex(name, time.Second, func() error { close(entered); <-release; return nil })
	}()
	<-entered
	if err := WithMutex(name, 50*time.Millisecond, func() error { return nil }); err == nil {
		t.Fatal("mutex not exclusive")
	}
	close(release)
	if err := WithMutex(name, time.Second, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
}
