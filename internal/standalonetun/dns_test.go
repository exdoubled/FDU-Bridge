package standalonetun

import (
	"net"
	"testing"

	"github.com/miekg/dns"
)

func TestDNSPreservesDomainForRouting(t *testing.T) {
	n := newNames()
	for _, host := range []string{"campus.invalid.", "public.invalid.", "campus.invalid."} {
		q := new(dns.Msg)
		q.SetQuestion(host, dns.TypeA)
		raw, _ := q.Pack()
		raw, err := n.answer(raw)
		if err != nil {
			t.Fatal(err)
		}
		var reply dns.Msg
		if err := reply.Unpack(raw); err != nil || len(reply.Answer) != 1 {
			t.Fatalf("bad DNS: %v %+v", err, reply)
		}
		ip := reply.Answer[0].(*dns.A).A.String()
		target, err := n.target(net.JoinHostPort(ip, "443"))
		if err != nil || target != host[:len(host)-1]+":443" {
			t.Fatalf("lost hostname: %s %v", target, err)
		}
	}
	if len(n.byName) != 2 {
		t.Fatal("did not reuse existing mapping")
	}
	if _, err := n.target("198.19.200.1:443"); err == nil {
		t.Fatal("unknown cached fake IP escaped")
	}
	if target, err := n.target("10.0.0.1:443"); err != nil || target != "10.0.0.1:443" {
		t.Fatal("IP rule target changed")
	}
}

func TestDNSDoesNotAdvertiseUnmappedIPv6OrServiceHints(t *testing.T) {
	for _, kind := range []uint16{dns.TypeAAAA, dns.TypeHTTPS, dns.TypeSVCB} {
		q := new(dns.Msg)
		q.SetQuestion("campus.invalid.", kind)
		raw, _ := q.Pack()
		raw, err := newNames().answer(raw)
		if err != nil {
			t.Fatal(err)
		}
		var reply dns.Msg
		if err := reply.Unpack(raw); err != nil || len(reply.Answer) != 0 || reply.Rcode != dns.RcodeSuccess {
			t.Fatal("bad NODATA reply")
		}
	}
}

func TestDNSForwardedForNonCampusResolvers(t *testing.T) {
	for _, address := range []string{"192.168.1.1:53", "10.0.0.1:53"} {
		if !forwardDNS(address) {
			t.Fatalf("%s should be forwarded, not answered with a fake IP", address)
		}
	}
	for _, address := range []string{"8.8.8.8:53", "203.0.113.53:53", "[2001:4860:4860::8888]:53"} {
		if forwardDNS(address) {
			t.Fatalf("%s should use the fake-IP answer", address)
		}
	}
}
