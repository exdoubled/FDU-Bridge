package clash

import (
	"net/url"
)

// Diagnostics add only bounded, read-only controller requests.
func diagnosticRead(path string) bool {
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
		(host == "library.fudan.edu.cn" || host == "icourse.fudan.edu.cn")
}
