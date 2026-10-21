package routing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxRulesFileSize = 256 * 1024

type RuleSet struct {
	Version        int            `json:"version"`
	Domains        []string       `json:"domains"`
	DomainSuffixes []string       `json:"domain_suffixes"`
	IPCIDRs        []string       `json:"ip_cidrs"`
	Disabled       *DisabledRules `json:"disabled,omitempty"`
}

type DisabledRules struct {
	Domains        []string `json:"domains"`
	DomainSuffixes []string `json:"domain_suffixes"`
	IPCIDRs        []string `json:"ip_cidrs"`
}

// Canonical returns a new document without retaining caller-owned slices.
func (rules RuleSet) Canonical() (RuleSet, error) {
	result, _, err := rules.canonicalPolicy()
	return result, err
}

// Build the immutable policy while validating the document, so the watcher
// does not normalize and parse each rule again before publishing it.
func (rules RuleSet) canonicalPolicy() (RuleSet, policy, error) {
	result := RuleSet{Version: rules.Version, Domains: []string{}, DomainSuffixes: []string{}, IPCIDRs: []string{}}
	p := policy{domains: make(map[string]bool)}
	if (rules.Version != 1 && rules.Version != 2) || rules.Domains == nil || rules.DomainSuffixes == nil || rules.IPCIDRs == nil {
		return result, p, errors.New("rules require version=1 or 2 and all three arrays: domains, domain_suffixes, ip_cidrs")
	}
	if (rules.Version == 1 && rules.Disabled != nil) || (rules.Version == 2 && rules.Disabled == nil) {
		return result, p, errors.New("per-entry switches require version=2 with a complete disabled object")
	}
	disabledLists := [][]string{nil, nil, nil}
	if rules.Disabled != nil {
		disabled, err := (RuleSet{Version: 1, Domains: rules.Disabled.Domains, DomainSuffixes: rules.Disabled.DomainSuffixes, IPCIDRs: rules.Disabled.IPCIDRs}).Canonical()
		if err != nil {
			return result, p, err
		}
		result.Disabled = &DisabledRules{Domains: disabled.Domains, DomainSuffixes: disabled.DomainSuffixes, IPCIDRs: disabled.IPCIDRs}
		disabledLists = [][]string{disabled.Domains, disabled.DomainSuffixes, disabled.IPCIDRs}
	}
	for index, input := range [][]string{rules.Domains, rules.DomainSuffixes, rules.IPCIDRs} {
		seen := make(map[string]bool)
		blocked := make(map[string]bool)
		for _, value := range disabledLists[index] {
			blocked[value] = true
		}
		for _, value := range input {
			var prefix netip.Prefix
			value = strings.TrimSpace(value)
			if index < 2 {
				value = normalizeDomain(value)
				if !validDomain(value) {
					return result, p, fmt.Errorf("invalid rule domain %q: use an ASCII hostname without URL or wildcard", value)
				}
			} else {
				if !strings.Contains(value, "/") {
					value += "/32"
				}
				var err error
				prefix, err = netip.ParsePrefix(value)
				if err != nil || !prefix.Addr().Is4() {
					return result, p, fmt.Errorf("invalid IPv4 rule %q", value)
				}
				prefix = prefix.Masked()
				value = prefix.String()
			}
			if seen[value] {
				continue
			}
			seen[value] = true
			switch index {
			case 0:
				result.Domains = append(result.Domains, value)
				if !blocked[value] {
					p.domains[value] = true
				}
			case 1:
				result.DomainSuffixes = append(result.DomainSuffixes, value)
				if !blocked[value] {
					p.suffixes = append(p.suffixes, value)
				}
			case 2:
				result.IPCIDRs = append(result.IPCIDRs, value)
				if !blocked[value] {
					p.prefixes = append(p.prefixes, prefix)
				}
			}
		}
		for value := range blocked {
			if !seen[value] {
				return result, p, fmt.Errorf("disabled rule %q does not exist in its rule type", value)
			}
		}
	}
	return result, p, nil
}

