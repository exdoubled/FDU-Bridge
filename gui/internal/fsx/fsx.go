// Package fsx holds the hash-checked file primitives shared by every GUI writer.
package fsx

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
)

func Hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// HashFile returns "" for a missing file so callers can compare against "not yet created".
func HashFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return Hash(b)
}

func ReadBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(b)) > limit {
		err = errors.New("文件超过大小限制")
	}
	return b, err
}

func tempName(path string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return path + ".fdu-" + hex.EncodeToString(b[:]) + ".tmp"
}

// AtomicWrite finishes the slow disk write before swapping the destination in.
func AtomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary := tempName(path)
	defer os.Remove(temporary)
	f, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
