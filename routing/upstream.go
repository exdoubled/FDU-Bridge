package routing

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"strconv"
	"sync"

	"github.com/mythologyli/zju-connect/internal/socksclient"
	"github.com/things-go/go-socks5/statute"
)

type upstream struct{ address, user, password string }

func newUpstream(raw string) (*upstream, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("routing: invalid upstream SOCKS5 URL")
	}
	if u.Scheme != "socks5" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("routing: upstream_proxy must be socks5://[user:password@]loopback-IP:port")
	}
	ip := net.ParseIP(u.Hostname())
	port, err := strconv.Atoi(u.Port())
	if ip == nil || !ip.IsLoopback() || err != nil || port < 1 || port > 65535 {
		return nil, errors.New("routing: upstream must use a literal loopback IP and valid port")
	}
	s := &upstream{address: net.JoinHostPort(ip.String(), strconv.Itoa(port))}
	if u.User != nil {
		s.user = u.User.Username()
		s.password, _ = u.User.Password()
		if len(s.user) == 0 || len(s.user) > 255 || len(s.password) == 0 || len(s.password) > 255 {
			return nil, errors.New("routing: SOCKS5 username/password must each contain 1..255 bytes")
		}
	}
	return s, nil
}

func (u *upstream) Dial(ctx context.Context, network, target string) (net.Conn, error) {
	switch network {
	case "tcp":
		c, _, err := socksclient.Open(ctx, u.address, u.user, u.password, statute.CommandConnect, target)
		return c, err
	case "udp":
		packet, err := statute.NewDatagram(target, nil)
		if err != nil {
			return nil, err
		}
		control, bind, err := socksclient.Open(ctx, u.address, u.user, u.password, statute.CommandAssociate, "0.0.0.0:0")
		if err != nil {
			return nil, err
		}
		relayIP := bind.IP
		if relayIP == nil || relayIP.IsUnspecified() {
			host, _, _ := net.SplitHostPort(u.address)
			relayIP = net.ParseIP(host)
		}
		if !relayIP.IsLoopback() || bind.Port < 1 {
			control.Close()
			return nil, errors.New("Clash upstream returned a non-loopback/invalid UDP relay")
		}
		udp, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: relayIP, Port: bind.Port})
		if err != nil {
			control.Close()
			return nil, err
		}
		c := &udpConn{UDPConn: udp, control: control, header: packet.Header(), target: target}
		// RFC 1928: the association terminates when the TCP control connection closes.
		go func() { io.Copy(io.Discard, control); c.Close() }()
		if ctx.Err() != nil {
			c.Close()
			return nil, ctx.Err()
		}
		return c, nil
	default:
		return nil, errors.New("SOCKS5 upstream supports only TCP/UDP")
	}
}

// udpConn exposes unframed datagrams while owning its TCP association.
type udpConn struct {
	*net.UDPConn
	control net.Conn
	header  []byte
	target  string
	once    sync.Once
}

func (c *udpConn) Write(p []byte) (int, error) {
	if len(c.header)+len(p) > 65507 {
		return 0, errors.New("SOCKS5 UDP datagram too large")
	}
	packet := make([]byte, 0, len(c.header)+len(p))
	packet = append(packet, c.header...)
	packet = append(packet, p...)
	_, err := c.UDPConn.Write(packet)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}
func (c *udpConn) Read(p []byte) (int, error) {
	buf := make([]byte, 65535)
	for {
		n, err := c.UDPConn.Read(buf)
		if err != nil {
			return 0, err
		}
		if n < 3 || buf[0] != 0 || buf[1] != 0 || buf[2] != 0 {
			continue
		}
		packet, err := statute.ParseDatagram(buf[:n])
		if err != nil {
			continue
		}
		// A domain destination may be represented by its resolved IP in replies.
		_, wantPort, _ := net.SplitHostPort(c.target)
		if strconv.Itoa(packet.DstAddr.Port) != wantPort {
			continue
		}
		return copy(p, packet.Data), nil
	}
}
func (c *udpConn) Close() error {
	var err error
	c.once.Do(func() { err = c.UDPConn.Close(); c.control.Close() })
	return err
}
