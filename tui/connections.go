package tui

import (
	"fmt"
	"sort"
	"strings"

	"netmon/internal/collector"
	"netmon/pkg/types"
)

func (m Model) renderConnections(w, h int) string {
	tableW := w * 2 / 3
	smW := w - tableW
	if smW < 30 {
		smW = 30
		tableW = w - smW
	}
	if tableW < 46 {
		tableW = 46
		smW = w - tableW
	}

	conns := append([]types.Connection{}, m.filteredConns()...)
	collector.SortConnections(conns, m.connSort, true)

	filter := m.filters[tabConnections]
	filterNote := ""
	if filter != "" {
		filterNote = styleWarn.Render("filter: \"" + filter + "\" (/ change, esc clears)")
	}

	bodyW := tableW - 4
	bodyH := h - 2

	if m.cursor[tabConnections] >= len(conns) {
		m.cursor[tabConnections] = len(conns) - 1
	}
	if m.cursor[tabConnections] < 0 {
		m.cursor[tabConnections] = 0
	}
	off := m.offset[tabConnections]
	if m.cursor[tabConnections] < off {
		off = m.cursor[tabConnections]
	}
	if m.cursor[tabConnections] >= off+bodyH-5 {
		off = m.cursor[tabConnections] - (bodyH - 5) + 1
	}
	if off < 0 {
		off = 0
	}

	protoW, stateW, pidW := 5, 10, 6
	fixed := protoW + stateW + pidW + 6
	nameW := 14
	remW := bodyW - fixed - nameW
	if remW < 14 {
		remW = 14
	}
	locW := bodyW - fixed - nameW - remW
	if locW < 14 {
		locW = 14
		remW = maxInt(14, bodyW-fixed-nameW-locW)
	}

	head := padRight("PROTO", protoW) + padRight("STATE", stateW) + padLeft("PID", pidW) +
		"  " + padRight("PROCESS", nameW) + padRight("LOCAL", locW) + "REMOTE"
	lines := []string{styleHeaderRow.Render(head)}
	if filterNote != "" {
		lines = append(lines, filterNote)
	}

	for i := off; i < len(conns) && len(lines) <= bodyH-3; i++ {
		c := conns[i]
		proc := c.Process
		if proc == "" {
			proc = "-"
		}
		line := padRight(c.Proto, protoW) + padRight(c.State, stateW) +
			padLeft(fmt.Sprintf("%d", c.PID), pidW) + "  " +
			padRight(truncateTo(proc, nameW), nameW) +
			padRight(truncateTo(c.Local, locW), locW) +
			truncateTo(c.Remote, remW)
		if i == m.cursor[tabConnections] {
			line = styleSel.Render(truncateTo(line, bodyW))
		}
		lines = append(lines, truncateTo(line, bodyW))
	}
	if len(conns) == 0 {
		lines = append(lines, styleDim.Render("no matching connections"))
	}
	lines = append(lines, "",
		styleDim.Render(fmt.Sprintf("showing %d/%d  j/k move  / filter  s sort  g/G top/bottom",
			bodyH-5, len(conns))))

	left := panel("CONNECTIONS", tableW, h, strings.Join(lines, "\n"))
	right := panel("SUMMARY", smW, h, m.connSummary(smW-4, h-2, m.snap.Conns))
	return hstack(left, right, tableW)
}

func (m Model) connSummary(w, h int, conns []types.Connection) string {
	if w < 16 || h < 4 {
		return ""
	}
	var out []string

	byProto := map[string]int{}
	byProc := map[string]int{}
	byRemote := map[string]int{}
	listening := 0
	for _, c := range conns {
		byProto[c.Proto]++
		if c.Process != "" {
			byProc[c.Process]++
		}
		if c.Remote != "" {
			host := hostOf(c.Remote)
			if host != "*" {
				byRemote[host]++
			}
		}
		if strings.Contains(strings.ToUpper(c.State), "LISTEN") {
			listening++
		}
	}

	out = append(out,
		styleDim.Render("total"),
		" "+styleBigNum.Render(fmt.Sprintf("%d", len(conns))),
		"",
		styleDim.Render("listening"), " "+styleText.Render(fmt.Sprintf("%d", listening)),
		"",
		styleDim.Render("protocols"),
		" "+styleText.Render(joinCounts(byProto, ", ")),
		"",
		styleDim.Render("top processes"),
	)
	procRows := topN(byProc, maxInt(4, h/5), "-")
	out = append(out, procRows...)

	out = append(out, "", styleDim.Render("top remotes"))
	remRows := topN(byRemote, maxInt(3, h/6), "-")
	out = append(out, remRows...)

	out = append(out, "",
		styleDim.Render("sort"), " "+styleText.Render(connSortKeys[m.connSortIndex()]))

	lines := fitLines(out, w, h)
	return strings.Join(lines, "\n")
}

func hostOf(addr string) string {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return addr
	}
	h := strings.Trim(addr[:i], "[]")
	if h == "" {
		return addr
	}
	return h
}

func joinCounts(m map[string]int, sep string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
	}
	return strings.Join(parts, sep)
}

func topN(m map[string]int, n int, bullet string) []string {
	type kv struct {
		k string
		v int
	}
	all := make([]kv, 0, len(m))
	for k, v := range m {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(a, b int) bool {
		if all[a].v != all[b].v {
			return all[a].v > all[b].v
		}
		return all[a].k < all[b].k
	})
	var out []string
	for i, it := range all {
		if i >= n {
			break
		}
		out = append(out, fmt.Sprintf(" %s %s %d", bullet, truncateTo(it.k, 12), it.v))
	}
	if len(out) == 0 {
		out = append(out, styleDim.Render(" —"))
	}
	return out
}

func fitLines(rows []string, w, h int) []string {
	out := make([]string, 0, len(rows)+2)
	for _, r := range rows {
		if r == "" {
			out = append(out, "")
			continue
		}
		for _, part := range strings.Split(r, "\n") {
			out = append(out, truncateTo(part, maxInt(w, 1)))
		}
	}
	if len(out) > h {
		out = out[:h]
	}
	return out
}
