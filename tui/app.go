package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"netmon/internal/collector"
	"netmon/internal/config"
	"netmon/internal/network"
	"netmon/pkg/types"
)

const (
	tabDashboard = iota
	tabInterfaces
	tabLatency
	tabConnections
	tabRoutes
	tabDNS
	tabTrace
	tabEvents
	tabCount
)

var tabNames = []string{
	"Dashboard", "Interfaces", "Latency", "Connections",
	"Routes", "DNS", "Trace", "Events",
}

var graphRanges = []time.Duration{
	time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour,
}

var graphModes = []string{"RX + TX", "RX", "TX"}

var dnsTypes = []string{"A", "AAAA", "CNAME", "MX", "TXT", "NS"}

type inputMode int

const (
	inputNone inputMode = iota
	inputFilter
	inputDNS
	inputTrace
)

type (
	tickMsg      time.Time
	dnsDoneMsg   struct{ res types.DNSResult }
	benchDoneMsg struct{ rows []types.DNSBenchmarkRow }
	traceHopMsg  struct {
		hops    []types.Hop
		reached bool
	}
	traceDoneMsg struct{ err error }
)

type Model struct {
	mon *collector.Monitor
	cfg *config.Config
	ctx context.Context

	width, height int
	tab           int
	snap          collector.Snapshot
	graphRangeIdx int
	graphModeIdx  int

	cursor    [tabCount]int
	offset    [tabCount]int
	filters   [tabCount]string
	filterTab int

	input textinput.Model
	mode  inputMode

	connSort string

	showHelp bool
	status   string
	statusAt time.Time

	dnsRes          types.DNSResult
	dnsLoading      bool
	dnsBench        []types.DNSBenchmarkRow
	dnsBenchLoaded  bool
	dnsBenchLoading bool
	dnsTypeIdx      int
	dnsServer       string

	traceTarget  string
	traceHops    []types.Hop
	traceRunning bool
	traceErr     string
	traceReached bool
	traceChan    chan traceHopMsg
	traceCancel  context.CancelFunc
}

func New(ctx context.Context, mon *collector.Monitor, cfg *config.Config) Model {
	ti := textinput.New()
	ti.CharLimit = 128
	ti.Prompt = "> "
	ti.PromptStyle = styleTitle
	return Model{
		mon:         mon,
		cfg:         cfg,
		ctx:         ctx,
		snap:        mon.Snapshot(),
		input:       ti,
		traceChan:   make(chan traceHopMsg, 64),
		traceTarget: defaultTraceTarget(cfg),
	}
}

func defaultTraceTarget(cfg *config.Config) string {
	if len(cfg.Targets) > 0 {
		return cfg.Targets[0].Address
	}
	return "1.1.1.1"
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, tickCmd())
}

