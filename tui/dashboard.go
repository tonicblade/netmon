package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"netmon/internal/fmtutil"
	"netmon/pkg/types"
)

func (m Model) renderDashboard(w, h int) string {
	eventsH := 6
	if h < 18 {
		eventsH = 4
	}
	topH := h - eventsH

	var events string

	if w >= 110 && topH >= 12 {
		leftW := w * 56 / 100
		rightW := w - leftW
		latH := topH * 64 / 100
		healthH := topH - latH

		left := panel("NETWORK", leftW, topH, m.networkBody(leftW-4, topH-2))
		right := vstack(
			panel("LATENCY", rightW, latH, m.latencyBody(rightW-4, latH-2)),
			panel("HEALTH", rightW, healthH, m.healthBody(rightW-4, healthH-2)),
		)
		body := hstack(left, right, leftW)
		events = panel("EVENTS", w, eventsH, m.eventsBody(w-4, eventsH-2))
		top := body
		return vstack(top, events)
	}

	netH := topH * 55 / 100
	latH := topH - netH
	body := vstack(
		panel("NETWORK", w, netH, m.networkBody(w-4, netH-2)),
		panel("LATENCY", w, latH, m.latencyBody(w-4, latH-2)),
	)
	events = panel("EVENTS", w, eventsH, m.eventsBody(w-4, eventsH-2))
	return vstack(body, events)
}

func (m Model) networkBody(w, h int) string {
	if w < 20 || h < 4 {
		return ""
	}

	avg, jitter, loss := m.aggregateLatency()

	bigRow := lipgloss.JoinHorizontal(lipgloss.Top,
		bigStat(fmtutil.Bps(m.snap.TotalRx), "Download", w/4),
		bigStat(fmtutil.Bps(m.snap.TotalTx), "Upload", w/4),
		bigStat(fmtutil.Ms(avg), "Latency", w/4),
		bigStat(fmtutil.Ms(jitter), "Jitter", w/4),
	)
	lossRow := styleDim.Render(fmt.Sprintf("packet loss %.1f%%   range %s   mode %s",
		loss, rangeLabel(graphRanges[m.graphRangeIdx]), graphModes[m.graphModeIdx]))

	ifaceRows := 5
	if h < 24 {
		ifaceRows = 3
	}
	graphH := h - 4 - ifaceRows
	if graphH < 4 {
		graphH = 4
	}
	if graphH > h-3 {
		graphH = h - 3
	}
	g := m.bandwidthGraph(w, graphH)

	tbl := m.ifaceTableBody(w, ifaceRows-2)

	return fitWidth(strings.Join([]string{
		bigRow,
		lossRow,
		"",
		g,
		"",
		tbl,
	}, "\n"), w)
}

func bigStat(value, label string, w int) string {
	col := styleBigNum.Render(value) + "\n" + styleBigLbl.Render(label)
	if w < 8 {
		w = 8
	}
	return lipgloss.NewStyle().Width(w).Render(col)
}

func (m Model) bandwidthGraph(w, h int) string {
	rng := graphRanges[m.graphRangeIdx]
	rx := m.snap.Rx.Window(rng)
	tx := m.snap.Tx.Window(rng)

	yFmt := func(v float64) string { return fmtutil.BpsAxis(v) }
	opts := GraphOptions{
		Width:   w,
		Height:  h,
		YFormat: yFmt,
		XLeft:   rangeLabel(rng),
		XRight:  "now",
	}

	switch m.graphModeIdx {
	case 1:
		return RenderGraph(opts, GraphSeries{Data: rx, Line: cRXLine, Fill: cRXFill})
	case 2:
		return RenderGraph(opts, GraphSeries{Data: tx, Line: cTXLine, Fill: cTXFill, Max: true})
	default:
		return RenderGraph(opts,
			GraphSeries{Data: rx, Line: cRXLine},
			GraphSeries{Data: tx, Line: cTXLine},
		)
	}
}

