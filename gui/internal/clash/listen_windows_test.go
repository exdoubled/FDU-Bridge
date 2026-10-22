package clash

import (
	"net"
	"testing"

	"github.com/Microsoft/go-winio"
)

func listenControl(t *testing.T, name string) (net.Listener, ControlAPI) {
	path := `\\.\pipe\` + name
	l, err := winio.ListenPipe(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	return l, ControlAPI{Network: "pipe", Address: path}
}
