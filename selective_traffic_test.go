//go:build !tun

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mythologyli/zju-connect/configs"
	"github.com/mythologyli/zju-connect/routing"
)

func TestSelectiveTrafficLifecycle(t *testing.T) {
	if os.Getenv("FDU_SELECTIVE_TRAFFIC_TEST_CHILD") == "1" {
		r, err := routing.New(configs.RoutingConfig{UpstreamProxy: "socks5://127.0.0.1:7902", Domains: []string{"api.openai.com"}})
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		r.SetVPN(func(context.Context, string, string) (net.Conn, error) {
			local, peer := net.Pipe()
			go func() { defer peer.Close(); _, _ = io.Copy(io.Discard, peer) }()
			return local, nil
		})
		ctx, cancel := context.WithCancel(context.Background())
		stop := startSelectiveTraffic(ctx, r)
		time.Sleep(1100 * time.Millisecond)
		conn, err := r.Dial(ctx, "tcp", "api.openai.com:443")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = conn.Write([]byte("tail")); err != nil {
			t.Fatal(err)
		}
		conn.Close()
		cancel()
		stop()
		stop()
		fmt.Println("TRAFFIC_STOPPED")
		time.Sleep(1100 * time.Millisecond)
		return
	}
	for _, enabled := range []string{"0", "1"} {
		t.Run(enabled, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestSelectiveTrafficLifecycle$")
			cmd.Env = append(os.Environ(), "FDU_SELECTIVE_TRAFFIC_TEST_CHILD=1", "FDU_CONNECT_GUI="+enabled)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("traffic subprocess: %v\n%s", err, output)
			}
			count, stopped := 0, false
			var started int64
			var finalUpload uint64
			for _, line := range strings.Split(string(output), "\n") {
				if strings.TrimSpace(line) == "TRAFFIC_STOPPED" {
					stopped = true
				}
				if !strings.HasPrefix(line, "@@FDU_GUI@@") {
					continue
				}
				if stopped {
					t.Fatalf("traffic event after joined shutdown: %s", output)
				}
				var event struct {
					Kind string                  `json:"kind"`
					Data routing.TrafficSnapshot `json:"data"`
				}
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "@@FDU_GUI@@")), &event); err != nil {
					t.Fatal(err)
				}
				if event.Kind != "traffic" || event.Data.Started <= 0 || (started != 0 && event.Data.Started != started) || event.Data.Connections == nil {
					t.Fatalf("invalid traffic snapshot: %+v", event)
				}
				started = event.Data.Started
				finalUpload = event.Data.FDU.Upload
				count++
			}
			if enabled == "0" && count != 0 || enabled == "1" && count < 2 {
				t.Fatalf("wrong initial/periodic GUI emissions (%d): %s", count, output)
			}
			if enabled == "1" && finalUpload != 4 {
				t.Fatalf("missing terminal traffic snapshot: %d", finalUpload)
			}
		})
	}
}
