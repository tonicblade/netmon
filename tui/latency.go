package tui

import (
	"fmt"
	"sort"
	"strings"

	"netmon/internal/fmtutil"
)

func (m Model) renderLatency(w, h int) string {
	keys := m.pingKeys()
	if len(keys) == 0 {
		return panel("LATENCY", w, h, styleDim.Render("waiting for probes..."))
	}

	tableH := h * 70 / 100
	if tableH > len(keys)+4 {
		tableH = len(keys) + 4
	}
	if tableH < 6 {
		tableH = 6
	}
	graphH := h - tableH
	if graphH < 3 {
		graphH = 3
		tableH = h - graphH
		if tableH < 2 {
			tableH = 2
		}
	}

	if m.cursor[tabLatency] >= len(keys) {
		m.cursor[tabLatency] = len(keys) - 1
	}
	if m.cursor[tabLatency] < 0 {
		m.cursor[tabLatency] = 0
	}
	selKey := keys[m.cursor[tabLatency]]

	table := panel("TARGETS", w, tableH,
		m.latencyTable(w-4, tableH-3, keys)+"\n"+m.latencyAggregate(keys))
	detail := panel("PROBE "+selKey, w, graphH, m.latencyDetail(w-4, graphH-2, selKey))
	return vstack(table, detail)
}

func (m Model) latencyTable(w, h int, keys []string) string {
	if w < 20 || h < 2 {
		return ""
	}
	head := " " + padRight("TARGET", 14) + padLeft("LAST", 9) + padLeft("AVG", 9) +
		padLeft("MIN", 9) + padLeft("MAX", 9) + padLeft("JITTER", 9) + padLeft("LOSS", 8) +
		padLeft("SENT", 7) + "  METHOD"
	lines := []string{styleHeaderRow.Render(head)}

	for i, k := range keys {
		if len(lines) >= h {
			break
		}
		st := m.snap.Pings[k]
		lossStr := fmt.Sprintf("%.1f%%", st.LossPct)
		lossCol := styleText
		switch {
		case st.LossPct >= 10:
			lossCol = styleError
		case st.LossPct > 0:
			lossCol = styleWarn
		}
		last := fmtutil.Ms(st.Last)
		lastCol := styleText
		if !st.LastOK {
			lastCol = styleError
		}
		line := padRight(truncateTo(st.Name, 13), 14) +
			lastCol.Render(padLeft(last, 9)) +
			padLeft(fmtutil.Ms(st.Avg), 9) +
			padLeft(fmtutil.Ms(st.Min), 9) +
			padLeft(fmtutil.Ms(st.Max), 9) +
			padLeft(fmtutil.Ms(st.Jitter), 9) +
			lossCol.Render(padLeft(lossStr, 8)) +
			padLeft(fmt.Sprintf("%d", st.Sent), 7) +
			"  " + styleDim.Render(st.Method)

		if i == m.cursor[tabLatency] {
			line = styleSel.Render(truncateTo(line, w))
		}
		lines = append(lines, truncateTo(line, w))
	}
	return strings.Join(lines, "\n")
}

// latencyAggregate summarizes all targets in one footer line.
func (m Model) latencyAggregate(keys []string) string {
	sum := 0.0
	n := 0
	worst := ""
	worstV := 0.0
	for _, k := range keys {
		st, ok := m.snap.Pings[k]
		if !ok || st.Sent == 0 || st.Avg <= 0 {
			continue
		}
		sum += st.Avg
		n++
		if st.Avg > worstV {
			worstV = st.Avg
			worst = st.Name
		}
	}
	if n == 0 {
		return styleDim.Render("  collecting samples...")
	}
	return styleDim.Render("  avg ") + styleText.Render(fmtutil.Ms(sum/float64(n))) +
		styleDim.Render("  worst ") + styleText.Render(fmt.Sprintf("%s %s", truncateTo(worst, 14), fmtutil.Ms(worstV)))
}

func (m Model) latencyDetail(w, h int, key string) string {
	st, ok := m.snap.Pings[key]
	if !ok || w < 10 {
		return ""
	}

	graphH := h - 3
	if graphH < 3 {
		graphH = 3
	}
	var g string
	if ser, ok := m.snap.PingLatency[key]; ok {
		rng := graphRanges[m.graphRangeIdx]
		g = RenderGraph(GraphOptions{
			Width:   w,
			Height:  graphH,
			YFormat: func(v float64) string { return fmtutil.Ms(v) },
			XLeft:   rangeLabel(rng),
			XRight:  "now",
		}, GraphSeries{Data: ser.Window(rng), Line: cLat, Fill: cLatFill})
	}

	summary := fmt.Sprintf("%s  sent %d  recv %d  lost %d  loss %.1f%%  stddev %s  %s -> %s  [%s]",
		padRight("", 0),
		st.Sent, st.Recv, st.Lost, st.LossPct,
		fmtutil.Ms(st.StdDev), st.Name, st.Address, st.Method)

	var sb strings.Builder
	sb.WriteString(g)
	if h > graphH+1 {
		sb.WriteString("\n")
		sb.WriteString(styleDim.Render(truncateTo(summary, w)))
	}
	return sb.String()
}

// pingKeysSorted keeps deterministic ordering for the latency table.
func sortedPingKeys(m Model) []string {
	keys := make([]string, 0, len(m.snap.Pings))
	for k := range m.snap.Pings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
