package underlay

import (
	"context"
	"net"
	"strconv"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/miekg/dns"
	"golang.org/x/sys/windows"
)

func TestBoundDialUsesSystemDNSWhenAdapterDNSIsBlocked(t *testing.T) {
	serve := func(ips ...string) string {
		packet, err := net.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		server := &dns.Server{PacketConn: packet, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, query *dns.Msg) {
			if len(ips) == 0 {
				return
			}
			answer := new(dns.Msg)
			answer.SetReply(query)
			for _, ip := range ips {
				answer.Answer = append(answer.Answer, &dns.A{Hdr: dns.RR_Header{Name: query.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.ParseIP(ip)})
			}
			_ = w.WriteMsg(answer)
		})}
		go func() { _ = server.ActivateAndServe() }()
		t.Cleanup(func() { _ = server.Shutdown() })
		return packet.LocalAddr().String()
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	loopback := ""
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 {
			loopback = iface.Name
			break
		}
	}
	if loopback == "" {
		t.Skip("no loopback interface")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	address := net.JoinHostPort("vpn-system-dns.test", strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
	physical := serve("127.0.0.1")
	originalDNS, originalResolver := interfaceDNSServer, net.DefaultResolver
	t.Cleanup(func() { interfaceDNSServer, net.DefaultResolver = originalDNS, originalResolver })
	for _, tc := range []struct {
		name       string
		systemIPs  []string
		adapterDNS string
	}{
		{"blocked adapter DNS", []string{"127.0.0.1"}, "127.0.0.1:1"},
		{"synthetic system answer", []string{"198.18.0.8"}, physical},
		{"later system address succeeds", []string{"127.0.0.2", "127.0.0.1"}, "127.0.0.1:1"},
		{"slow system DNS preserves physical fallback", nil, physical},
	} {
		t.Run(tc.name, func(t *testing.T) {
			system := serve(tc.systemIPs...)
			net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, system)
			}}
			interfaceDNSServer = func(string) string { return tc.adapterDNS }
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			conn, err := dialContextOnInterface(ctx, "tcp4", address, loopback, "")
			if err != nil {
				t.Fatalf("could not connect using a real DNS answer: %v", err)
			}
			conn.Close()
		})
	}
}

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
