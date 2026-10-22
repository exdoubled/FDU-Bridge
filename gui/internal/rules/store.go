// Package rules edits the selective-routing rules file the running core watches.
package rules

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mythologyli/zju-connect/gui/internal/fsx"
	"github.com/mythologyli/zju-connect/gui/internal/sysproc"
	"github.com/mythologyli/zju-connect/routing"
)

const limit = 256 * 1024

// Kind indexes the three rule lists in the order the file stores them.
type Kind string

const (
	Domain Kind = "domain"
	Suffix Kind = "suffix"
	CIDR   Kind = "cidr"
)

type Document struct {
	Domains        []string `json:"domains"`
	DomainSuffixes []string `json:"domain_suffixes"`
	IPCIDRs        []string `json:"ip_cidrs"`
	Disabled       Disabled `json:"disabled"`
}

type Disabled struct {
	Domains        []string `json:"domains"`
	DomainSuffixes []string `json:"domain_suffixes"`
	IPCIDRs        []string `json:"ip_cidrs"`
}

type Snapshot struct {
	Document Document `json:"document"`
	Hash     string   `json:"hash"`
	Missing  bool     `json:"missing"`
}

type ApplyState struct {
	Applied bool   `json:"applied"`
	Message string `json:"message"`
}

type Store struct{ Path string }

func New(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	return &Store{abs}, err
}

func Empty() Document {
	return Document{[]string{}, []string{}, []string{}, Disabled{[]string{}, []string{}, []string{}}}
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func fromRuleSet(rules routing.RuleSet) Document {
	d := Document{nonNil(rules.Domains), nonNil(rules.DomainSuffixes), nonNil(rules.IPCIDRs), Empty().Disabled}
	if rules.Disabled != nil {
		d.Disabled = Disabled{nonNil(rules.Disabled.Domains), nonNil(rules.Disabled.DomainSuffixes), nonNil(rules.Disabled.IPCIDRs)}
	}
	return d
}

// Normalize applies the core's own canonicalization so the GUI never saves what the core rejects.
func Normalize(d Document) (Document, error) {
	set := routing.RuleSet{Version: 2, Domains: nonNil(d.Domains), DomainSuffixes: nonNil(d.DomainSuffixes), IPCIDRs: nonNil(d.IPCIDRs),
		Disabled: &routing.DisabledRules{Domains: nonNil(d.Disabled.Domains), DomainSuffixes: nonNil(d.Disabled.DomainSuffixes), IPCIDRs: nonNil(d.Disabled.IPCIDRs)}}
	canonical, err := set.Canonical()
	if err != nil {
		if strings.Contains(err.Error(), "disabled rule") {
			return d, errors.New("未启用的条目必须存在于同类型的完整规则列表中")
		}
		return d, fmt.Errorf("规则无效：%w", err)
	}
	return fromRuleSet(canonical), nil
}

// Entry validates a single new rule and returns its canonical spelling.
func Entry(kind Kind, input string) (string, error) {
	value := strings.TrimSpace(input)
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return "", errors.New("请输入有效的 HTTP/HTTPS 网址、域名或 IPv4")
		}
		value = u.Hostname()
	}
	switch kind {
	case Domain, Suffix:
		value = strings.ToLower(strings.TrimSuffix(value, "."))
		if _, err := netip.ParseAddr(value); err == nil || value == "" {
			return "", errors.New("请输入有效域名，不能把 IP 放在域名类型中")
		}
		set := routing.RuleSet{Version: 1, Domains: []string{value}, DomainSuffixes: []string{}, IPCIDRs: []string{}}
		if _, err := set.Canonical(); err != nil {
			return "", errors.New("域名需为 ASCII/Punycode，不带协议、路径、端口或 *；含子域名请选“域名及子域名”")
		}
		return value, nil
	case CIDR:
		if !strings.Contains(value, "/") {
			value += "/32"
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil || !prefix.Addr().Is4() {
			return "", errors.New("请输入 IPv4 或 IPv4 网段，例如 10.20.26.253 或 10.20.0.0/16")
		}
		return prefix.Masked().String(), nil
	}
	return "", errors.New("未知的规则类型")
}

