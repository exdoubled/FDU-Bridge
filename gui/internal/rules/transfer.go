package rules

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mythologyli/zju-connect/gui/internal/fsx"
	"github.com/mythologyli/zju-connect/routing"
)

// ImportFile validates a standalone rules document without changing any file.
func ImportFile(path string) (Document, error) {
	b, err := fsx.ReadBounded(path, limit)
	if err != nil {
		return Document{}, fmt.Errorf("无法导入校内资源：%w", err)
	}
	parsed, err := routing.ParseRules(b)
	if err != nil {
		return Document{}, fmt.Errorf("校内资源 JSON 无效：%w", err)
	}
	return fromRuleSet(parsed), nil
}

// ExportFile includes only rules and switches, never settings or login state.
func ExportFile(path string, d Document, protected []string) error {
	normalized, err := Normalize(d)
	if err != nil {
		return err
	}
	b, err := encode(normalized)
	if err != nil {
		return err
	}
	if len(b) > limit {
		return errors.New("规则文件不能超过 256 KiB")
	}
	target, err := transferPath(path)
	if err != nil {
		return err
	}
	targetInfo, _ := os.Stat(path)
	for _, existing := range protected {
		if existing == "" {
			continue
		}
		managed, err := transferPath(existing)
		if err != nil {
			return err
		}
		info, _ := os.Stat(existing)
		if strings.EqualFold(target, managed) || (targetInfo != nil && info != nil && os.SameFile(targetInfo, info)) {
			return errors.New("不能覆盖正在使用的规则、配置或状态文件，请另选导出位置")
		}
	}
	return fsx.AtomicWrite(path, b)
}

// Resolve existing ancestors as well, so directory links cannot bypass protection
// for a managed state file that has not been created yet.
func transferPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current, tail := abs, ""
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			return filepath.Join(resolved, tail), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return abs, nil
		}
		tail = filepath.Join(filepath.Base(current), tail)
		current = parent
	}
}
