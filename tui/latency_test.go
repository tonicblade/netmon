package tui

import (
	"fmt"
	"testing"
	"time"

	"netmon/pkg/types"
)

func TestLatencyChurnNoPanic(t *testing.T) {
	for _, w := range []int{30, 40, 60, 80, 120, 200} {
		for _, h := range []int{5, 6, 8, 12, 24, 40, 80} {
			for keys := 0; keys <= 3; keys++ {
				m := fakeModel()
				m.width, m.height = w, h
				m.tab = tabLatency
				for n := 0; n < 30; n++ {
					m.snap.Pings = map[string]types.PingStats{}
					for k := 0; k < keys; k++ {
						m.snap.Pings[fmt.Sprintf("target-%d", k)] = types.PingStats{
							Name: "target", Address: "1.1.1.1", Sent: n + 1,
							Last: 4, Avg: 5, LastOK: true, Method: "icmp",
						}
					}
					m.cursor[tabLatency] = n
					m.offset[tabLatency] = n
					_ = m.renderLatency(w, h)
				}
			}
		}
	}
}

func TestLatencyGraphReadability(t *testing.T) {
	m := fakeModel()
	m.width, m.height = 120, 30
	m.tab = tabLatency
	ts := types.NewTimeSeries("lat", "ms", 100)
	now := time.Now()
	for i := 0; i < 60; i++ {
		ts.AppendAt(now.Add(-time.Duration(60-i)*time.Second), 8+float64(i%3))
	}
	m.snap.PingLatency = map[string]*types.TimeSeries{"target": ts}
	m.snap.Pings = map[string]types.PingStats{
		"target": {Name: "target", Address: "1.1.1.1", Sent: 60, Last: 9, Avg: 9.2, Min: 8, Max: 11, Jitter: 0.5, Method: "icmp"},
	}
	out := m.renderLatency(120, 30)
	if out == "" {
		t.Fatal("empty render")
	}
}
