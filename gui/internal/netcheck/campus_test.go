package netcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalizeCampusURLRemovesCredentialsAndPreservesPath(t *testing.T) {
	for _, test := range []struct{ input, want, host string }{
		{" Library.Fudan.edu.cn ", "https://library.fudan.edu.cn/", "library.fudan.edu.cn"},
		{"https://LIBRARY.fudan.edu.cn:443/a%20b?token=secret#private", "https://library.fudan.edu.cn:443/a%20b", "library.fudan.edu.cn"},
		{"http://10.20.1.2/path", "http://10.20.1.2/path", "10.20.1.2"},
	} {
		got, err := NormalizeCampusURL(test.input)
		if err != nil || got.URL != test.want || got.Host != test.host {
			t.Fatalf("%q: %+v / %v", test.input, got, err)
		}
	}
	for _, input := range []string{"", "ftp://example.edu/", "https://user:pass@example.edu/", "https://user@example.edu/", "//example.edu", "https://example.edu:0", "https://example.edu:65536", "https://bad..edu/", "https://example.edu\\evil", "javascript:alert(1)", "example.edu/path"} {
		if _, err := NormalizeCampusURL(input); err == nil {
			t.Fatalf("accepted unsafe input %q", input)
		}
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
