package routing

import (
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// AIProvider recognizes service hostnames without attributing shared IPs or CDNs.
func AIProvider(host string) string {
	host = normalizeDomain(host)
	if !validDomain(host) {
		return ""
	}
	switch host {
	case "gemini.google.com", "aistudio.google.com", "generativelanguage.googleapis.com":
		return "gemini"
	}
	for _, service := range []struct {
		provider string
		domains  []string
	}{
		{"openai", []string{"openai.com", "chatgpt.com", "oaistatic.com", "oaiusercontent.com"}},
		{"anthropic", []string{"anthropic.com", "claude.ai", "claudeusercontent.com"}},
		{"grok", []string{"x.ai", "grok.com"}},
	} {
		for _, domain := range service.domains {
			if host == domain || strings.HasSuffix(host, "."+domain) {
				return service.provider
			}
		}
	}
	return ""
}

type TrafficTotals struct {
	Upload      uint64 `json:"upload"`
	Download    uint64 `json:"download"`
	Connections int    `json:"connections"`
}

type TrafficConnection struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Host     string `json:"host"`
	Port     string `json:"port"`
	Network  string `json:"network"`
	Route    string `json:"route"`
	TargetIP string `json:"targetIP"`
	Upload   uint64 `json:"upload"`
	Download uint64 `json:"download"`
	Started  int64  `json:"started"`
}

type TrafficSnapshot struct {
	Started     int64               `json:"started"`
	FDU         TrafficTotals       `json:"fdu"`
	NonFDU      TrafficTotals       `json:"nonFdu"`
	Connections []TrafficConnection `json:"connections"`
	Truncated   bool                `json:"truncated"`
}

type trafficCounters struct {
	upload      atomic.Uint64
	download    atomic.Uint64
	connections int // guarded by Router.mu
}

// trackTraffic runs after a successful dial while Router.mu is held. The
// selected bucket belongs to the connection and survives later rule changes.
func (r *Router) trackTraffic(c *routedConn, network, host, port string) {
	c.traffic = &r.nonFDUTraffic
	route := "non-fdu"
	if c.campus {
		c.traffic = &r.fduTraffic
		route = "fdu"
	}
	c.traffic.connections++
	provider := AIProvider(host)
	if provider == "" {
		return
	}
	c.ai = true
	r.aiActive++
	if len(r.aiConnections) >= 128 {
		return
	}
	r.trafficID++
	flow := TrafficConnection{
		ID: strconv.FormatUint(r.trafficID, 10), Provider: provider,
		Host: normalizeDomain(host), Port: port, Network: network, Route: route,
		Started: time.Now().UnixMilli(),
	}
	// RemoteAddr may describe a SOCKS relay or an aTrust gateway. Leave the
	// target unknown unless the dialer can supply verified destination metadata.
	r.aiConnections[c] = flow
}

// Traffic contains session totals and at most 128 active AI connections.
// Ordinary destinations and failed connection attempts are never retained.
func (r *Router) Traffic() TrafficSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot := TrafficSnapshot{
		Started:     r.trafficStarted,
		FDU:         TrafficTotals{Upload: r.fduTraffic.upload.Load(), Download: r.fduTraffic.download.Load(), Connections: r.fduTraffic.connections},
		NonFDU:      TrafficTotals{Upload: r.nonFDUTraffic.upload.Load(), Download: r.nonFDUTraffic.download.Load(), Connections: r.nonFDUTraffic.connections},
		Connections: make([]TrafficConnection, 0, len(r.aiConnections)),
		Truncated:   r.aiActive > len(r.aiConnections),
	}
	for c, flow := range r.aiConnections {
		flow.Upload, flow.Download = c.upload.Load(), c.download.Load()
		snapshot.Connections = append(snapshot.Connections, flow)
	}
	sort.Slice(snapshot.Connections, func(i, j int) bool {
		left, right := snapshot.Connections[i], snapshot.Connections[j]
		if left.Started != right.Started {
			return left.Started < right.Started
		}
		return left.ID < right.ID
	})
	return snapshot
}

func (c *routedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 && c.traffic != nil {
		c.traffic.download.Add(uint64(n))
		if c.ai {
			c.download.Add(uint64(n))
		}
	}
	return n, err
}

func (c *routedConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 && c.traffic != nil {
		c.traffic.upload.Add(uint64(n))
		if c.ai {
			c.upload.Add(uint64(n))
		}
	}
	return n, err
}
