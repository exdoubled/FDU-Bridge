package clash

import (
	"os"
	"path/filepath"
	"testing"
)

func TestICourseCompatibilityBypassIsNotRemovalAdvice(t *testing.T) {
	file := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(file, []byte(`{"version":1,"domains":["icourse.fudan.edu.cn"],"domain_suffixes":["fudan.edu.cn"],"ip_cidrs":["10.0.0.0/8"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := bypassConflicts([]string{"icourse.fudan.edu.cn", "*.icourse.fudan.edu.cn", "10.*", "*.fudan.edu.cn"}, file)
	if err != nil || len(got) != 2 || got[0] != "10.*" || got[1] != "*.fudan.edu.cn" {
		t.Fatalf("compatibility entry must be kept without hiding broader conflicts: %v / %v", got, err)
	}
}
