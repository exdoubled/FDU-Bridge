package authchallenge

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestGUITrafficEmitsOnlyForOwnedGUI(t *testing.T) {
	if os.Getenv("FDU_TRAFFIC_EMITTER_TEST_CHILD") == "1" {
		GUITraffic(map[string]any{"started": 123, "fdu": map[string]any{"upload": 7}})
		return
	}
	for _, enabled := range []string{"0", "1"} {
		t.Run(enabled, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestGUITrafficEmitsOnlyForOwnedGUI$")
			cmd.Env = append(os.Environ(), "FDU_TRAFFIC_EMITTER_TEST_CHILD=1", "FDU_CONNECT_GUI="+enabled)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("emitter subprocess: %v\n%s", err, output)
			}
			var events []map[string]any
			for _, line := range strings.Split(string(output), "\n") {
				if !strings.HasPrefix(line, guiPrefix) {
					continue
				}
				var event map[string]any
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, guiPrefix)), &event); err != nil {
					t.Fatal(err)
				}
				events = append(events, event)
			}
			if enabled == "0" {
				if len(events) != 0 {
					t.Fatalf("CLI emitted GUI traffic: %s", output)
				}
				return
			}
			if len(events) != 1 || events[0]["kind"] != "traffic" {
				t.Fatalf("missing traffic event: %s", output)
			}
			data, ok := events[0]["data"].(map[string]any)
			if !ok || data["started"] != float64(123) || data["fdu"].(map[string]any)["upload"] != float64(7) {
				t.Fatalf("traffic payload changed: %s", output)
			}
		})
	}
}
