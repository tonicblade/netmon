package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"netmon/internal/config"
	"netmon/internal/network"
	"netmon/internal/platform"
	"netmon/pkg/types"
)

const (
	maxEvents      = 300
	pingWindow     = 300
	seriesCapIface = 14400
	seriesCapPing  = 3600
)

type Snapshot struct {
	Host        types.HostInfo
	Generated   time.Time
	Interfaces  []types.InterfaceSnapshot
	TotalRx     float64
	TotalTx     float64
	Rx          *types.TimeSeries
	Tx          *types.TimeSeries
	IfaceRx     map[string]*types.TimeSeries
	IfaceTx     map[string]*types.TimeSeries
	Pings       map[string]types.PingStats
	PingLatency map[string]*types.TimeSeries
	Conns       []types.Connection
	ConnCounts  map[string]int
	Routes      []types.Route
	Events      []types.Event
	Health      types.Health
	Paused      bool
}

type Monitor struct {
	cfg   *config.Config
	plat  platform.Collector
	httpc *http.Client

	mu           sync.RWMutex
	snap         Snapshot
	prevCounters map[string]types.InterfaceStats
	pingWindow   map[string][]float64
	alertActive  map[string]bool
	pingers      map[string]*network.Pinger
	targets      []types.PingTarget
	dnsRTT       float64
	dnsMeasured  bool
	paused       bool

	bwDropTicks int
	bwOKTicks   int

	events []types.Event

	stop chan struct{}
	wg   sync.WaitGroup
}

func New(cfg *config.Config) (*Monitor, error) {
	plat := platform.New()
	m := &Monitor{
		cfg:          cfg,
		plat:         plat,
		httpc:        &http.Client{Timeout: 3 * time.Second},
		prevCounters: map[string]types.InterfaceStats{},
		pingWindow:   map[string][]float64{},
		alertActive:  map[string]bool{},
		pingers:      map[string]*network.Pinger{},
		targets:      append([]types.PingTarget{}, cfg.Targets...),
		stop:         make(chan struct{}),
		snap: Snapshot{
			Host:        platform.HostInfo(),
			Generated:   time.Now(),
			IfaceRx:     map[string]*types.TimeSeries{},
			IfaceTx:     map[string]*types.TimeSeries{},
			Pings:       map[string]types.PingStats{},
			PingLatency: map[string]*types.TimeSeries{},
			ConnCounts:  map[string]int{},
			Rx:          types.NewTimeSeries("rx", "bps", seriesCapIface),
			Tx:          types.NewTimeSeries("tx", "bps", seriesCapIface),
			Health:      types.Health{Score: 100, Parts: map[string]int{}},
		},
	}
	m.targets = m.withGatewayTarget()
	return m, nil
}

func (m *Monitor) withGatewayTarget() []types.PingTarget {
	routes, err := m.plat.Routes()
	if err != nil {
		return m.targets
	}
	var gw string
	for _, r := range routes {
		if r.Default && r.Gateway != "" && r.Gateway != "0.0.0.0" && r.Gateway != "::" {
			gw = r.Gateway
			break
		}
	}
	if gw == "" {
		return m.targets
	}
	for _, t := range m.targets {
		if t.Address == gw {
			return m.targets
		}
	}
	return append([]types.PingTarget{{Name: "Gateway", Address: gw}}, m.targets...)
}

func (m *Monitor) Targets() []types.PingTarget { return m.targets }

func (m *Monitor) Start(ctx context.Context) {
	m.AddEvent(types.LevelOK, "netmon started",
		fmt.Sprintf("%d latency targets, interfaces every %s", len(m.targets), m.cfg.Refresh.Interface.D()))

	m.wg.Add(1)
	go func() { defer m.wg.Done(); m.ifaceLoop(ctx) }()

	for _, t := range m.targets {
		t := t
		m.wg.Add(1)
		go func() { defer m.wg.Done(); m.pingLoop(ctx, t) }()
	}

	m.wg.Add(1)
	go func() { defer m.wg.Done(); m.connLoop(ctx) }()

	m.wg.Add(1)
	go func() { defer m.wg.Done(); m.routeLoop(ctx) }()

	m.wg.Add(1)
	go func() { defer m.wg.Done(); m.hostLoop(ctx) }()
}