func (m Model) ifaceTableBody(w, h int) string {
	if h < 1 {
		return ""
	}
	rows := make([]types.InterfaceSnapshot, 0, len(m.snap.Interfaces))
	for _, is := range m.snap.Interfaces {
		if !is.Info.IsLoop {
			rows = append(rows, is)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].RxBps > rows[j].RxBps
	})

	header := styleHeaderRow.Render(padRight("INTERFACE", 12) +
		padLeft("RX", 10) + padLeft("TX", 10) + padLeft("PACKETS", 11) + padLeft("ERRORS", 9))
	lines := []string{header}
	for i, is := range rows {
		if i >= h-1 {
			break
		}
		pkts := fmt.Sprintf("%s/s", fmtutil.Count64(int64(is.RxPps+is.TxPps)))
		errs := fmt.Sprintf("%d/%d", is.Counters.RxErrors, is.Counters.TxErrors)
		line := padRight(is.Info.Name, 12) +
			padLeft(fmtutil.Bps(is.RxBps), 10) +
			padLeft(fmtutil.Bps(is.TxBps), 10) +
			padLeft(pkts, 11) +
			padLeft(errs, 9)
		st := styleText
		if is.Counters.RxErrors+is.Counters.TxErrors > 0 {
			st = styleWarn
		}
		lines = append(lines, st.Render(truncateTo(line, w)))
	}
	return strings.Join(lines[:minInt(len(lines), h)], "\n")
}

func (m Model) aggregateLatency() (avg, jitter, loss float64) {
	n := 0
	for _, st := range m.snap.Pings {
		if st.Sent == 0 {
			continue
		}
		avg += st.Avg
		jitter += st.Jitter
		loss += st.LossPct
		n++
	}
	if n == 0 {
		return 0, 0, 0
	}
	return avg / float64(n), jitter / float64(n), loss / float64(n)
}

func (m Model) latencyBody(w, h int) string {
	if w < 20 || h < 3 {
		return ""
	}
	keys := m.pingKeys()
	if len(keys) == 0 {
		return styleDim.Render("waiting for probes...")
	}

	header := styleHeaderRow.Render(padRight("TARGET", 14) + padLeft("LAST", 9) +
		padLeft("AVG", 9) + padLeft("JIT", 8) + padLeft("LOSS", 7) + "  QUALITY")
	lines := []string{header}

	maxAvg := 1.0
	for _, st := range m.snap.Pings {
		if st.Avg > maxAvg {
			maxAvg = st.Avg
		}
	}
	barW := maxInt(8, w-14-9-9-8-7-2)
	if barW > 30 {
		barW = 30
	}

	for _, k := range keys {
		if len(lines) >= h {
			break
		}
		st := m.snap.Pings[k]
		last := fmtutil.Ms(st.Last)
		lastStyle := styleText
		if !st.LastOK {
			lastStyle = styleError
		}
		line := padRight(truncateTo(st.Name, 12), 13) +
			lastStyle.Render(padLeft(last, 9)) +
			padLeft(fmtutil.Ms(st.Avg), 9) +
			padLeft(fmtutil.Ms(st.Jitter), 8) +
			padLeft(fmt.Sprintf("%.1f%%", st.LossPct), 7) + "  " +
			styleDim.Render(barString(100-st.LossPct, 100, 8))
		lines = append(lines, truncateTo(line, w))
	}

	if len(lines) < h {
		avg, jit, loss := m.aggregateLatency()
		lines = append(lines, "")
		if len(lines) < h {
			lines = append(lines, styleDim.Render(fmt.Sprintf(
				"avg %s  jitter %s  loss %.1f%%  targets %d",
				fmtutil.Ms(avg), fmtutil.Ms(jit), loss, len(keys))))
		}
	}
	return strings.Join(lines[:minInt(len(lines), h)], "\n")
}

