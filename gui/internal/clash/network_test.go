package clash

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiagnosticReadAllowlist(t *testing.T) {
	for path, want := range map[string]bool{
		"http://other.example/dns/query?name=api.openai.com&type=A": false,
		"/connections":                                       true,
		"/dns/query?name=api.openai.com&type=A":              false,
		"/dns/query?name=icourse.fudan.edu.cn&type=AAAA":     true,
		"/dns/query?name=unrelated.example&type=A":           false,
		"/dns/query?name=api.openai.com.evil.example&type=A": false,
		"/dns/query?name=api.openai.com&type=TXT":            false,
		"/dns/query?name=api.openai.com&type=A&extra=1":      false,
		"/connections?delete=all":                            false,
	} {
		if got := allowed("GET", path); got != want {
			t.Errorf("GET %s = %v, want %v", path, got, want)
		}
	}
	if allowed("DELETE", "/connections") {
		t.Fatal("diagnostics must not close connections")
	}
}

func TestLargeConnectionSnapshotsKeepTheirRouteMetadata(t *testing.T) {
	var body strings.Builder
	body.WriteString(`{"uploadTotal":8000,"downloadTotal":16000,"connections":[`)
	for i := 0; i < 8000; i++ {
		if i > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"id":"%d","upload":1,"download":2,"chains":["DIRECT"],"padding":"%s"}`, i, strings.Repeat("x", 512))
	}
	body.WriteString(`]}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body.String()) }))
	defer server.Close()
	api := ControlAPI{Network: "tcp", Address: strings.TrimPrefix(server.URL, "http://")}
	var snapshot ConnectionSnapshot
	if err := api.Do("GET", "/connections", nil, &snapshot); err != nil || len(snapshot.Connections) != 8000 || snapshot.Connections[0].Chains[0] != "DIRECT" {
		t.Fatalf("a large connection snapshot lost accounting data: %d / %v", len(snapshot.Connections), err)
	}
	if err := api.Do("GET", "/proxies", nil, nil); err == nil {
		t.Fatal("non-connection responses must retain their smaller bound")
	}
}