func (m *Monitor) Stop() {
	select {
	case <-m.stop:
		return
	default:
		close(m.stop)
	}
	m.wg.Wait()
	m.mu.Lock()
	for _, p := range m.pingers {
		p.Close()
	}
	m.pingers = map[string]*network.Pinger{}
	m.mu.Unlock()
}

func (m *Monitor) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.snap
	s.Host = m.snap.Host
	s.Interfaces = append([]types.InterfaceSnapshot(nil), m.snap.Interfaces...)
	s.Pings = make(map[string]types.PingStats, len(m.snap.Pings))
	for k, v := range m.snap.Pings {
		s.Pings[k] = v.Clone()
	}
	s.IfaceRx = copySeriesMap(m.snap.IfaceRx)
	s.IfaceTx = copySeriesMap(m.snap.IfaceTx)
	s.PingLatency = copySeriesMap(m.snap.PingLatency)
	s.Conns = append([]types.Connection(nil), m.snap.Conns...)
	s.ConnCounts = make(map[string]int, len(m.snap.ConnCounts))
	for k, v := range m.snap.ConnCounts {
		s.ConnCounts[k] = v
	}
	s.Routes = append([]types.Route(nil), m.snap.Routes...)
	s.Events = append([]types.Event(nil), m.events...)
	s.Health = m.snap.Health
	s.Paused = m.paused

	return s
}

func copySeriesMap(in map[string]*types.TimeSeries) map[string]*types.TimeSeries {
	out := make(map[string]*types.TimeSeries, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (m *Monitor) TogglePause() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.paused = !m.paused
	if m.paused {
		m.addEventLocked(types.LevelInfo, "paused", "")
	} else {
		m.addEventLocked(types.LevelInfo, "resumed", "")
	}
	return m.paused
}

func (m *Monitor) Paused() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.paused
}

func (m *Monitor) AddEvent(level types.Level, text, detail string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addEventLocked(level, text, detail)
}

func (m *Monitor) addEventLocked(level types.Level, text, detail string) {
	m.events = append(m.events, types.Event{
		Time: time.Now(), Level: level, Text: text, Detail: detail,
	})
	if len(m.events) > maxEvents {
		m.events = m.events[len(m.events)-maxEvents:]
	}
}

func (m *Monitor) RecordDNS(res types.DNSResult) {
	m.mu.Lock()
	if res.Err == "" {
		m.dnsRTT = res.RTT
		m.dnsMeasured = true
	}
	m.mu.Unlock()
	if res.Err != "" {
		m.AddEvent(types.LevelError, "DNS query failed",
			fmt.Sprintf("%s %s via %s: %s", res.Query, res.Type, res.Server, res.Err))
	} else {
		m.AddEvent(types.LevelInfo, fmt.Sprintf("DNS %s %s", res.Query, res.Type),
			fmt.Sprintf("%s -> %d records, %s", res.Server, len(res.Records), fmtMs(res.RTT)))
	}
	m.recomputeHealth()
}

func (m *Monitor) ifaceLoop(ctx context.Context) {
	interval := m.cfg.Refresh.Interface.D()
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	t := time.NewTicker(interval)
	defer t.Stop()

	ifaceRefresh := time.NewTicker(10 * time.Second)
	defer ifaceRefresh.Stop()

	m.refreshInterfaces()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stop:
			return
		case <-ifaceRefresh.C:
			m.refreshInterfaces()
		case now := <-t.C:
			m.collectInterfaces(now, interval)
		}
	}
}

func (m *Monitor) refreshInterfaces() {
	infos, err := m.plat.Interfaces()
	if err != nil {
		return
	}
	m.mu.Lock()
	m.snap.Interfaces = mergeInfos(infos)
	m.mu.Unlock()
}

func mergeInfos(infos []types.InterfaceInfo) []types.InterfaceSnapshot {
	out := make([]types.InterfaceSnapshot, 0, len(infos))
	for _, i := range infos {
		out = append(out, types.InterfaceSnapshot{Info: i})
	}
	return out
}

