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

type RulesImport struct {
	Name     string         `json:"name"`
	Document rules.Document `json:"document"`
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

// Import returns a preview only; Save is still the only operation that applies rules.
func (s *RulesService) Import() (*RulesImport, error) {
	path, err := s.b.App.Dialog.OpenFile().SetTitle("导入校内资源").AddFilter("校内资源 JSON", "*.json").PromptForSingleSelection()
	if err != nil {
		return nil, err
	}
	return s.importFile(path)
}

func (s *RulesService) importFile(path string) (*RulesImport, error) {
	if path == "" {
		return nil, nil
	}
	document, err := rules.ImportFile(path)
	if err != nil {
		return nil, err
	}
	return &RulesImport{Name: filepath.Base(path), Document: document}, nil
}

// Export writes the current frontend draft, including changes not yet saved.
func (s *RulesService) Export(document rules.Document) (string, error) {
	if _, err := rules.Normalize(document); err != nil {
		return "", err
	}
	path, err := s.b.App.Dialog.SaveFile().SetFilename("campus-resources.json").AddFilter("校内资源 JSON", "*.json").PromptForSingleSelection()
	if err != nil {
		return "", err
	}
	return s.exportFile(path, document)
}

func (s *RulesService) exportFile(path string, document rules.Document) (string, error) {
	if path == "" {
		return "", nil
	}
	files, directories := s.b.exportPaths()
	if err := rules.ExportFile(path, document, files, directories...); err != nil {
		return "", err
	}
	return path, nil
}

func (b *Backend) exportPaths() ([]string, []string) {
	b.mu.Lock()
	paths := []string{b.executable, b.config.ConfigPath, b.config.FduConfig, b.config.Integration.RuntimeFile}
	directories := []string{b.config.ClashDir}
	// A portable install may live directly in Downloads. Protect our own
	// files and recovery tree without claiming the entire parent directory.
	for _, dir := range []string{b.Dir, b.exeDir} {
		if dir == "" {
			continue
		}
		for _, name := range []string{
			"FDU-Connect-GUI.exe", "FDU-Connect-GUI", "fdu-connect.exe", "fdu-connect", "LICENSE", "config.toml.example",
			"config.toml", "client.toml", "gui-settings.json", "client_data.json", "routing-rules.json", "routing-rules.json.status.json",
			"campus-session.json", "campus-feed.json", "fdu-campus-runtime.yaml",
		} {
			paths = append(paths, filepath.Join(dir, name))
		}
		directories = append(directories, filepath.Join(dir, "connection-backups"))
	}
	if b.runtime != nil {
		paths = append(paths, b.runtime.Core)
	}
	if b.workspace != nil {
		paths = append(paths, b.workspace.Target)
	}
	addRules := func(path string) {
		if path != "" {
			paths = append(paths, path, path+".status.json")
		}
	}
	addRules(b.rulesPath)
	addValues := func(values map[string]any, config string) {
		for _, key := range []string{"client_data_file", "cert_file", "resource_file", "graph_code_file", "debug_pcap_file", "debug_tls_log_file", "routing.rules_file"} {
			if file := text(values, key); file != "" {
				if !filepath.IsAbs(file) {
					file = filepath.Join(filepath.Dir(config), file)
				}
				paths = append(paths, file)
				if key == "routing.rules_file" {
					addRules(file)
				}
			}
		}
	}
	config := filepath.Join(b.Dir, "client.toml")
	if b.view != nil && b.view.Path != "" {
		config = b.view.Path
	}
	addValues(b.saved, config)
	if b.view != nil {
		paths = append(paths, b.view.Path, b.view.Source)
		addValues(b.view.Values, b.view.Path)
	}
	if b.plan != nil {
		paths = append(paths, b.plan.Config, b.plan.ProviderPath, b.plan.RuntimeFile)
		addRules(b.plan.Rules)
		for file := range b.plan.Inputs {
			paths = append(paths, file)
		}
		for _, file := range b.plan.Files {
			paths = append(paths, file.Path)
		}
	}
	if b.campus != nil {
		paths = append(paths, b.campus.Config, b.campus.ProviderPath, b.campus.RuntimeFile)
		addRules(b.campus.Rules)
		directories = append(directories, b.campus.Root)
	}
	if b.conn != nil {
		paths = append(paths, b.conn.RuntimePath())
	}
	updater := b.updater
	b.mu.Unlock()
	if updater != nil {
		updater.mu.Lock()
		if updater.pending != nil {
			directories = append(directories, updater.pending.Root)
		}
		updater.mu.Unlock()
	}
	for _, path := range paths {
		if path != "" {
			paths = append(paths, path+".bak")
		}
	}
	return paths, directories
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
	if s.b.proxyOnly() {
		return rules.ApplyState{Message: "仅本地代理：校园分流由外部客户端管理，当前列表保留供自动／手动接入使用"}
	}
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