func tickCmd() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tickMsg:
		m.snap = m.mon.Snapshot()
		cmds := []tea.Cmd{tickCmd()}
		if m.mode != inputNone {
			cmds = append(cmds, textinput.Blink)
		}
		return m, tea.Batch(cmds...)

	case dnsDoneMsg:
		m.dnsLoading = false
		m.dnsRes = msg.res
		m.mon.RecordDNS(msg.res)
		if msg.res.Err != "" {
			m.setStatus("dns error: " + msg.res.Err)
		} else {
			m.setStatus(fmt.Sprintf("%s %s -> %d records in %.1fms",
				msg.res.Query, msg.res.Type, len(msg.res.Records), msg.res.RTT))
		}
		return m, nil

	case benchDoneMsg:
		m.dnsBenchLoaded = true
		m.dnsBench = msg.rows
		m.dnsBenchLoading = false
		return m, nil

	case traceHopMsg:
		m.traceHops = msg.hops
		m.traceReached = msg.reached
		if m.traceRunning {
			return m, waitTraceHop(m)
		}
		return m, nil

	case traceDoneMsg:
		m.traceRunning = false
		if msg.err != nil {
			m.traceErr = msg.err.Error()
			m.setStatus("trace error: " + m.traceErr)
		} else {
			m.traceErr = ""
			m.setStatus(fmt.Sprintf("trace to %s finished (%d hops)", m.traceTarget, len(m.traceHops)))
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func waitTraceHop(m Model) tea.Cmd {
	return func() tea.Msg {
		select {
		case h := <-m.traceChan:
			return traceHopMsg(h)
		case <-m.ctx.Done():
			return nil
		}
	}
}

func (m *Model) setStatus(s string) {
	m.status = s
	m.statusAt = time.Now()
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {

	if m.mode != inputNone {
		switch msg.String() {
		case "esc":
			m.mode = inputNone
			m.input.SetValue("")
			return m, nil
		case "enter":
			mode := m.mode
			fTab := m.filterTab
			val := strings.TrimSpace(m.input.Value())
			m.mode = inputNone
			m.input.SetValue("")
			switch mode {
			case inputDNS:
				if val != "" {
					m.dnsLoading = true
					m.dnsRes = types.DNSResult{}
					return m, runDNS(m, val)
				}
			case inputTrace:
				if val != "" {
					return m.startTrace(val)
				}
			case inputFilter:
				if fTab >= 0 && fTab < tabCount {
					m.filters[fTab] = val
					m.cursor[fTab] = 0
					m.offset[fTab] = 0
				}
				return m, nil
			}
			return m, nil
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
	}

	if m.showHelp {
		switch msg.String() {
		case "?", "esc", "q":
			m.showHelp = false
		}
		return m, nil
	}

	key := msg.String()
	switch key {
	case "q", "ctrl+c":
		if m.traceCancel != nil {
			m.traceCancel()
		}
		return m, tea.Quit

	case "?":
		m.showHelp = true
		return m, nil

	case "1", "2", "3", "4", "5", "6", "7", "8":
		m.tab = int(key[0] - '1')
		return m, nil

	case "tab":
		m.tab = (m.tab + 1) % tabCount
		return m, nil
	case "shift+tab":
		m.tab = (m.tab + tabCount - 1) % tabCount
		return m, nil

	case "p":
		m.mon.TogglePause()
		return m, nil

	case "h":
		if m.tab == tabDashboard || m.tab == tabLatency || m.tab == tabInterfaces {
			if m.graphRangeIdx > 0 {
				m.graphRangeIdx--
			}
		}
		return m, nil
	case "l":
		if m.tab == tabDashboard || m.tab == tabLatency || m.tab == tabInterfaces {
			if m.graphRangeIdx < len(graphRanges)-1 {
				m.graphRangeIdx++
			}
		}
		return m, nil
	case "x":
		m.graphModeIdx = (m.graphModeIdx + 1) % len(graphModes)
		return m, nil

	case "g":
		m.cursor[m.tab] = 0
		m.offset[m.tab] = 0
		return m, nil
	case "G":
		m.cursor[m.tab] = m.listLen(m.tab) - 1
		if m.cursor[m.tab] < 0 {
			m.cursor[m.tab] = 0
		}
		m.clampOffset(m.tab)
		return m, nil

	case "j", "down":
		m.moveCursor(m.tab, 1)
		return m, nil
	case "k", "up":
		m.moveCursor(m.tab, -1)
		return m, nil
	case "pgdown", "ctrl+d":
		m.moveCursor(m.tab, 10)
		return m, nil
	case "pgup", "ctrl+u":
		m.moveCursor(m.tab, -10)
		return m, nil

	case "/":
		switch m.tab {
		case tabInterfaces, tabConnections, tabEvents:
			m.mode = inputFilter
			m.filterTab = m.tab
			m.input.SetValue(m.filters[m.tab])
			m.input.CursorEnd()
			m.input.Focus()
			return m, textinput.Blink
		}
		return m, nil

	case "esc":
		if m.mode != inputNone {
			m.mode = inputNone
			m.input.SetValue("")
		}
		return m, nil

	case "enter":
		if m.tab == tabDNS {
			m.mode = inputDNS
			m.input.SetValue(m.dnsRes.Query)
			m.input.CursorEnd()
			m.input.Focus()
			return m, textinput.Blink
		}
		if m.tab == tabTrace && !m.traceRunning {
			m.mode = inputTrace
			m.input.SetValue(m.traceTarget)
			m.input.CursorEnd()
			m.input.Focus()
			return m, textinput.Blink
		}
		return m, nil

	case "r":
		switch m.tab {
		case tabDNS:
			if m.dnsRes.Query != "" {
				m.dnsLoading = true
				return m, runDNS(m, m.dnsRes.Query)
			}
		case tabTrace:
			if !m.traceRunning && m.traceTarget != "" {
				return m.startTrace(m.traceTarget)
			}
		}
		return m, nil

	case "b":
		if m.tab == tabDNS && !m.dnsBenchLoading {
			m.dnsBenchLoading = true
			m.dnsBenchLoaded = false
			return m, runBench(m)
		}
		return m, nil

	case "n":
		if m.tab == tabDNS {
			m.dnsTypeIdx = (m.dnsTypeIdx + 1) % len(dnsTypes)
			return m, nil
		}
		return m, nil

	case "s":
		if m.tab == tabDNS {
			m.dnsServer = nextDNSServer(m.dnsServer, m.cfg.DNS.Resolvers)
			return m, nil
		}
		if m.tab == tabConnections {
			m.connSortNext()
			return m, nil
		}
		return m, nil

	case "t":
		if m.tab == tabTrace && !m.traceRunning && m.traceTarget != "" {
			return m.startTrace(m.traceTarget)
		}
		return m, nil
	}
	return m, nil
}

func nextDNSServer(cur string, resolvers []string) string {
	chain := append([]string{""}, resolvers...)
	for i, s := range chain {
		if s == cur {
			return chain[(i+1)%len(chain)]
		}
	}
	return ""
}

func (m Model) listLen(tab int) int {
	switch tab {
	case tabInterfaces:
		return len(m.snap.Interfaces)
	case tabLatency:
		return len(m.snap.Pings)
	case tabConnections:
		return len(m.filteredConns())
	case tabRoutes:
		return len(m.snap.Routes)
	case tabEvents:
		return len(m.snap.Events)
	case tabTrace:
		return len(m.traceHops)
	}
	return 0
}

func (m *Model) moveCursor(tab, delta int) {
	m.cursor[tab] += delta
	if m.cursor[tab] < 0 {
		m.cursor[tab] = 0
	}
	if n := m.listLen(tab); m.cursor[tab] > n-1 {
		m.cursor[tab] = n - 1
	}
	if m.cursor[tab] < 0 {
		m.cursor[tab] = 0
	}
	m.clampOffset(tab)
}

func (m *Model) clampOffset(tab int) {
	rows := m.viewportRows(tab)
	if rows <= 0 {
		rows = 8
	}
	if m.cursor[tab] < m.offset[tab] {
		m.offset[tab] = m.cursor[tab]
	}
	if m.cursor[tab] >= m.offset[tab]+rows {
		m.offset[tab] = m.cursor[tab] - rows + 1
	}
	if m.offset[tab] < 0 {
		m.offset[tab] = 0
	}
}

func (m Model) viewportRows(tab int) int {

	body := m.height - 2
	switch tab {
	case tabConnections:
		return body - 6
	case tabInterfaces:
		return 12
	case tabEvents:
		return body - 3
	case tabTrace:
		return 10
	default:
		return body / 3
	}
}

func (m Model) activeFilter(tab int) string {
	return m.filters[tab]
}

func runDNS(m Model, name string) tea.Cmd {
	q := network.DNSQuery{
		Name:   name,
		Type:   dnsTypes[m.dnsTypeIdx],
		Server: m.dnsServer,
	}
	return func() tea.Msg {
		return dnsDoneMsg{res: network.Query(m.ctx, q)}
	}
}

func runBench(m Model) tea.Cmd {
	servers := append([]string{}, m.cfg.DNS.Resolvers...)
	return func() tea.Msg {
		return benchDoneMsg{rows: network.Benchmark(m.ctx, "example.com", servers, 2*time.Second)}
	}
}

func (m Model) startTrace(target string) (tea.Model, tea.Cmd) {
	m.traceTarget = target
	m.traceRunning = true
	m.traceErr = ""
	m.traceHops = nil
	m.traceReached = false
	ctx, cancel := context.WithCancel(m.ctx)
	if m.traceCancel != nil {
		m.traceCancel()
	}
	m.traceCancel = cancel
	ch := m.traceChan
	opts := network.TraceOptions{Target: target}
	return m, tea.Batch(
		func() tea.Msg {
			_, err := network.Trace(ctx, opts, func(hops []types.Hop, reached bool) {
				select {
				case ch <- traceHopMsg{hops: hops, reached: reached}:
				default:
				}
			})
			return traceDoneMsg{err: err}
		},
		waitTraceHop(m),
	)
}

func (m Model) filteredConns() []types.Connection {
	filter := strings.ToLower(strings.TrimSpace(m.filters[tabConnections]))
	conns := m.snap.Conns
	if filter == "" {
		return conns
	}
	out := make([]types.Connection, 0, len(conns))
	for _, c := range conns {
		hay := strings.ToLower(c.Proto + " " + c.Local + " " + c.Remote + " " + c.State + " " + c.Process)
		if strings.Contains(hay, filter) {
			out = append(out, c)
		}
	}
	return out
}

var connSortKeys = []string{"proto", "state", "process", "remote", "local"}

func (m Model) connSortIndex() int {
	if m.connSort == "" {
		return 0
	}
	for i, k := range connSortKeys {
		if k == m.connSort {
			return i
		}
	}
	return 0
}

func (m *Model) connSortNext() {
	idx := m.connSortIndex()
	m.connSort = connSortKeys[(idx+1)%len(connSortKeys)]
}

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "starting..."
	}
	if m.showHelp {
		return m.renderHelp()
	}

	header := m.renderHeader()
	footer := m.renderFooter()

	bodyH := m.height - 2
	var body string
	switch m.tab {
	case tabDashboard:
		body = m.renderDashboard(m.width, bodyH)
	case tabInterfaces:
		body = m.renderInterfaces(m.width, bodyH)
	case tabLatency:
		body = m.renderLatency(m.width, bodyH)
	case tabConnections:
		body = m.renderConnections(m.width, bodyH)
	case tabRoutes:
		body = m.renderRoutes(m.width, bodyH)
	case tabDNS:
		body = m.renderDNS(m.width, bodyH)
	case tabTrace:
		body = m.renderTrace(m.width, bodyH)
	case tabEvents:
		body = m.renderEvents(m.width, bodyH)
	}
	body = fitHeight(body, bodyH)

	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func (m Model) renderHeader() string {
	h := m.snap.Host
	parts := []string{
		styleTitle.Render(" NETMON "),
		styleDim.Render("host ") + styleHeaderVal.Render(h.Hostname),
		styleDim.Render("os ") + styleHeaderVal.Render(h.OS+"/"+h.Arch),
		styleDim.Render("up ") + styleHeaderVal.Render(h.Uptime),
	}

	health := m.snap.Health
	hColor := styleGood
	switch {
	case health.Score < 50:
		hColor = styleBad
	case health.Score < 80:
		hColor = styleWarn
	}
	bar := barString(float64(health.Score), 100, 10)
	parts = append(parts, styleDim.Render("nqi ")+hColor.Render(fmt.Sprintf("%3d", health.Score))+" "+hColor.Render(bar))

	line := strings.Join(parts, "  ")

	clock := time.Now().Format("15:04:05")
	right := styleHeaderVal.Render(clock)
	if m.snap.Paused {
		right += "  " + styleWarn.Render("paused")
	}

	gap := m.width - lipgloss.Width(line) - lipgloss.Width(right)
	if gap < 1 {
		return truncate(line, m.width-lipgloss.Width(right)-1) + " " + right
	}
	return line + strings.Repeat(" ", gap) + right
}

func (m Model) renderFooter() string {
	var b strings.Builder
	for i, name := range tabNames {
		label := fmt.Sprintf("%d %s", i+1, name)
		if i == m.tab {
			b.WriteString(styleTabActive.Render(label))
		} else {
			b.WriteString(styleTabInactive.Render(label))
		}
		b.WriteString(" ")
	}
	line := b.String()

	tail := styleDim.Render("? help  p pause  q quit")
	if m.status != "" && time.Since(m.statusAt) < 15*time.Second {
		st := styleOK
		if strings.Contains(m.status, "error") {
			st = styleError
		}
		tail = st.Render(m.status)
	}

	gap := m.width - lipgloss.Width(line) - lipgloss.Width(tail)
	if gap < 1 {
		return truncate(line, maxInt(0, m.width-lipgloss.Width(tail)-1)) + " " + tail
	}
	return line + strings.Repeat(" ", gap) + tail
}

func fitHeight(s string, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}

func barString(v, max float64, width int) string {
	if max <= 0 {
		return strings.Repeat(" ", width)
	}
	f := v / max
	if f > 1 {
		f = 1
	}
	if f < 0 {
		f = 0
	}
	n := int(f*float64(width) + 0.5)
	return strings.Repeat("█", n) + strings.Repeat("░", width-n)
}

func hstack(left, right string, leftWidth int) string {
	l := strings.Split(left, "\n")
	r := strings.Split(right, "\n")
	n := len(l)
	if len(r) > n {
		n = len(r)
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		var a, b string
		if i < len(l) {
			a = l[i]
		}
		if i < len(r) {
			b = r[i]
		}
		pad := leftWidth - lipgloss.Width(a)
		if pad < 0 {
			a = truncate(a, leftWidth)
			pad = 0
		}
		out[i] = a + strings.Repeat(" ", pad) + b
	}
	return strings.Join(out, "\n")
}

func vstack(parts ...string) string {
	return strings.Join(parts, "\n")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
