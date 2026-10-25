package services

import (
	"github.com/mythologyli/zju-connect/gui/internal/netcheck"
	"testing"
)

func TestLatestDiagnosisDoesNotShareMutableResults(t *testing.T) {
	s := &NetworkService{}
	if _, ok := s.latestDiagnosis(); ok {
		t.Fatal("unexpected initial diagnosis")
	}
	input := CampusDiagnosis{Target: "https://example.edu/", Suggestions: []string{"original"}, Probe: &ProbeReport{Result: netcheck.Result{Answers: []string{"10.0.0.1"}, Checks: []netcheck.Check{{Kind: "http", Detail: "original"}}}}}
	s.saveDiagnosis(input)
	input.Suggestions[0] = "input mutation"
	input.Probe.Result.Answers[0] = "input mutation"
	first, ok := s.latestDiagnosis()
	if !ok || first.Suggestions[0] != "original" || first.Probe.Result.Answers[0] != "10.0.0.1" {
		t.Fatalf("input aliases snapshot: %+v", first)
	}
	first.Suggestions[0] = "reader mutation"
	first.Probe.Result.Checks[0].Detail = "reader mutation"
	second, _ := s.latestDiagnosis()
	if second.Suggestions[0] != "original" || second.Probe.Result.Checks[0].Detail != "original" {
		t.Fatalf("reader aliases snapshot: %+v", second)
	}
}
