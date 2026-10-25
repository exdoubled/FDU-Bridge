package netcheck

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeCampusURLRemovesCredentialsAndPreservesPath(t *testing.T) {
	for _, test := range []struct{ input, want, host string }{
		{" Library.Fudan.edu.cn ", "https://library.fudan.edu.cn/", "library.fudan.edu.cn"},
		{"https://LIBRARY.fudan.edu.cn:443/a%20b?token=secret#private", "https://library.fudan.edu.cn:443/a%20b", "library.fudan.edu.cn"},
		{"http://10.20.1.2/path", "http://10.20.1.2/path", "10.20.1.2"},
		{"jwfw.fudan.edu.cn/xk/", "https://jwfw.fudan.edu.cn/xk/", "jwfw.fudan.edu.cn"},
		{"JWFW.fudan.edu.cn/a%20b?token=secret#private", "https://jwfw.fudan.edu.cn/a%20b", "jwfw.fudan.edu.cn"},
	} {
		got, err := NormalizeCampusURL(test.input)
		if err != nil || got.URL != test.want || got.Host != test.host {
			t.Fatalf("%q: %+v / %v", test.input, got, err)
		}
	}
	for _, input := range []string{"", "ftp://example.edu/", "https://user:pass@example.edu/", "https://user@example.edu/", "//example.edu", "https://example.edu:0", "https://example.edu:65536", "https://bad..edu/", "https://example.edu\\evil", "javascript:alert(1)", "user:pass@example.edu/path", "user@example.edu/path", "/relative/path", "bad..edu/path", "example.edu:0/path", "example.edu:65536/path", "example.edu:abc/path"} {
		if _, err := NormalizeCampusURL(input); err == nil {
			t.Fatalf("accepted unsafe input %q", input)
		}
	}
}

func TestProbeCampusIPLiteralDoesNotUseDNSLookup(t *testing.T) {
	for _, host := range []string{"10.20.1.2", "2001:db8::1"} {
		t.Run(host, func(t *testing.T) {
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Hostname() != host || r.URL.Path != "/campus" {
					t.Errorf("unexpected target %s", r.URL)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer proxy.Close()
			hostport := host
			if net.ParseIP(host).To4() == nil {
				hostport = "[" + host + "]"
			}
			lookups := 0
			result, err := ProbeCampus(context.Background(), "http://"+hostport+"/campus", Options{ProxyURL: proxy.URL, Lookup: func(_ context.Context, name string) ([]string, error) {
				lookups++
				if net.ParseIP(name) != nil {
					return nil, errors.New("Clash DNS Rcode 3")
				}
				return []string{"203.0.113.8"}, nil
			}})
			if err != nil || result.HTTPStatus != 204 || lookups != 0 {
				t.Fatalf("IP target used DNS: %+v / %v / lookups=%d", result, err, lookups)
			}
			check := findCheck(t, result, "dns")
			if check.Status != "ok" || !strings.Contains(check.Detail, "无需解析") || len(result.Answers) != 1 || result.Answers[0] != host {
				t.Fatalf("IP target DNS evidence: %+v / %v", check, result.Answers)
			}
		})
	}
}

func TestProbeCampusDoesNotSendQueryCredentialsOrFollowRedirect(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/campus" || r.URL.RawQuery != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("unsafe request: %s %v", r.URL, r.Header)
		}
		w.Header().Set("Set-Cookie", "private=value")
		w.Header().Set("Location", "/other?token=secret")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	result, err := ProbeCampus(context.Background(), server.URL+"/campus?token=private#secret", Options{Lookup: localLookup})
	if err != nil || result.HTTPStatus != 302 || hits != 1 {
		t.Fatalf("redirect/query handling: %+v / %v / %d requests", result, err, hits)
	}
}
