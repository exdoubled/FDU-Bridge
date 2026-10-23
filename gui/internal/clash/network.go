package clash

import (
	"encoding/json"
	"net/url"
	"time"

	"github.com/mythologyli/zju-connect/routing"
)

// Diagnostics add only bounded, read-only controller requests.
func diagnosticRead(path string) bool {
	if path == "/connections" {
		return true
	}
	u, err := url.ParseRequestURI(path)
	if err != nil || u.IsAbs() || u.Host != "" || u.Path != "/dns/query" || u.Fragment != "" {
		return false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q) != 2 || len(q["name"]) != 1 || len(q["type"]) != 1 {
		return false
	}
	host := q.Get("name")
	return (q.Get("type") == "A" || q.Get("type") == "AAAA") &&
		(routing.AIProvider(host) != "" || host == "library.fudan.edu.cn" || host == "icourse.fudan.edu.cn")
}

// Only fields needed for local accounting and allowlisted AI diagnostics are read.
type ConnectionSnapshot struct {
	UploadTotal   uint64               `json:"uploadTotal"`
	DownloadTotal uint64               `json:"downloadTotal"`
	Connections   []ObservedConnection `json:"connections"`
}

type ObservedConnection struct {
	ID       string             `json:"id"`
	Metadata ConnectionMetadata `json:"metadata"`
	Upload   uint64             `json:"upload"`
	Download uint64             `json:"download"`
	Start    time.Time          `json:"start"`
	Chains   []string           `json:"chains"`
}

type ConnectionMetadata struct {
	Host            string      `json:"host"`
	SniffHost       string      `json:"sniffHost"`
	Network         string      `json:"network"`
	DestinationIP   string      `json:"destinationIP"`
	DestinationPort json.Number `json:"destinationPort"`
	InboundName     string      `json:"inboundName"`
	InboundPort     json.Number `json:"inboundPort"`
	DNSMode         string      `json:"dnsMode"`
}