func (m *Monitor) collectInterfaces(now time.Time, fallbackInterval time.Duration) {
	stats, err := m.plat.InterfaceStats()
	if err != nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.paused {

		for _, s := range stats {
			m.prevCounters[s.Name] = s
		}
		return
	}

	prevByName := map[string]types.InterfaceSnapshot{}
	for _, is := range m.snap.Interfaces {
		prevByName[is.Info.Name] = is
	}

	newSnap := make([]types.InterfaceSnapshot, 0, len(stats))
	var totalRx, totalTx float64

	for _, cur := range stats {
		prev, ok := m.prevCounters[cur.Name]
		m.prevCounters[cur.Name] = cur

		var rxBps, txBps, rxPps, txPps float64
		if ok {
			dt := cur.Timestamp.Sub(prev.Timestamp).Seconds()
			if dt <= 0 {
				dt = fallbackInterval.Seconds()
			}
			if cur.RxBytes >= prev.RxBytes {
				rxBps = float64(cur.RxBytes-prev.RxBytes) * 8 / dt
			}
			if cur.TxBytes >= prev.TxBytes {
				txBps = float64(cur.TxBytes-prev.TxBytes) * 8 / dt
			}
			if cur.RxPackets >= prev.RxPackets {
				rxPps = float64(cur.RxPackets-prev.RxPackets) / dt
			}
			if cur.TxPackets >= prev.TxPackets {
				txPps = float64(cur.TxPackets-prev.TxPackets) / dt
			}
		}

		is := types.InterfaceSnapshot{
			Info:     infoFor(prevByName, cur.Name),
			Counters: cur,
			RxBps:    rxBps,
			TxBps:    txBps,
			RxPps:    rxPps,
			TxPps:    txPps,
		}
		if is.Info.Name == "" {
			is.Info.Name = cur.Name
		}
		newSnap = append(newSnap, is)

		if prev, had := prevByName[cur.Name]; had && prev.Info.IsUp != is.Info.IsUp {
			lvl := types.LevelWarn
			state := "down"
			if is.Info.IsUp {
				lvl = types.LevelOK
				state = "up"
			}
			m.addEventLocked(lvl, fmt.Sprintf("%s link %s", cur.Name, state), "")
		}

		if !is.Info.IsLoop {
			totalRx += rxBps
			totalTx += txBps
		}

		if !is.Info.IsLoop {
			rx := m.snap.IfaceRx[cur.Name]
			if rx == nil {
				rx = types.NewTimeSeries(cur.Name+" rx", "bps", seriesCapIface)
				m.snap.IfaceRx[cur.Name] = rx
			}
			tx := m.snap.IfaceTx[cur.Name]
			if tx == nil {
				tx = types.NewTimeSeries(cur.Name+" tx", "bps", seriesCapIface)
				m.snap.IfaceTx[cur.Name] = tx
			}
			rx.AppendAt(now, rxBps)
			tx.AppendAt(now, txBps)
		}
	}

	m.snap.Interfaces = newSnap
	m.snap.TotalRx = totalRx
	m.snap.TotalTx = totalTx
	m.snap.Rx.AppendAt(now, totalRx)
	m.snap.Tx.AppendAt(now, totalTx)
	m.snap.Generated = now

	m.applyBandwidthAlert(totalRx + totalTx)
}

func infoFor(prev map[string]types.InterfaceSnapshot, name string) types.InterfaceInfo {
	if s, ok := prev[name]; ok {
		return s.Info
	}
	return types.InterfaceInfo{Name: name}
}

func targetKey(t types.PingTarget) string {
	if t.Name != "" {
		return t.Name
	}
	return t.Address
}

