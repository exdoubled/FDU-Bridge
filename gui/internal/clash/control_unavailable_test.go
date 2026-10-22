package clash

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mythologyli/zju-connect/gui/internal/fsx"
)

func TestControllerUnavailabilityExcludesHTTPRejections(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	api := ControlAPI{Network: "tcp", Address: address, Timeout: time.Second}
	if err := api.Do("GET", "/configs", nil, nil); !errors.Is(err, ErrControllerUnavailable) {
		t.Fatalf("lost controller not classified: %v", err)
	}
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"unauthorized", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }},
		{"timeout", func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }},
		{"incomplete", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte("partial"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(tc.handler)
			defer server.Close()
			api.Address, api.Timeout = strings.TrimPrefix(server.URL, "http://"), 100*time.Millisecond
			if err := api.Do("GET", "/configs", nil, nil); err == nil || errors.Is(err, ErrControllerUnavailable) {
				t.Fatalf("uncertain failure treated as stopped Clash: %v", err)
			}
		})
	}
}

type goneOnReloadAPI struct{}

func (goneOnReloadAPI) Do(string, string, any, any) error { return ErrControllerUnavailable }

func TestProviderRollbackKeepsControllerFailureCause(t *testing.T) {
	path := filepath.Join(t.TempDir(), "provider.yaml")
	data := []byte("payload: ['DOMAIN,campus.invalid']\n")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	a := CampusAttachment{ProviderPath: path, ProviderHash: fsx.Hash(data)}
	if _, err := replaceCampusProvider(goneOnReloadAPI{}, a, []byte("payload: []\n"), true); !errors.Is(err, ErrControllerUnavailable) {
		t.Fatalf("lost unavailable-controller cause during rollback: %v", err)
	}
}
