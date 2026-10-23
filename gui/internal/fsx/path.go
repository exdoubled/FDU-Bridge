package fsx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ErrOutsideRoot = errors.New("配置路径超出受管目录")

// NoLinksBelow allows an explicitly selected root (and its ancestors) to be an
// OS alias, but rejects links, including Windows junctions, inside that root.
// Missing descendants are allowed so previews can validate future outputs.
func NoLinksBelow(root, path string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return ErrOutsideRoot
	}
	if rel == "." {
		return nil
	}
	current := root
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		// Readlink also recognizes Windows mount-point reparse tags, whose
		// FileMode does not necessarily include ModeSymlink.
		_, linkErr := os.Readlink(current)
		if info.Mode()&os.ModeSymlink != 0 || linkErr == nil {
			return fmt.Errorf("配置路径包含链接：%s", current)
		}
	}
	return nil
}