func (m *Monitor) pingLoop(ctx context.Context, t types.PingTarget) {
	key := targetKey(t)
	interval := m.cfg.Refresh.Ping.D()
	if interval <= 0 {
		interval = time.Second
	}
	timeout := m.cfg.Ping.Timeout.D()
	if timeout <= 0 {
		timeout = time.Second
	}
	port := t.Port
	if port == 0 {
		port = m.cfg.Ping.Port
	}

	p, err := network.NewPinger(t.Address, port)
	if err != nil {
		m.AddEvent(types.LevelError, fmt.Sprintf("cannot probe %s", t.Address), err.Error())
		return
	}
	m.mu.Lock()
	m.pingers[key] = p
	m.mu.Unlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	m.probeOnce(p, t, key, timeout)
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stop:
			return
		case <-ticker.C:
			m.probeOnce(p, t, key, timeout)
		}
	}
}

func (m *Monitor) probeOnce(p *network.Pinger, t types.PingTarget, key string, timeout time.Duration) {
	m.mu.RLock()
	paused := m.paused
	m.mu.RUnlock()
	if paused {
		return
	}

	rtt, err := p.Probe(timeout)
	now := time.Now()

	m.mu.Lock()
	lost := err != nil
	ms := 0.0
	if !lost {
		ms = float64(rtt.Nanoseconds()) / 1e6
	}

	w := m.pingWindow[key]
	if !lost {
		w = append(w, ms)
		if len(w) > pingWindow {
			w = w[len(w)-pingWindow:]
		}
		m.pingWindow[key] = w
	}

	st := m.snap.Pings[key]
	st.Name = t.Name
	if st.Name == "" {
		st.Name = t.Address
	}
	st.Address = p.Addr()
	st.Method = string(p.Method())
	st.Sent++
	st.Updated = now
	st.LastOK = !lost
	if lost {
		st.Lost++
		st.Last = 0
	} else {
		st.Last = ms
	}
	st.LossPct = float64(st.Lost) / float64(st.Sent) * 100

	if len(w) > 0 {
		st.Avg = types.Avg(w)
		st.Min = types.Min(w)
		st.Max = types.Max(w)
		st.Jitter = types.Jitter(w)
		st.StdDev = types.StdDev(w)
	}

	m.snap.Pings[key] = st

	if _, has := m.pingLatencySeries(key); !has {
		m.snap.PingLatency[key] = types.NewTimeSeries(st.Name+" latency", "ms", seriesCapPing)
	}
	if !lost {
		m.snap.PingLatency[key].AppendAt(now, ms)
	}
	m.mu.Unlock()

	m.checkPingAlerts(key, st)
	m.recomputeHealth()
}

func (m *Monitor) pingLatencySeries(key string) (*types.TimeSeries, bool) {
	s, ok := m.snap.PingLatency[key]
	return s, ok
}

func (m *Monitor) connLoop(ctx context.Context) {
	interval := m.cfg.Refresh.Connections.D()
	if interval <= 0 {
		interval = 2 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	m.refreshConnections()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stop:
			return
		case <-t.C:
			m.refreshConnections()
		}
	}
}

func (m *Monitor) refreshConnections() {
	m.mu.RLock()
	paused := m.paused
	m.mu.RUnlock()
	if paused {
		return
	}
	conns, err := m.plat.Connections()
	if err != nil {
		return
	}
	counts := map[string]int{}
	for _, c := range conns {
		counts[strings.ToLower(c.Proto)]++
		switch c.State {
		case "LISTEN":
			counts["listen"]++
		case "ESTABLISHED":
			counts["established"]++
		}
	}
	m.mu.Lock()
	m.snap.Conns = conns
	m.snap.ConnCounts = counts
	m.mu.Unlock()
}

func (m *Monitor) routeLoop(ctx context.Context) {
	interval := m.cfg.Refresh.Routes.D()
	if interval <= 0 {
		interval = 10 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	m.refreshRoutes()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stop:
			return
		case <-t.C:
			m.refreshRoutes()
		}
	}
}

func (m *Monitor) refreshRoutes() {
	routes, err := m.plat.Routes()
	if err != nil {
		return
	}
	m.mu.Lock()
	m.snap.Routes = routes
	m.mu.Unlock()
}

func (m *Monitor) hostLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stop:
			return
		case <-t.C:
			m.mu.Lock()
			m.snap.Host = platform.HostInfo()
			m.mu.Unlock()
		}
	}
}

