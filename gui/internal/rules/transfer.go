package rules

import (
	"fmt"

	"github.com/mythologyli/zju-connect/gui/internal/fsx"
)

// ImportFile validates a standalone rules document without changing any file.
func ImportFile(path string) (Document, error) {
	b, err := fsx.ReadBounded(path, limit)
	if err != nil {
		return Document{}, fmt.Errorf("无法导入校内资源：%w", err)
	}
	parsed, err := parse(b)
	if err != nil {
		return Document{}, fmt.Errorf("校内资源 JSON 无效：%w", err)
	}
	return parsed, nil
}

// ExportFile includes only rules and switches, never settings or login state.
func ExportFile(path string, d Document, protected []string, directories ...string) error {
	b, _, err := marshal(d)
	if err != nil {
		return err
	}
	target, err := fsx.ExportPath(path, protected, directories)
	if err != nil {
		return err
	}
	return fsx.AtomicWrite(target, b)
}