func ParseRules(data []byte) (RuleSet, error) {
	rules, err := decodeRules(data)
	if err != nil {
		return rules, err
	}
	return rules.Canonical()
}

func decodeRules(data []byte) (RuleSet, error) {
	if len(data) > maxRulesFileSize {
		return RuleSet{}, errors.New("rules file exceeds 256 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var rules RuleSet
	if err := decoder.Decode(&rules); err != nil {
		return rules, fmt.Errorf("read rules JSON: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return rules, errors.New("rules file must contain exactly one JSON document")
	}
	return rules, nil
}

func (r *Router) ReplaceRules(rules RuleSet) error {
	_, p, err := rules.canonicalPolicy()
	if err != nil {
		return err
	}
	r.policy.Store(&p)
	return nil
}

type RuleFileStatus struct {
	Version      int       `json:"version"`
	PID          int       `json:"pid"`
	Running      bool      `json:"running"`
	UpdatedAt    time.Time `json:"updated_at"`
	LoadedSHA256 string    `json:"loaded_sha256"`
	Error        string    `json:"error"`
}

type ruleWatcher struct {
	cancel context.CancelFunc
	done   chan error
	once   sync.Once
	err    error
}

func (w *ruleWatcher) Close() error {
	w.once.Do(func() { w.cancel(); w.err = <-w.done })
	return w.err
}

func readRulesFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxRulesFileSize+1))
	if err == nil && len(data) > maxRulesFileSize {
		err = errors.New("rules file exceeds 256 KiB")
	}
	return data, err
}

func writeRuleStatus(path string, status RuleFileStatus) error {
	data, err := json.Marshal(status)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".fdu-rules-status-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, path+".status.json")
}

// WatchRulesFile validates before listeners start. Later errors retain last-good
// policy; acknowledgements refer to raw file bytes, not re-serialized JSON.
func WatchRulesFile(ctx context.Context, path string, router *Router) (io.Closer, error) {
	return watchRulesFile(ctx, path, router, time.Second)
}

func watchRulesFile(parent context.Context, path string, router *Router, interval time.Duration) (io.Closer, error) {
	data, err := readRulesFile(path)
	if err != nil {
		return nil, err
	}
	rules, err := decodeRules(data)
	if err != nil {
		return nil, err
	}
	if err = router.ReplaceRules(rules); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	status := RuleFileStatus{Version: 1, PID: os.Getpid(), Running: true, UpdatedAt: time.Now().UTC(), LoadedSHA256: hex.EncodeToString(digest[:])}
	if err = writeRuleStatus(path, status); err != nil {
		return nil, fmt.Errorf("write rule acknowledgement: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	w := &ruleWatcher{cancel: cancel, done: make(chan error, 1)}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				status.Running = false
				status.UpdatedAt = time.Now().UTC()
				w.done <- writeRuleStatus(path, status)
				return
			case <-ticker.C:
				previous := status
				data, readErr := readRulesFile(path)
				if readErr == nil {
					digest = sha256.Sum256(data)
					hash := hex.EncodeToString(digest[:])
					if hash != status.LoadedSHA256 {
						rules, readErr = decodeRules(data)
						if readErr == nil {
							readErr = router.ReplaceRules(rules)
						}
						if readErr == nil {
							status.LoadedSHA256 = hash
						}
					}
				}
				status.Error = ""
				if readErr != nil {
					status.Error = readErr.Error()
				}
				if status.Error != previous.Error || status.LoadedSHA256 != previous.LoadedSHA256 || time.Since(status.UpdatedAt) >= 5*time.Second {
					status.UpdatedAt = time.Now().UTC()
					if writeRuleStatus(path, status) != nil {
						status.UpdatedAt = previous.UpdatedAt
					}
				}
			}
		}
	}()
	return w, nil
}
