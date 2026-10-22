package services

import (
	"encoding/json"
	"path/filepath"

	"github.com/mythologyli/zju-connect/gui/internal/rules"
)

type RulesView struct {
	Path     string           `json:"path"`
	Snapshot rules.Snapshot   `json:"snapshot"`
	Status   rules.ApplyState `json:"status"`
}

// RulesService edits the rules file the running core hot-reloads.
type RulesService struct{ b *Backend }

func NewRulesService(b *Backend) *RulesService { return &RulesService{b} }

func (s *RulesService) store() (*rules.Store, error) {
	s.b.mu.Lock()
	path := s.b.rulesPath
	s.b.mu.Unlock()
	if path == "" {
		path = filepath.Join(s.b.Dir, "routing-rules.json")
	}
	return rules.New(path)
}

func (s *RulesService) Load() (RulesView, error) {
	store, err := s.store()
	if err != nil {
		return RulesView{}, err
	}
	snapshot, err := store.Load()
	if err != nil {
		return RulesView{Path: store.Path}, err
	}
	if snapshot.Missing {
		if view := s.b.currentView(); view != nil {
			raw, err := json.Marshal(view.Values["routing"])
			if err != nil {
				return RulesView{}, err
			}
			document := rules.Empty()
			if err = json.Unmarshal(raw, &document); err != nil {
				return RulesView{}, err
			}
			if snapshot.Document, err = rules.Normalize(document); err != nil {
				return RulesView{}, err
			}
		}
	}
	return RulesView{store.Path, snapshot, s.applyStatus(store, snapshot.Hash)}, nil
}

// Save writes only if the file still matches expectedHash ("" for a file not yet created).
func (s *RulesService) Save(document rules.Document, expectedHash string) (RulesView, error) {
	store, err := s.store()
	if err != nil {
		return RulesView{}, err
	}
	snapshot, err := store.Save(document, expectedHash)
	if err != nil {
		return RulesView{}, err
	}
	s.b.emit(EventChanged, "rules")
	return RulesView{store.Path, snapshot, s.applyStatus(store, snapshot.Hash)}, nil
}

// Validate returns the canonical spelling of one new entry.
func (s *RulesService) Validate(kind rules.Kind, value string) (string, error) {
	return rules.Entry(kind, value)
}

func (s *RulesService) Status(hash string) rules.ApplyState {
	store, err := s.store()
	if err != nil {
		return rules.ApplyState{Message: err.Error()}
	}
	return s.applyStatus(store, hash)
}

func (s *RulesService) applyStatus(store *rules.Store, hash string) rules.ApplyState {
	s.b.mu.Lock()
	enabled := s.b.enabled
	s.b.mu.Unlock()
	if !enabled {
		return rules.ApplyState{Message: "已保存，下次启用校内访问时生效"}
	}
	state := store.ApplyStatus(hash)
	if !state.Applied {
		return state
	}
	a := s.b.campusAttachment()
	if a == nil || a.RulesHash != hash {
		return rules.ApplyState{Message: "FDU 已加载，等待 Clash 同步校内资源…"}
	}
	return rules.ApplyState{Applied: true, Message: "FDU 和 Clash 已同步，新连接按此分流"}
}
