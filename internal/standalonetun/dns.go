package standalonetun

import (
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"

	"github.com/miekg/dns"
	"github.com/mythologyli/zju-connect/log"
)

// Addresses are never recycled within a session: a cached answer cannot send a
// connection to another domain. Exhaustion fails closed instead of evicting.
type names struct {
	mu        sync.Mutex
	byName    map[string]netip.Addr
	byIP      map[netip.Addr]string
	exhausted bool
}

func newNames() *names { return &names{byName: map[string]netip.Addr{}, byIP: map[netip.Addr]string{}} }

var fakeRange = netip.MustParsePrefix("198.18.0.0/15")

func (n *names) target(address string) (string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !fakeRange.Contains(ip.Unmap()) {
		return address, nil
	}
	n.mu.Lock()
	name, ok := n.byIP[ip.Unmap()]
	n.mu.Unlock()
	if !ok {
		return "", errors.New("unknown TUN DNS address; resolve the hostname again")
	}
	return net.JoinHostPort(name, port), nil
}

func (n *names) answer(raw []byte) ([]byte, error) {
	var query dns.Msg
	if err := query.Unpack(raw); err != nil {
		return nil, err
	}
	reply := new(dns.Msg)
	reply.SetReply(&query)
	reply.RecursionAvailable = true
	if query.Opcode != dns.OpcodeQuery || len(query.Question) != 1 {
		reply.Rcode = dns.RcodeFormatError
		return reply.Pack()
	}
	q := query.Question[0]
	// IPv4 fake answers carry the original hostname to either outbound. AAAA and
	// HTTPS/SVCB return NODATA so applications resolve A without bypassing rules.
	if q.Qclass == dns.ClassINET && q.Qtype == dns.TypeA {
		name := strings.ToLower(strings.TrimSuffix(q.Name, "."))
		n.mu.Lock()
		ip, ok := n.byName[name]
		if !ok && len(n.byName) < 65534 {
			index := uint32(len(n.byName)) + 1
			ip = netip.AddrFrom4([4]byte{198, 19, byte(index >> 8), byte(index)})
			n.byName[name], n.byIP[ip] = ip, name
		}
		report := !ok && !ip.IsValid() && !n.exhausted
		n.exhausted = n.exhausted || report
		n.mu.Unlock()
		if report {
			log.Printf("TUN DNS: fake address pool exhausted; new hostnames fail until FDU is restarted")
		}
		if !ip.IsValid() {
			reply.Rcode = dns.RcodeServerFailure
		} else {
			reply.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 1}, A: net.IP(ip.AsSlice())}}
		}
	} else if q.Qtype != dns.TypeAAAA && q.Qtype != dns.TypeHTTPS && q.Qtype != dns.TypeSVCB {
		reply.Rcode = dns.RcodeNotImplemented
	}
	return reply.Pack()
}

// forwardDNS keeps LAN and private resolvers (routers, split-DNS servers) on the
// physical network instead of answering them with fake addresses.
func forwardDNS(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()
}
