package atrust

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mythologyli/zju-connect/internal/zctcpip"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newSessionTestClient() *Client {
	return NewClient(ClientOptions{
		Session: SessionOptions{
			Username: "user",
			SID:      "sid",
			DeviceID: "device",
		},
	})
}

func TestForwardFromConnPreservesPacket(t *testing.T) {
	transport := &trackingNetConn{closed: make(chan struct{})}
	conn := &l3TunnelConn{
		tlsConn:      tls.Client(transport, &tls.Config{InsecureSkipVerify: true}),
		incoming:     make(chan []byte, 1),
		closeCh:      make(chan struct{}),
		conntrackMgr: newConntrackMgr(),
	}
	tunnel := &L3Tunnel{
		conns:    map[string]*l3TunnelConn{"group": conn},
		dataChan: make(chan []byte, 1),
		closeCh:  make(chan struct{}),
	}
	want := makeUDPPacket(12345, 53)
	done := make(chan struct{})
	go func() {
		tunnel.forwardFromConn("group", conn)
		close(done)
	}()
	conn.incoming <- want

	select {
	case got := <-tunnel.dataChan:
		if !bytes.Equal(got, want) {
			t.Fatalf("forwarded packet = % X, want % X", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("packet was not forwarded")
	}
	_ = conn.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("forwarder did not stop after connection close")
	}
}

func TestGetConnCoalescesConcurrentConnects(t *testing.T) {
	client := newSessionTestClient()
	client.BestNodes = map[string]string{"group": "node:443"}
	tunnel := &L3Tunnel{
		client:     client,
		conns:      make(map[string]*l3TunnelConn),
		connecting: make(map[string]*l3TunnelConnectCall),
		dataChan:   make(chan []byte, 1),
		closeCh:    make(chan struct{}),
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	transport := &trackingNetConn{closed: make(chan struct{})}
	want := &l3TunnelConn{
		tlsConn:      tls.Client(transport, &tls.Config{InsecureSkipVerify: true}),
		incoming:     make(chan []byte),
		closeCh:      make(chan struct{}),
		conntrackMgr: newConntrackMgr(),
	}
	tunnel.connect = func(context.Context, string, *conntrackMgr) (*l3TunnelConn, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return want, nil
	}

	const callers = 16
	results := make(chan *l3TunnelConn, callers)
	errs := make(chan error, callers)
	for range callers {
		go func() {
			conn, err := tunnel.getConn("group")
			results <- conn
			errs <- err
		}()
	}
	<-started
	time.Sleep(20 * time.Millisecond)
	close(release)

	for range callers {
		if err := <-errs; err != nil {
			t.Fatalf("getConn() error = %v", err)
		}
		if got := <-results; got != want {
			t.Fatalf("getConn() = %p, want %p", got, want)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("connect calls = %d, want 1", got)
	}
	tunnel.Close()
}

func TestTunnelReconnectUsesFixedIntervalUntilSuccessAndSharesResult(t *testing.T) {
	client := newSessionTestClient()
	client.BestNodes = map[string]string{"group": "node:443"}
	tunnel := &L3Tunnel{
		client:            client,
		conns:             make(map[string]*l3TunnelConn),
		connecting:        make(map[string]*l3TunnelConnectCall),
		dataChan:          make(chan []byte, 1),
		closeCh:           make(chan struct{}),
		reconnectInterval: 10 * time.Millisecond,
	}
	transport := &trackingNetConn{closed: make(chan struct{})}
	want := &l3TunnelConn{
		tlsConn:      tls.Client(transport, &tls.Config{InsecureSkipVerify: true}),
		incoming:     make(chan []byte),
		closeCh:      make(chan struct{}),
		conntrackMgr: newConntrackMgr(),
	}
	var calls atomic.Int32
	var attemptTimes []time.Time
	var attemptTimesMu sync.Mutex
	tunnel.connect = func(context.Context, string, *conntrackMgr) (*l3TunnelConn, error) {
		call := calls.Add(1)
		attemptTimesMu.Lock()
		attemptTimes = append(attemptTimes, time.Now())
		attemptTimesMu.Unlock()
		if call <= 6 {
			return nil, fmt.Errorf("dial failed: %w", net.ErrClosed)
		}
		return want, nil
	}

	tunnel.startReconnect("group")
	got, err := tunnel.getConn("group")
	if err != nil {
		t.Fatalf("getConn() error = %v", err)
	}
	if got != want {
		t.Fatalf("getConn() = %p, want %p", got, want)
	}
	if calls.Load() != 7 {
		t.Fatalf("connect calls = %d, want 7", calls.Load())
	}
	attemptTimesMu.Lock()
	defer attemptTimesMu.Unlock()
	for i := 1; i < len(attemptTimes); i++ {
		elapsed := attemptTimes[i].Sub(attemptTimes[i-1])
		if elapsed < 8*time.Millisecond || elapsed > 100*time.Millisecond {
			t.Fatalf("retry delay %d = %s, want fixed 10ms interval", i, elapsed)
		}
	}
	tunnel.Close()
}

func TestTunnelReconnectStopsWhenTunnelCloses(t *testing.T) {
	client := newSessionTestClient()
	client.BestNodes = map[string]string{"group": "node:443"}
	tunnel := &L3Tunnel{
		client:            client,
		conns:             make(map[string]*l3TunnelConn),
		connecting:        make(map[string]*l3TunnelConnectCall),
		dataChan:          make(chan []byte, 1),
		closeCh:           make(chan struct{}),
		reconnectInterval: time.Hour,
	}
	attempted := make(chan struct{})
	tunnel.connect = func(context.Context, string, *conntrackMgr) (*l3TunnelConn, error) {
		close(attempted)
		return nil, fmt.Errorf("dial failed: %w", net.ErrClosed)
	}

	result := make(chan error, 1)
	go func() {
		_, err := tunnel.getConn("group")
		result <- err
	}()
	<-attempted
	tunnel.Close()
	select {
	case err := <-result:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("getConn() error = %v, want net.ErrClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("getConn() did not stop after tunnel close")
	}
}

func TestTunnelReconnectPreservesConntrackAndToken(t *testing.T) {
	client := newSessionTestClient()
	client.BestNodes = map[string]string{"group": "node:443"}
	tunnel := &L3Tunnel{
		client:            client,
		conns:             make(map[string]*l3TunnelConn),
		connecting:        make(map[string]*l3TunnelConnectCall),
		conntrackMgrs:     make(map[string]*conntrackMgr),
		dataChan:          make(chan []byte, 1),
		closeCh:           make(chan struct{}),
		reconnectInterval: time.Millisecond,
	}
	var calls atomic.Int32
	var first, second *l3TunnelConn
	tunnel.connect = func(_ context.Context, _ string, manager *conntrackMgr) (*l3TunnelConn, error) {
		conn := &l3TunnelConn{
			tlsConn:      tls.Client(&trackingNetConn{closed: make(chan struct{})}, &tls.Config{InsecureSkipVerify: true}),
			incoming:     make(chan []byte),
			closeCh:      make(chan struct{}),
			conntrackMgr: manager,
		}
		if calls.Add(1) == 1 {
			first = conn
		} else {
			second = conn
		}
		return conn, nil
	}

	conn, err := tunnel.getConn("group")
	if err != nil {
		t.Fatal(err)
	}
	manager := conn.conntrackMgr
	ct := manager.getOrCreate("flow", "app", "group")
	manager.completeAuth(ct.authID, "connect-token", nil)

	tunnel.evictConn("group", conn)
	tunnel.startReconnect("group")
	reconnected, err := tunnel.getConn("group")
	if err != nil {
		t.Fatal(err)
	}
	if first != conn || second != reconnected || calls.Load() != 2 {
		t.Fatalf("connections = first %p second %p calls %d", first, second, calls.Load())
	}
	if reconnected.conntrackMgr != manager {
		t.Fatal("reconnect replaced the node group's conntrack manager")
	}
	if manager.getByKey(ct.key) != ct {
		t.Fatal("reconnect replaced the authenticated conntrack")
	}
	if token, authErr, authenticated := manager.authResult(ct); !authenticated || authErr != nil || token != "connect-token" {
		t.Fatalf("auth result after reconnect = token %q, err %v, authenticated %t", token, authErr, authenticated)
	}
	manager.mu.Lock()
	closedBeforeTunnelClose := manager.closed
	manager.mu.Unlock()
	if closedBeforeTunnelClose {
		t.Fatal("transport close also closed the shared conntrack manager")
	}

	tunnel.Close()
	manager.mu.Lock()
	closedAfterTunnelClose := manager.closed
	manager.mu.Unlock()
	if !closedAfterTunnelClose {
		t.Fatal("tunnel close did not close the shared conntrack manager")
	}
}

func TestTunnelRecoversFromReadAndHeartbeatFailures(t *testing.T) {
	tests := []struct {
		name  string
		start func(*l3TunnelConn)
	}{
		{
			name: "TLS EOF",
			start: func(conn *l3TunnelConn) {
				conn.reader = bufio.NewReader(bytes.NewReader(nil))
				go conn.readLoop()
			},
		},
		{
			name: "unknown version",
			start: func(conn *l3TunnelConn) {
				conn.reader = bufio.NewReader(bytes.NewReader([]byte{0xff, cmdDataResp}))
				go conn.readLoop()
			},
		},
		{
			name: "unknown command",
			start: func(conn *l3TunnelConn) {
				conn.reader = bufio.NewReader(bytes.NewReader([]byte{l3Version, 0x92}))
				go conn.readLoop()
			},
		},
		{
			name: "late tunnel auth",
			start: func(conn *l3TunnelConn) {
				conn.reader = bufio.NewReader(bytes.NewReader([]byte{0x53, 0x00, 0x00, 0x00}))
				go conn.readLoop()
			},
		},
		{
			name: "truncated frame",
			start: func(conn *l3TunnelConn) {
				conn.reader = bufio.NewReader(bytes.NewReader([]byte{l3Version, cmdDataResp, 0x00, 0x04, 0x45}))
				go conn.readLoop()
			},
		},
		{
			name: "heartbeat lost",
			start: func(conn *l3TunnelConn) {
				conn.heartbeatInterval = time.Millisecond
				conn.heartbeatMissLimit = 1
				atomic.StoreInt32(&conn.heartbeatMisses, 1)
				go conn.heartbeatLoop()
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newSessionTestClient()
			client.BestNodes = map[string]string{"group": "node:443"}
			manager := newConntrackMgr()
			oldConn := &l3TunnelConn{
				tlsConn:      tls.Client(&trackingNetConn{closed: make(chan struct{})}, &tls.Config{InsecureSkipVerify: true}),
				incoming:     make(chan []byte),
				closeCh:      make(chan struct{}),
				conntrackMgr: manager,
			}
			reconnected := make(chan *l3TunnelConn, 1)
			tunnel := &L3Tunnel{
				client:            client,
				conns:             map[string]*l3TunnelConn{"group": oldConn},
				connecting:        make(map[string]*l3TunnelConnectCall),
				conntrackMgrs:     map[string]*conntrackMgr{"group": manager},
				dataChan:          make(chan []byte, 1),
				closeCh:           make(chan struct{}),
				reconnectInterval: time.Millisecond,
			}
			tunnel.connect = func(_ context.Context, _ string, gotManager *conntrackMgr) (*l3TunnelConn, error) {
				conn := &l3TunnelConn{
					tlsConn:      tls.Client(&trackingNetConn{closed: make(chan struct{})}, &tls.Config{InsecureSkipVerify: true}),
					incoming:     make(chan []byte),
					closeCh:      make(chan struct{}),
					conntrackMgr: gotManager,
				}
				reconnected <- conn
				return conn, nil
			}

			go tunnel.forwardFromConn("group", oldConn)
			test.start(oldConn)
			select {
			case newConn := <-reconnected:
				if newConn.conntrackMgr != manager {
					t.Fatal("recovery replaced conntrack manager")
				}
			case <-time.After(time.Second):
				t.Fatal("failure did not establish a replacement tunnel")
			}
			tunnel.Close()
		})
	}
}

type limitedReader struct {
	reader io.Reader
	limit  int
}

func (r limitedReader) Read(p []byte) (int, error) {
	if len(p) > r.limit {
		p = p[:r.limit]
	}
	return r.reader.Read(p)
}

func TestReadFrameHandlesSplitAndCoalescedFrames(t *testing.T) {
	data := []byte{
		l3Version, cmdAuthResp, 0x84, 0x00, 0x02, 'o', 'k',
		l3Version, cmdHeartbeatResp, 0x00, 0x00,
	}
	conn := &l3TunnelConn{reader: bufio.NewReaderSize(limitedReader{reader: bytes.NewReader(data), limit: 1}, 2)}

	authFrame, err := conn.readFrame()
	if err != nil {
		t.Fatalf("readFrame(auth) error = %v", err)
	}
	if authFrame.cmd != cmdAuthResp || authFrame.status != 0x84 || !bytes.Equal(authFrame.payload, []byte("ok")) {
		t.Fatalf("auth frame = %#v", authFrame)
	}
	heartbeatFrame, err := conn.readFrame()
	if err != nil {
		t.Fatalf("readFrame(heartbeat) error = %v", err)
	}
	if heartbeatFrame.cmd != cmdHeartbeatResp || heartbeatFrame.status != 0 || len(heartbeatFrame.payload) != 0 {
		t.Fatalf("heartbeat frame = %#v", heartbeatFrame)
	}
}

func TestL3TunnelCloseUnblocksL3ConnRead(t *testing.T) {
	tunnel := &L3Tunnel{
		conns:    make(map[string]*l3TunnelConn),
		dataChan: make(chan []byte),
		closeCh:  make(chan struct{}),
	}
	conn := &L3Conn{l3Tunnel: tunnel, closeCh: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, err := conn.Read(make([]byte, 1500))
		done <- err
	}()

	tunnel.Close()
	select {
	case err := <-done:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Read() error = %v, want EOF", err)
		}
	case <-time.After(time.Second):
		t.Fatal("tunnel close did not unblock Read")
	}
}

func TestPendingPacketsFlushInOrderAfterAuthentication(t *testing.T) {
	manager := newConntrackMgr()
	frames := make([][]byte, 0, 3)
	conn := &l3TunnelConn{
		closeCh:      make(chan struct{}),
		authWake:     make(chan struct{}, 1),
		conntrackMgr: manager,
		writeFrameHook: func(frame []byte) error {
			frames = append(frames, append([]byte(nil), frame...))
			return nil
		},
	}
	meta := packetMeta{
		atype: 4, proto: 17,
		srcIP: net.IPv4(192, 0, 2, 1), dstIP: net.IPv4(198, 51, 100, 1),
		srcPort: 12345, dstPort: 53, key: "flow",
	}
	first := makeUDPPacket(12345, 53)
	wantFirst := append([]byte(nil), first...)
	second := makeUDPPacket(12345, 54)
	if err := conn.WritePacket(meta, "app", "group", first); err != nil {
		t.Fatalf("first WritePacket() error = %v", err)
	}
	if err := conn.WritePacket(meta, "app", "group", second); err != nil {
		t.Fatalf("second WritePacket() error = %v", err)
	}
	first[0] = 0
	if len(frames) != 0 {
		t.Fatalf("wrote %d frames before authentication, want 0", len(frames))
	}

	jobs, more := manager.nextAuthBatch(defaultAuthBatchSize)
	if len(jobs) != 1 || more {
		t.Fatalf("auth batch = %d, more=%t, want 1, false", len(jobs), more)
	}
	if err := conn.sendAuthRequest(jobs[0].conntrack, jobs[0].meta); err != nil {
		t.Fatalf("sendAuthRequest() error = %v", err)
	}
	manager.markAuthSent(jobs[0].conntrack.authID, time.Now().Add(defaultAuthTimeout))
	response, err := json.Marshal(authResponseIP{
		Data: authResponseIPData{ConntrackHash: jobs[0].conntrack.authID, ConnectToken: "token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	conn.handleAuthResp(0, response)

	if len(frames) != 3 {
		t.Fatalf("frame count = %d, want auth plus two data frames", len(frames))
	}
	if frames[0][1] != cmdAuthReq || frames[1][1] != cmdDataReq || frames[2][1] != cmdDataReq {
		t.Fatalf("frame commands = %02x %02x %02x", frames[0][1], frames[1][1], frames[2][1])
	}
	firstPackets, err := parseDataPayload(frames[1][2:])
	if err != nil || len(firstPackets) != 1 || !bytes.Equal(firstPackets[0], wantFirst) {
		t.Fatalf("first flushed packet was not the cached copy: packets=%v err=%v", firstPackets, err)
	}
	secondPackets, err := parseDataPayload(frames[2][2:])
	if err != nil || len(secondPackets) != 1 || !bytes.Equal(secondPackets[0], second) {
		t.Fatalf("second flushed packet mismatch: packets=%v err=%v", secondPackets, err)
	}
}

func makeUDPPacket(srcPort, dstPort uint16) []byte {
	packet := make(zctcpip.IPv4Packet, zctcpip.IPv4HeaderSize+zctcpip.UDPHeaderSize)
	packet[0] = zctcpip.IPv4Version << 4
	packet.SetHeaderLen(zctcpip.IPv4HeaderSize)
	packet.SetTotalLength(uint16(len(packet)))
	packet.SetProtocol(zctcpip.UDP)
	packet.SetSourceIP(net.IPv4(192, 0, 2, 1))
	packet.SetDestinationIP(net.IPv4(198, 51, 100, 1))
	udpPacket := zctcpip.UDPPacket(packet.Payload())
	udpPacket.SetSourcePort(srcPort)
	udpPacket.SetDestinationPort(dstPort)
	udpPacket.SetLength(zctcpip.UDPHeaderSize)
	return packet
}

type trackingNetConn struct {
	closed     chan struct{}
	closeOnce  sync.Once
	deadlineMu sync.Mutex
	deadlines  []time.Time
}

func (*trackingNetConn) Read([]byte) (int, error) { return 0, io.EOF }

func (c *trackingNetConn) Write(p []byte) (int, error) { return len(p), nil }

func (c *trackingNetConn) Close() error { c.closeOnce.Do(func() { close(c.closed) }); return nil }

func (*trackingNetConn) LocalAddr() net.Addr { return &net.TCPAddr{} }

func (*trackingNetConn) RemoteAddr() net.Addr { return &net.TCPAddr{} }

func (c *trackingNetConn) SetDeadline(deadline time.Time) error {
	c.deadlineMu.Lock()
	c.deadlines = append(c.deadlines, deadline)
	c.deadlineMu.Unlock()
	return nil
}

func (*trackingNetConn) SetReadDeadline(time.Time) error { return nil }

func (*trackingNetConn) SetWriteDeadline(time.Time) error { return nil }

func (c *trackingNetConn) recordedDeadlines() []time.Time {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	return append([]time.Time(nil), c.deadlines...)
}

func TestTCPTunnelReadDecodesDownstreamFramesAcrossBufferSizes(t *testing.T) {
	for _, bufferSize := range []int{2, 5, 16} {
		t.Run(fmt.Sprintf("buffer_%d", bufferSize), func(t *testing.T) {
			payload := []byte("hello")
			frame := append([]byte{0x01, 0x00, 0x00, byte(len(payload))}, payload...)
			conn := &tcpTunnelConn{reader: bufio.NewReader(bytes.NewReader(frame))}
			var got []byte
			buf := make([]byte, bufferSize)
			for len(got) < len(payload) {
				n, err := conn.Read(buf)
				if err != nil {
					t.Fatalf("Read() error = %v", err)
				}
				got = append(got, buf[:n]...)
			}
			if !bytes.Equal(got, payload) {
				t.Fatalf("Read() payload = %q, want %q", got, payload)
			}
		})
	}
}

func TestWaitForTCPConnectPreservesBufferedDownstreamData(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	payload := []byte("HTTP/1.1 200 OK\r\n")
	serverErrCh := make(chan error, 1)
	go func() {
		response := append([]byte{0x05, 0x81, 0x53, 0x00, 0x00, 0x21}, []byte(`{"code":0,"message":"Successful"}`)...)
		if _, err := server.Write(response); err != nil {
			serverErrCh <- err
			return
		}
		statusAndPayload := append([]byte{0x05, 0x00, 0x00, 0x01, 10, 0, 0, 1, 0x1F, 0x90, 0x01, 0x00, 0x00, byte(len(payload))}, payload...)
		_, err := server.Write(statusAndPayload)
		serverErrCh <- err
	}()

	reader := bufio.NewReader(client)
	if err := waitForTCPConnect(context.Background(), client, reader); err != nil {
		t.Fatalf("waitForTCPConnect() error = %v", err)
	}
	conn := &tcpTunnelConn{reader: reader}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("Read() payload = %q, want %q", got, payload)
	}
	if err := <-serverErrCh; err != nil {
		t.Fatalf("server exchange failed: %v", err)
	}
}

func TestTCPTunnelWriteEncodesUpstreamFrame(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	payload := []byte("GET / HTTP/1.1\r\n\r\n")
	serverResult := make(chan []byte, 1)
	go func() {
		got := make([]byte, 4+len(payload))
		if _, err := io.ReadFull(server, got); err != nil {
			serverResult <- nil
			return
		}
		serverResult <- got
	}()

	conn := &tcpTunnelConn{tlsConn: client}
	n, err := conn.Write(payload)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != len(payload) {
		t.Fatalf("Write() length = %d, want %d", n, len(payload))
	}
	want := append([]byte{0x01, 0x00, 0x00, byte(len(payload))}, payload...)
	if got := <-serverResult; !bytes.Equal(got, want) {
		t.Fatalf("upstream frame = % X, want % X", got, want)
	}
}
