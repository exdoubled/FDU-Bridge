package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ExportPath rejects managed files and directory trees, including directory aliases
// and files not yet created. Unreadable unrelated managed paths do not block export.
func ExportPath(path string, files, directories []string) (string, error) {
	target, err := exportLocation(path)
	if err != nil {
		return "", err
	}
	blocked := errors.New("不能覆盖程序或代理客户端管理的文件，请另选导出位置")
	for _, file := range files {
		if file == "" {
			continue
		}
		managed, _ := exportLocation(file)
		if sameExportLocation(target, managed) {
			return "", blocked
		}
	}
	for _, directory := range directories {
		if directory == "" {
			continue
		}
		managed, _ := exportLocation(directory)
		for current := target.path; ; current = filepath.Dir(current) {
			ancestor, _ := exportLocation(current)
			if sameExportLocation(ancestor, managed) {
				return "", blocked
			}
			if current == filepath.Dir(current) {
				break
			}
		}
	}
	return target.path, nil
}

type exportFileLocation struct {
	path string
	info os.FileInfo
	tail string
}

func exportLocation(path string) (exportFileLocation, error) {
	abs, err := filepath.Abs(path)
	location := exportFileLocation{path: abs}
	if err != nil {
		return location, err
	}
	current := abs
	for {
		info, err := os.Stat(current)
		if err == nil {
			location.info = info
			return location, nil
		}
		if !os.IsNotExist(err) || current == filepath.Dir(current) {
			return location, err
		}
		location.tail = filepath.Join(filepath.Base(current), location.tail)
		current = filepath.Dir(current)
	}
}

func sameExportLocation(a, b exportFileLocation) bool {
	equal := func(a, b string) bool {
		if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
			return strings.EqualFold(a, b)
		}
		return a == b
	}
	return a.path != "" && b.path != "" && (equal(a.path, b.path) ||
		(a.info != nil && b.info != nil && os.SameFile(a.info, b.info) && equal(a.tail, b.tail)))
}
