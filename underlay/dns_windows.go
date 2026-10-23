package underlay

import (
	"net"
	"unsafe"

	"golang.org/x/sys/windows"
)

// GetAdaptersAddresses preserves the selected adapter's DNS even when the
// system resolver list is headed by a Clash TUN adapter. Prefer IPv4 for aTrust.
func systemInterfaceDNSServer(name string) string {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return ""
	}
	size := uint32(15000)
	for {
		buffer := make([]byte, size)
		first := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buffer[0]))
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, windows.GAA_FLAG_SKIP_ANYCAST|windows.GAA_FLAG_SKIP_MULTICAST, 0, first, &size)
		if err == windows.ERROR_BUFFER_OVERFLOW {
			continue
		}
		if err != nil {
			return ""
		}
		for adapter := first; adapter != nil; adapter = adapter.Next {
			if adapter.IfIndex != uint32(iface.Index) && adapter.Ipv6IfIndex != uint32(iface.Index) {
				continue
			}
			return adapterDNSServer(name, adapter.FirstDnsServerAddress)
		}
		return ""
	}
}

func adapterDNSServer(name string, first *windows.IpAdapterDnsServerAdapter) string {
	ipv6 := ""
	for server := first; server != nil; server = server.Next {
		ip := server.Address.IP()
		if ip == nil || ip.IsUnspecified() || ip.IsLoopback() {
			continue
		}
		if ipv4 := ip.To4(); ipv4 != nil {
			// Clash may put its TUN DNS on the physical adapter itself.
			// That address is unreachable when bound to the physical NIC.
			if ipv4[0] == 198 && (ipv4[1] == 18 || ipv4[1] == 19) {
				continue
			}
			return net.JoinHostPort(ip.String(), "53")
		}
		host := ip.String()
		if ip.IsLinkLocalUnicast() {
			host += "%" + name
		}
		if ipv6 == "" {
			ipv6 = net.JoinHostPort(host, "53")
		}
	}
	return ipv6
}
