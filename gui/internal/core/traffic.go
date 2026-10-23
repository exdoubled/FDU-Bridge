package core

import (
	"encoding/json"
	"time"

	"github.com/mythologyli/zju-connect/routing"
)

func (r *Runtime) readTraffic(data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	var snapshot routing.TrafficSnapshot
	if json.Unmarshal(raw, &snapshot) != nil || snapshot.Started <= 0 || len(snapshot.Connections) > 128 {
		return
	}
	kept := snapshot.Connections[:0]
	for _, c := range snapshot.Connections {
		if routing.AIProvider(c.Host) != "" && (c.Route == "fdu" || c.Route == "non-fdu") {
			kept = append(kept, c)
		}
	}
	snapshot.Connections = kept
	r.mu.Lock()
	if snapshot.Started != r.trafficEpoch {
		r.trafficEpoch, r.trafficLast = snapshot.Started, [2]routing.TrafficTotals{}
	}
	for i, current := range []routing.TrafficTotals{snapshot.FDU, snapshot.NonFDU} {
		bucket, total := &snapshot.FDU, r.traffic.FDU
		if i == 1 {
			bucket, total = &snapshot.NonFDU, r.traffic.NonFDU
		}
		bucket.Upload = total.Upload + trafficDelta(current.Upload, r.trafficLast[i].Upload)
		bucket.Download = total.Download + trafficDelta(current.Download, r.trafficLast[i].Download)
		r.trafficLast[i] = current
	}
	if r.traffic.Started != 0 {
		snapshot.Started = r.traffic.Started // stable epoch for this GUI runtime
	}
	r.traffic, r.trafficAt = snapshot, time.Now().UnixMilli()
	r.mu.Unlock()
}

func trafficDelta(current, previous uint64) uint64 {
	if current < previous {
		return current
	}
	return current - previous
}

func (r *Runtime) Traffic() (routing.TrafficSnapshot, int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot := r.traffic
	snapshot.Connections = append([]routing.TrafficConnection{}, snapshot.Connections...)
	return snapshot, r.trafficAt
}
