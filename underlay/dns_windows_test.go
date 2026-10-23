package underlay

import (
	"net"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestAdapterDNSSkipsClashAddresses(t *testing.T) {
	for _, tc := range []struct {
		name    string
		servers []string
		want    string
	}{
		{"physical fallback", []string{"198.18.0.2", "198.19.255.254", "192.168.1.1"}, "192.168.1.1:53"},
		{"only proxy DNS", []string{"198.18.0.2", "127.0.0.1", "::"}, ""},
		{"IPv6 fallback", []string{"198.19.0.1", "2001:db8::53"}, "[2001:db8::53]:53"},
		{"IPv4 preferred", []string{"2001:db8::53", "10.0.0.53"}, "10.0.0.53:53"},
		{"below fake range", []string{"198.17.255.254"}, "198.17.255.254:53"},
		{"above fake range", []string{"198.20.0.1"}, "198.20.0.1:53"},
		{"scoped IPv6", []string{"198.18.0.2", "fe80::53"}, "[fe80::53%Ethernet]:53"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var first *windows.IpAdapterDnsServerAdapter
			for i := len(tc.servers) - 1; i >= 0; i-- {
				ip := net.ParseIP(tc.servers[i])
				raw := new(syscall.RawSockaddrAny)
				address := windows.SocketAddress{Sockaddr: raw}
				if ip4 := ip.To4(); ip4 != nil {
					sa := (*windows.RawSockaddrInet4)(unsafe.Pointer(raw))
					sa.Family = windows.AF_INET
					copy(sa.Addr[:], ip4)
					address.SockaddrLength = int32(unsafe.Sizeof(*sa))
				} else {
					sa := (*windows.RawSockaddrInet6)(unsafe.Pointer(raw))
					sa.Family = windows.AF_INET6
					copy(sa.Addr[:], ip.To16())
					address.SockaddrLength = int32(unsafe.Sizeof(*sa))
				}
				first = &windows.IpAdapterDnsServerAdapter{Address: address, Next: first}
			}
			if got := adapterDNSServer("Ethernet", first); got != tc.want {
				t.Fatalf("adapter DNS = %q, want %q", got, tc.want)
			}
		})
	}
}
