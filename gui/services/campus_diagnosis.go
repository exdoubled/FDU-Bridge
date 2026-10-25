package services

// CampusDiagnosis separates the configured route from evidence collected by a probe.
type CampusDiagnosis struct {
	CheckedAt    int64               `json:"checkedAt"`
	Target       string              `json:"target"`
	Host         string              `json:"host"`
	Integration  string              `json:"integration"`
	State        string              `json:"state"`
	Connected    bool                `json:"connected"`
	AuthRequired bool                `json:"authRequired"`
	Coverage     string              `json:"coverage"`
	Rule         CampusRuleDiagnosis `json:"rule"`
	ExpectedPath string              `json:"expectedPath"`
	Probe        *ProbeReport        `json:"probe"`
	Problem      string              `json:"problem"`
	Suggestions  []string            `json:"suggestions"`
}

type CampusRuleDiagnosis struct {
	Known   bool   `json:"known"`
	Matched bool   `json:"matched"`
	Kind    string `json:"kind"`
	Value   string `json:"value"`
	Enabled bool   `json:"enabled"`
	Applied bool   `json:"applied"`
	Message string `json:"message"`
}

func cloneDiagnosis(value CampusDiagnosis) CampusDiagnosis {
	value.Suggestions = append([]string(nil), value.Suggestions...)
	if value.Probe != nil {
		probe := *value.Probe
		probe.Result.Answers = append([]string(nil), probe.Result.Answers...)
		probe.Result.Checks = append(probe.Result.Checks[:0:0], probe.Result.Checks...)
		value.Probe = &probe
	}
	return value
}

func (s *NetworkService) saveDiagnosis(value CampusDiagnosis) {
	s.diagnosisMu.Lock()
	defer s.diagnosisMu.Unlock()
	copy := cloneDiagnosis(value)
	s.diagnosis = &copy
}

func (s *NetworkService) latestDiagnosis() (CampusDiagnosis, bool) {
	s.diagnosisMu.Lock()
	defer s.diagnosisMu.Unlock()
	if s.diagnosis == nil {
		return CampusDiagnosis{}, false
	}
	return cloneDiagnosis(*s.diagnosis), true
}