func (m *Monitor) checkPingAlerts(key string, st types.PingStats) {
	rule := m.cfg.Rule()

	type probe struct {
		id     string
		active bool
		level  types.Level
		title  string
		detail string
	}
	probes := []probe{
		{
			id:     "latency:" + key,
			active: rule.LatencyMS > 0 && st.Avg > rule.LatencyMS && st.Sent > 3,
			level:  types.LevelWarn,
			title:  fmt.Sprintf("latency > %s", fmtMs(rule.LatencyMS)),
			detail: fmt.Sprintf("%s avg %s", st.Name, fmtMs(st.Avg)),
		},
		{
			id:     "loss:" + key,
			active: rule.LossPct > 0 && st.LossPct >= rule.LossPct && st.Sent > 5,
			level:  types.LevelWarn,
			title:  fmt.Sprintf("packet loss > %.0f%%", rule.LossPct),
			detail: fmt.Sprintf("%s loss %.1f%%", st.Name, st.LossPct),
		},
		{
			id:     "jitter:" + key,
			active: rule.JitterMS > 0 && st.Jitter > rule.JitterMS && st.Sent > 5,
			level:  types.LevelWarn,
			title:  fmt.Sprintf("jitter > %s", fmtMs(rule.JitterMS)),
			detail: fmt.Sprintf("%s jitter %s", st.Name, fmtMs(st.Jitter)),
		},
	}

	for _, p := range probes {
		m.mu.Lock()
		was := m.alertActive[p.id]
		if p.active && !was {
			m.alertActive[p.id] = true
			m.addEventLocked(p.level, p.title, p.detail)
			m.mu.Unlock()
			m.notify(p.level, p.title, p.detail)
			continue
		}
		if !p.active && was {
			delete(m.alertActive, p.id)
			m.addEventLocked(types.LevelOK, "recovered: "+p.title, p.detail)
			m.mu.Unlock()
			m.notify(types.LevelOK, "recovered: "+p.title, p.detail)
			continue
		}
		m.mu.Unlock()
	}
}

func (m *Monitor) applyBandwidthAlert(totalBps float64) {
	rule := m.cfg.Rule()
	if rule.BandwidthDropPct <= 0 {
		return
	}

	ref := m.snap.Rx.Avg(60*time.Second) + m.snap.Tx.Avg(60*time.Second)
	if ref <= 1000 {
		return
	}
	dropPct := (1 - totalBps/ref) * 100
	activeNow := dropPct >= rule.BandwidthDropPct

	const (
		dropTicks    = 6
		recoverTicks = 4
	)
	if activeNow {
		m.bwDropTicks++
		m.bwOKTicks = 0
	} else {
		m.bwOKTicks++
		m.bwDropTicks = 0
	}

	id := "bandwidth"
	was := m.alertActive[id]
	if !was && m.bwDropTicks >= dropTicks {
		m.alertActive[id] = true
		detail := fmt.Sprintf("%s now, recent avg %s (-%.0f%%)",
			fmtBps(totalBps), fmtBps(ref), dropPct)
		m.addEventLocked(types.LevelWarn, "bandwidth dropped", detail)
		m.notify(types.LevelWarn, "bandwidth dropped", detail)
		return
	}
	if was && m.bwOKTicks >= recoverTicks {
		delete(m.alertActive, id)
		m.addEventLocked(types.LevelOK, "recovered: bandwidth", "")
		m.notify(types.LevelOK, "recovered: bandwidth", "")
	}
}

func (m *Monitor) notify(level types.Level, title, detail string) {
	if m.cfg.Alerts.Webhook != "" {
		url := m.cfg.Alerts.Webhook
		payload := map[string]string{
			"content": fmt.Sprintf("[netmon] %s — %s: %s", strings.ToUpper(level.String()), title, detail),
			"title":   title,
			"detail":  detail,
			"level":   level.String(),
		}
		body, _ := json.Marshal(payload)
		go func() {
			resp, err := m.httpc.Post(url, "application/json", bytes.NewReader(body))
			if err == nil {
				resp.Body.Close()
			}
		}()
	}
	if m.cfg.Alerts.LogFile != "" {
		line := fmt.Sprintf("%s [%s] %s %s\n",
			time.Now().Format("2006-01-02 15:04:05"), level.String(), title, detail)
		go func() {
			f, err := os.OpenFile(m.cfg.Alerts.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				return
			}
			defer f.Close()
			f.WriteString(line)
		}()
	}
}