func (m Model) pingKeys() []string {
	keys := make([]string, 0, len(m.snap.Pings))
	for k := range m.snap.Pings {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	sort.SliceStable(keys, func(i, j int) bool {
		return strings.ToLower(keys[i]) < strings.ToLower(keys[j])
	})
	return keys
}

func (m Model) healthBody(w, h int) string {
	if w < 20 || h < 3 {
		return ""
	}
	hl := m.snap.Health
	color := styleGood
	switch {
	case hl.Score < 50:
		color = styleBad
	case hl.Score < 80:
		color = styleWarn
	}
	barW := minInt(40, w-8)
	lines := []string{
		styleDim.Render("NET QUALITY INDEX (heuristic)"),
		color.Render(barString(float64(hl.Score), 100, barW)) +
			" " + color.Render(fmt.Sprintf("%d/100", hl.Score)),
	}
	if h >= 6 {
		lines = append(lines, "")
		lines = append(lines, strings.Join([]string{
			healthPart("latency", hl.Parts["latency"]),
			healthPart("jitter", hl.Parts["jitter"]),
			healthPart("loss", hl.Parts["loss"]),
		}, "   "))
	}
	if h >= 7 {
		lines = append(lines, strings.Join([]string{
			healthPart("bandwidth", hl.Parts["bandwidth"]),
			healthPart("dns", hl.Parts["dns"]),
		}, "   "))
	}
	return strings.Join(lines[:minInt(len(lines), h)], "\n")
}

func healthPart(name string, v int) string {
	st := styleGood
	switch {
	case v < 50:
		st = styleBad
	case v < 80:
		st = styleWarn
	}
	return styleDim.Render(name) + " " + st.Render(fmt.Sprintf("%d", v))
}

func (m Model) eventsBody(w, h int) string {
	evs := m.snap.Events
	if len(evs) == 0 {
		return styleDim.Render("no events yet")
	}
	start := len(evs) - h
	if start < 0 {
		start = 0
	}
	lines := make([]string, 0, h)
	for _, e := range evs[start:] {
		ts := e.Time.Format("15:04:05")
		line := styleDim.Render(ts) + " " + levelGlyph(int(e.Level)) + " " +
			truncateTo(e.Text, maxInt(0, w-11))
		if e.Detail != "" && w > 50 {
			detail := truncateTo(e.Detail, maxInt(0, w-11-lipgloss.Width(e.Text)-2))
			gap := w - lipgloss.Width(line) - lipgloss.Width(detail)
			if gap > 0 {
				line += strings.Repeat(" ", gap)
			}
			line += styleDim.Render(detail)
		}
		lines = append(lines, truncateTo(line, w))
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func padRight(s string, w int) string {
	s = truncateTo(s, w)
	return s + strings.Repeat(" ", maxInt(0, w-lipgloss.Width(s)))
}

func padLeft(s string, w int) string {
	s = truncateTo(s, w)
	return strings.Repeat(" ", maxInt(0, w-lipgloss.Width(s))) + s
}

func truncateTo(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}

	var b strings.Builder
	width := 0
	for _, r := range s {
		rw := runewidth(r)
		if width+rw > w {
			break
		}
		b.WriteRune(r)
		width += rw
	}
	return b.String()
}

func runewidth(r rune) int {
	if r >= 0x1100 && (r <= 0x115f ||
		r == 0x2329 || r == 0x232a ||
		(r >= 0x2e80 && r <= 0xa4cf && r != 0x303f) ||
		(r >= 0xac00 && r <= 0xd7a3) ||
		(r >= 0xf900 && r <= 0xfaff) ||
		(r >= 0xfe30 && r <= 0xfe6f) ||
		(r >= 0xff00 && r <= 0xff60) ||
		(r >= 0xffe0 && r <= 0xffe6)) {
		return 2
	}
	return 1
}

func fitWidth(s string, w int) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = truncateTo(lines[i], w)
	}
	return strings.Join(lines, "\n")
}
