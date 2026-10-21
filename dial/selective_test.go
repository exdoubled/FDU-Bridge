package dial

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/mythologyli/zju-connect/client"
)

func TestDialVPNStrictACLAndNoDirectFallback(t *testing.T) {
	resource := client.IPResource{IPMin: net.ParseIP("10.20.26.253"), IPMax: net.ParseIP("10.20.26.253"), PortMin: 80, PortMax: 80, Protocol: "tcp"}
	d := NewDialer(&capturingStack{}, nil, []client.IPResource{resource}, false, "")
	if _, err := d.DialVPN(context.Background(), "tcp", "10.20.26.253:80"); err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct{ network, addr string }{{"tcp", "10.20.26.253:443"}, {"udp", "10.20.26.253:80"}, {"tcp", "127.0.0.1:80"}} {
		if _, err := d.DialVPN(context.Background(), target.network, target.addr); !errors.Is(err, ErrACLDenied) {
			t.Fatalf("%v: %v", target, err)
		}
	}
	d = NewDialer(&capturingStack{}, nil, nil, false, "")
	if _, err := d.DialVPN(context.Background(), "tcp", "10.20.26.253:80"); !errors.Is(err, ErrACLDenied) {
		t.Fatalf("missing ACL = %v", err)
	}
	for _, addr := range []string{"[fd00::1]:80", "campus.invalid:80", "invalid", "10.0.0.1:0"} {
		if _, err := d.DialVPN(context.Background(), "tcp", addr); err == nil {
			t.Fatalf("accepted %s", addr)
		}
	}
}