func (m *Monitor) recomputeHealth() {
	m.mu.Lock()
	defer m.mu.Unlock()

	lat := make([]float64, 0, len(m.snap.Pings))
	jit := make([]float64, 0, len(m.snap.Pings))
	loss := make([]float64, 0, len(m.snap.Pings))
	for _, st := range m.snap.Pings {
		if st.Sent < 3 {
			continue
		}
		lat = append(lat, st.Avg)
		jit = append(jit, st.Jitter)
		loss = append(loss, st.LossPct)
	}

	latScore := 100
	if len(lat) > 0 {
		latScore = clamp(100-int((types.Avg(lat)/150)*100), 0, 100)
		if types.Avg(lat) <= 10 {
			latScore = 100
		}
	}
	jitScore := 100
	if len(jit) > 0 {
		jitScore = clamp(100-int((types.Avg(jit)/30)*100), 0, 100)
		if types.Avg(jit) <= 2 {
			jitScore = 100
		}
	}
	lossScore := 100
	if len(loss) > 0 {
		lossScore = clamp(100-int((types.Avg(loss)/10)*100), 0, 100)
	}

	var drops, pkts float64
	for _, is := range m.snap.Interfaces {
		if is.Info.IsLoop {
			continue
		}
		drops += is.RxPps*0 + float64(is.Counters.RxDrops) + float64(is.Counters.TxDrops)
		pkts += float64(is.Counters.RxPackets + is.Counters.TxPackets)
	}
	bwScore := 100
	if pkts > 0 {
		ratio := drops / pkts
		bwScore = clamp(100-int(ratio*10000), 0, 100)
	}

	dnsScore := 100
	if m.dnsMeasured {
		if m.dnsRTT <= 10 {
			dnsScore = 100
		} else if m.dnsRTT >= 150 {
			dnsScore = 0
		} else {
			dnsScore = clamp(100-int(((m.dnsRTT-10)/140)*100), 0, 100)
		}
	}

	parts := map[string]int{
		"latency":   latScore,
		"jitter":    jitScore,
		"loss":      lossScore,
		"bandwidth": bwScore,
		"dns":       dnsScore,
	}

	total := int(float64(latScore)*0.35 + float64(jitScore)*0.15 +
		float64(lossScore)*0.30 + float64(bwScore)*0.10 + float64(dnsScore)*0.10)

	m.snap.Health = types.Health{
		Score:     clamp(total, 0, 100),
		Parts:     parts,
		UpdatedAt: time.Now(),
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func fmtMs(v float64) string { return fmt.Sprintf("%.2fms", v) }

func fmtBps(v float64) string {
	switch {
	case v >= 1_000_000_000:
		return fmt.Sprintf("%.2f Gbps", v/1_000_000_000)
	case v >= 1_000_000:
		return fmt.Sprintf("%.1f Mbps", v/1_000_000)
	case v >= 1_000:
		return fmt.Sprintf("%.1f Kbps", v/1_000)
	default:
		return fmt.Sprintf("%.0f bps", v)
	}
}

func SortConnections(conns []types.Connection, key string, desc bool) {
	less := func(a, b types.Connection) bool { return false }
	switch key {
	case "port":
		less = func(a, b types.Connection) bool { return a.Local < b.Local }
	case "process":
		less = func(a, b types.Connection) bool { return a.Process < b.Process }
	case "remote":
		less = func(a, b types.Connection) bool { return a.Remote < b.Remote }
	case "proto":
		less = func(a, b types.Connection) bool { return a.Proto < b.Proto }
	case "state":
		less = func(a, b types.Connection) bool { return a.State < b.State }
	}
	sort.SliceStable(conns, func(i, j int) bool {
		if desc {
			return less(conns[j], conns[i])
		}
		return less(conns[i], conns[j])
	})
}
