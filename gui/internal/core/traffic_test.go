package core

import (
	"strings"
	"testing"
)

func TestTrafficEventsStayOutOfLogsAndSnapshotsAreCopied(t *testing.T) {
	r := &Runtime{OnLog: func(string) { t.Fatal("traffic event leaked into logs") }}
	r.readStdout(strings.NewReader(guiPrefix + `{"kind":"traffic","data":{"started":123,"fdu":{"upload":7,"download":9},"connections":[{"id":"1","provider":"openai","host":"api.openai.com","port":"443","network":"tcp","route":"fdu"}]}}` + "\n"))
	got, at := r.Traffic()
	if at == 0 || got.FDU.Upload != 7 || len(got.Connections) != 1 {
		t.Fatalf("traffic event not retained: %+v", got)
	}
	got.Connections[0].Host = "changed.example"
	again, _ := r.Traffic()
	if again.Connections[0].Host != "api.openai.com" {
		t.Fatal("caller modified retained traffic")
	}
}

func TestTrafficTotalsSurviveCoreEpochChanges(t *testing.T) {
	r, _, config := newRuntime(t, false)
	r.readStdout(strings.NewReader(guiPrefix + `{"kind":"traffic","data":{"started":100,"fdu":{"upload":7},"nonFdu":{"download":10}}}` + "\n"))
	r.readStdout(strings.NewReader(guiPrefix + `{"kind":"traffic","data":{"started":200,"fdu":{"upload":3},"nonFdu":{"download":5}}}` + "\n"))
	r.readStdout(strings.NewReader(guiPrefix + `{"kind":"traffic","data":{"started":200,"fdu":{"upload":4},"nonFdu":{"download":6}}}` + "\n"))
	got, _ := r.Traffic()
	if got.FDU.Upload != 11 || got.NonFDU.Download != 16 {
		t.Fatalf("core restart lost totals: %+v", got)
	}
	if err := r.Start(config, nil, "auth-info"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Kill(); r.Wait() })
	got, _ = r.Traffic()
	if got.FDU.Upload != 11 || got.NonFDU.Download != 16 {
		t.Fatal("new launch discarded unsampled totals")
	}
}
