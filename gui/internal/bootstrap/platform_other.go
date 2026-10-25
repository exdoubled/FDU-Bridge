//go:build !windows

package bootstrap

func Prepare() (*Startup, error) { return &Startup{}, nil }
func LogPath() (string, error)   { return "", nil }
