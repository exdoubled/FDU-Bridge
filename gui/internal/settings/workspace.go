// Package settings edits the GUI-owned client configuration without touching imported sources.
package settings

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mythologyli/zju-connect/gui/internal/clashsetup"
	"github.com/mythologyli/zju-connect/gui/internal/fsx"
)

type Field = clashsetup.SettingField

type View struct {
	Path    string         `json:"path"`
	Source  string         `json:"source"`
	Fields  []Field        `json:"fields"`
	Values  map[string]any `json:"values"`
	Dirty   bool           `json:"dirty"`
	Saved   bool           `json:"saved"`
	Example bool           `json:"example"`
}

type Workspace struct {
	Dir        string
	Target     string
	targetHash string
	snapshot   *clashsetup.SettingsReply
}

func New(directory string) (*Workspace, error) {
	dir, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	return &Workspace{Dir: dir, Target: filepath.Join(dir, "client.toml")}, nil
}

// Private reports whether path lives inside the GUI directory, the only place Save may write.
func (w *Workspace) Private(path string) bool {
	rel, err := filepath.Rel(w.Dir, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

func (w *Workspace) Loaded() bool { return w.snapshot != nil }

// Load reads source as a draft. managed makes a GUI-owned source the new save target.
func (w *Workspace) Load(source string, managed bool) (View, error) {
	source, err := filepath.Abs(source)
	if err != nil {
		return View{}, err
	}
	loaded, err := clashsetup.ReadSettings(source, w.Dir)
	if err != nil {
		return View{}, fmt.Errorf("无法读取配置：%w", err)
	}
	if managed && w.Private(source) {
		w.Target = source
	}
	w.targetHash = fsx.HashFile(w.Target)
	w.snapshot = &loaded
	_, statErr := os.Stat(w.Target)
	return View{Path: w.Target, Source: source, Fields: loaded.Fields, Values: loaded.Values,
		Dirty: loaded.Adjusted || !strings.EqualFold(source, w.Target), Saved: statErr == nil}, nil
}

type write struct {
	path     string
	data     []byte // nil removes the file
	expected string
}

// Save renders values, then writes the login-state/rules copies and the config as one unit.
func (w *Workspace) Save(values map[string]any) (View, error) {
	if w.snapshot == nil {
		return View{}, errors.New("请先读取配置")
	}
	if !w.Private(w.Target) {
		return View{}, errors.New("只允许保存到本程序自己的目录")
	}
	if fsx.HashFile(w.Target) != w.targetHash {
		return View{}, errors.New("配置已被其他程序修改，请重新读取")
	}
	rendered, err := clashsetup.RenderSettings(values)
	if err != nil {
		return View{}, err
	}
	var writes []write
	for _, copy := range w.snapshot.Copies {
		if !w.Private(copy.Destination) {
			return View{}, errors.New("副本路径超出本程序目录")
		}
		if fsx.HashFile(copy.Destination) != copy.DestinationHash {
			return View{}, errors.New("目标状态或规则已变化，请重新导入")
		}
		var data []byte
		if copy.Remove {
			if _, err := os.Stat(copy.Source); err == nil {
				return View{}, errors.New("导入的状态或规则已变化，请重新导入")
			}
		} else {
			if data, err = os.ReadFile(copy.Source); err != nil || fsx.Hash(data) != copy.Hash {
				return View{}, errors.New("导入的状态或规则已变化，请重新导入")
			}
		}
		writes = append(writes, write{copy.Destination, data, copy.DestinationHash})
	}
	writes = append(writes, write{w.Target, []byte(rendered.Content), w.targetHash})
	originals := map[string][]byte{}
	var applied []write
	for _, item := range writes {
		err = func() error {
			if fsx.HashFile(item.path) != item.expected {
				return errors.New("配置副本在保存前发生变化")
			}
			if old, readErr := os.ReadFile(item.path); readErr == nil {
				originals[item.path] = old
				if err := os.WriteFile(item.path+".bak", old, 0o600); err != nil {
					return err
				}
			}
			if item.data == nil {
				if err := os.Remove(item.path); err != nil && !os.IsNotExist(err) {
					return err
				}
			} else if err := fsx.AtomicWrite(item.path, item.data); err != nil {
				return err
			}
			applied = append(applied, item)
			return nil
		}()
		if err != nil {
			break
		}
	}
	if err != nil {
		var failures []string
		for _, item := range slices.Backward(applied) {
			want := ""
			if item.data != nil {
				want = fsx.Hash(item.data)
			}
			var restore error
			if fsx.HashFile(item.path) != want {
				restore = errors.New("副本被其他程序修改，未覆盖")
			} else if old, ok := originals[item.path]; !ok {
				restore = os.Remove(item.path)
			} else {
				restore = fsx.AtomicWrite(item.path, old)
			}
			if restore != nil && !os.IsNotExist(restore) {
				failures = append(failures, filepath.Base(item.path)+": "+restore.Error())
			}
		}
		if len(failures) == 0 {
			return View{}, fmt.Errorf("%v；已恢复原副本", err)
		}
		return View{}, fmt.Errorf("%v\n%s", err, strings.Join(failures, "\n"))
	}
	w.targetHash = fsx.HashFile(w.Target)
	rendered.Copies = nil
	w.snapshot = &rendered
	return View{Path: w.Target, Source: w.Target, Fields: rendered.Fields, Values: rendered.Values, Saved: true}, nil
}
