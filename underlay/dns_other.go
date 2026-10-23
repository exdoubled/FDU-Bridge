//go:build !windows

package underlay

func systemInterfaceDNSServer(string) string { return "" }
