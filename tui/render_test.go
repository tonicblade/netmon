package tui

import (
	"fmt"
	"testing"
	"time"

	"netmon/internal/collector"
	"netmon/internal/config"
	"netmon/pkg/types"
)

func fakeSnapshot() collector.Snapshot {
	ts := func(v float64) *types.TimeSeries {
		s := types.NewTimeSeries("t", "bps", 100)
		now := time.Now()
		for i := 0; i < 40; i++ {
			s.AppendAt(now.Add(-time.Duration(40-i)*time.Second), v+float64(i%5))
		}
		return s
	}
	s := collector.Snapshot{
		Host:    types.HostInfo{Hostname: "testhost", OS: "windows", Arch: "amd64", Uptime: "1h"},
		IfaceRx: map[string]*types.TimeSeries{},
		IfaceTx: map[string]*types.TimeSeries{},
		Rx:      ts(80),
		Tx:      ts(40),
		Interfaces: []types.InterfaceSnapshot{
			{Info: types.InterfaceInfo{Name: "Ethernet", IsUp: true, Kind: "eth", MTU: 1500, Index: 1, MAC: "aa:bb", Speed: 1_000_000_000, Addr4: []string{"192.168.4.10"}}, RxBps: 5000, TxBps: 2000},
			{Info: types.InterfaceInfo{Name: "Loopback", IsUp: true, IsLoop: true, Kind: "loop"}},
			{Info: types.InterfaceInfo{Name: "Wi-Fi", IsUp: false, Kind: "wifi"}},
		},
		TotalRx: 5000,
		TotalTx: 2000,
		Pings: map[string]types.PingStats{
			"Gateway": {Name: "Gateway", Address: "192.168.4.1", Sent: 10, Lost: 1, LossPct: 10, Last: 4.2, Avg: 3.9, Min: 3.1, Max: 6.0, Jitter: 0.8},
			"1.1.1.1": {Name: "b.internet", Address: "1.1.1.1", Sent: 9, LastOK: true, Last: 12.5, Avg: 13.1, Min: 11.2, Max: 15.0, Jitter: 1.2},
		},
		Conns: []types.Connection{
			{Proto: "TCP", Local: "192.168.4.10:50431", Remote: "1.1.1.1:443", State: "ESTABLISHED", PID: 404, Process: "chrome.exe"},
			{Proto: "TCP", Local: "0.0.0.0:445", Remote: "-", State: "LISTEN", PID: 4, Process: "System"},
			{Proto: "UDP", Local: "192.168.4.10:5353", Remote: "224.0.0.251:5353", State: "-", PID: 1200, Process: "spotify.exe"},
		},
		Routes: []types.Route{
			{Destination: "0.0.0.0", Gateway: "192.168.4.1", Genmask: "0.0.0.0", Metric: 25, Iface: "Ethernet", Family: "IPv4", Flags: "UG", Default: true},
			{Destination: "192.168.4.0", Gateway: "0.0.0.0", Genmask: "255.255.255.0", Metric: 25, Iface: "Ethernet", Family: "IPv4", Flags: "U"},
			{Destination: "fe80::/64", Gateway: "::", Genmask: "", Metric: 256, Iface: "Wi-Fi", Family: "IPv6", Flags: "U"},
		},
		Events: []types.Event{
			{Time: time.Now().Add(-3 * time.Second), Level: types.LevelWarn, Text: "bandwidth dropped", Detail: "120Kbps now"},
			{Time: time.Now().Add(-10 * time.Second), Level: types.LevelOK, Text: "recovered: bandwidth"},
			{Time: time.Now().Add(-30 * time.Second), Level: types.LevelError, Text: "DNS query failed", Detail: "nxdomain"},
		},
		Health: types.Health{Score: 88, Parts: map[string]int{"latency": 95, "jitter": 90, "loss": 80, "bandwidth": 100, "dns": 75}},
	}
	return s
}

func fakeModel() Model {
	cfg := &config.Config{}
	mon, err := collector.New(cfg)
	if err != nil {
		mon = nil
	}
	_ = mon
	m := New(nil, mon, cfg)
	m.snap = fakeSnapshot()
	m.traceHops = []types.Hop{
		{Num: 1, Addr: "192.168.4.1", Sent: 1, Last: 4.2, Avg: 4.2, LossPct: 0},
		{Num: 2, Addr: "71.164.108.1", Sent: 1, Last: 8.1, Avg: 8.1, LossPct: 0},
		{Num: 3, Addr: "", Sent: 1, Timeouts: 1, LossPct: 100},
	}
	for i := 4; i <= 14; i++ {
		m.traceHops = append(m.traceHops, types.Hop{Num: i + 2})
	}
	m.offset[tabTrace] = 12 // stale scroll from a longer run
	return m
}

func TestRenderAllTabsNoPanic(t *testing.T) {
	for _, w := range []int{40, 60, 80, 120, 170} {
		for _, h := range []int{10, 14, 24, 40, 60} {
			for tab := 0; tab < tabCount; tab++ {
				m := fakeModel()
				m.width, m.height = w, h
				m.tab = tab
				_ = m.View()
			}
		}
	}
}

func TestRenderTraceStaleOffsetNoPanic(t *testing.T) {
	m := fakeModel()
	m.width, m.height = 100, 24
	m.tab = tabTrace
	_ = m.View()
	m.traceHops = nil
	m.offset[tabTrace] = 40
	_ = m.View()
}

func TestRenderGraphGutterWideningNoPanic(t *testing.T) {
	// Long y-labels / wide y-values must re-fit the gutter without letting
	// series columns drift from the grid (regression: index-out-of-range on
	// the area-fill path).
	ts := types.NewTimeSeries("t", "bps", 100)
	now := time.Now()
	for i := 0; i < 200; i++ {
		ts.AppendAt(now.Add(-time.Duration(200-i)*time.Second), 500+float64(i%7))
	}
	opts := GraphOptions{
		Width:  120,
		Height: 16,
		YFormat: func(v float64) string {
			return fmt.Sprintf("%9d", int(v))
		},
		XLeft:  "-15m",
		XRight: "now",
	}
	for _, w := range []int{24, 30, 40, 80, 120} {
		opts.Width = w
		out := RenderGraph(opts,
			GraphSeries{Data: ts.Window(15 * time.Minute), Line: cRXLine},
			GraphSeries{Data: ts.Window(15 * time.Minute), Line: cTXLine, Fill: cTXFill, Max: true},
		)
		if out == "" {
			t.Fatalf("empty graph at width %d", w)
		}
	}
}

func TestRenderHelpNoPanic(t *testing.T) {
	m := fakeModel()
	m.width, m.height = 40, 12
	m.showHelp = true
	_ = m.View()
}