func (s *Store) Load() (Snapshot, error) {
	b, err := fsx.ReadBounded(s.Path, limit)
	if os.IsNotExist(err) {
		return Snapshot{Document: Empty(), Missing: true}, nil
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("无法读取规则：%w", err)
	}
	parsed, err := routing.ParseRules(b)
	if err != nil {
		return Snapshot{}, fmt.Errorf("规则文件格式无效：%w", err)
	}
	return Snapshot{Document: fromRuleSet(parsed), Hash: fsx.Hash(b)}, nil
}

func encode(d Document) ([]byte, error) {
	return json.Marshal(struct {
		Version int `json:"version"`
		Document
	}{2, d})
}

// Save replaces the file only if it still has expectedHash ("" means it must not exist).
func (s *Store) Save(d Document, expectedHash string) (Snapshot, error) {
	normalized, err := Normalize(d)
	if err != nil {
		return Snapshot{}, err
	}
	b, err := encode(normalized)
	if err != nil {
		return Snapshot{}, err
	}
	if len(b) > limit {
		return Snapshot{}, errors.New("规则文件不能超过 256 KiB")
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return Snapshot{}, err
	}
	// Match the old manager's mutex so both never interleave a compare-and-replace.
	name := `Local\FduRulesSave-` + fsx.Hash([]byte(strings.ToUpper(s.Path)))
	err = sysproc.WithMutex(name, 2*time.Second, func() error {
		actual := ""
		if current, readErr := fsx.ReadBounded(s.Path, limit); readErr == nil {
			actual = fsx.Hash(current)
		} else if !os.IsNotExist(readErr) {
			return readErr
		}
		if actual != expectedHash {
			return errors.New("规则文件已在其他位置修改或删除；请先重新读取，避免覆盖其他修改")
		}
		return fsx.AtomicWrite(s.Path, b)
	})
	if errors.Is(err, sysproc.ErrMutexBusy) {
		err = errors.New("另一个窗口正在保存规则，请稍后重试")
	}
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Document: normalized, Hash: fsx.Hash(b)}, nil
}

type status struct {
	Version      int    `json:"version"`
	PID          uint32 `json:"pid"`
	Running      bool   `json:"running"`
	UpdatedAt    string `json:"updated_at"`
	LoadedSHA256 string `json:"loaded_sha256"`
	Error        string `json:"error"`
}

// Acknowledgement reads the core's status sidecar; ok is false when it is absent or stale.
func (s *Store) Acknowledgement() (pid uint32, loaded string, failure string, ok bool) {
	b, err := fsx.ReadBounded(s.Path+".status.json", 16384)
	if err != nil {
		return 0, "", "", false
	}
	var st status
	if json.Unmarshal(b, &st) != nil || st.Version != 1 || !st.Running {
		return 0, "", "", false
	}
	updated, err := time.Parse(time.RFC3339Nano, st.UpdatedAt)
	if err != nil {
		return 0, "", "", false
	}
	if age := time.Since(updated); age < -5*time.Second || age > 15*time.Second {
		return 0, "", "", false
	}
	started, err := sysproc.StartTime(st.PID)
	if err != nil || started.After(updated) {
		return 0, "", "", false
	}
	return st.PID, st.LoadedSHA256, st.Error, true
}

func (s *Store) ApplyStatus(expectedHash string) ApplyState {
	if expectedHash == "" {
		return ApplyState{Message: "规则尚未保存"}
	}
	_, loaded, failure, ok := s.Acknowledgement()
	switch {
	case !ok:
		return ApplyState{Message: "已保存，尚未收到运行中 FDU 的加载确认"}
	case failure != "":
		return ApplyState{Message: "FDU 未接受新规则，仍保留上一份有效规则：" + failure}
	case !strings.EqualFold(loaded, expectedHash):
		return ApplyState{Message: "已保存，等待 FDU 加载新规则…"}
	}
	return ApplyState{Applied: true, Message: "FDU 已加载当前规则，新连接按此分流"}
}
