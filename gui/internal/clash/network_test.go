package clash

import "testing"

func TestDiagnosticReadAllowlist(t *testing.T) {
	for path, want := range map[string]bool{
		"http://other.example/dns/query?name=api.openai.com&type=A": false,
		"/connections":                                       false,
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
