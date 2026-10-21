package gvisor

import (
	"context"
	"net"

	"github.com/mythologyli/zju-connect/resolve"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
)

func (s *Stack) DialTCP(ctx context.Context, addr *net.TCPAddr) (net.Conn, error) {
	if s.endpoint.client.CanUseTCPTunnel() && !resolve.TCPPrefersL3(ctx) {
		return s.endpoint.client.DialTCP(ctx, addr)
	}

	target := tcpip.FullAddress{
		NIC:  NICID,
		Port: uint16(addr.Port),
		Addr: tcpip.AddrFromSlice(addr.IP),
	}
	if s.endpoint.onError != nil {
		return gonet.DialContextTCP(ctx, s.gvisorStack, target, header.IPv4ProtocolNumber)
	}
	return gonet.DialTCP(s.gvisorStack, target, header.IPv4ProtocolNumber)
}

func (s *Stack) DialUDP(ctx context.Context, addr *net.UDPAddr) (net.Conn, error) {
	return gonet.DialUDP(s.gvisorStack, nil, &tcpip.FullAddress{
		NIC:  NICID,
		Port: uint16(addr.Port),
		Addr: tcpip.AddrFromSlice(addr.IP),
	}, header.IPv4ProtocolNumber)
}
