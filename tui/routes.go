package tui

import (
	"fmt"
	"strings"
)

type routeRow struct {
	fam, dest, mask, gw, iface, flags string
	metric                            int
	isDefault                         bool
}

func (m Model) renderRoutes(w, h int) string {
	routes := m.snap.Routes
	if len(routes) == 0 {
		return panel("ROUTES", w, h, styleDim.Render("no routes collected"))
	}

	tableW := w * 2 / 3
	sumW := w - tableW
	if sumW < 30 {
		sumW = 30
		tableW = w - sumW
	}
	if tableW < 46 {
		tableW = 46
		sumW = w - tableW
	}

	bodyW := tableW - 4
	rows := make([]routeRow, 0, len(routes))
	for _, r := range routes {
		rows = append(rows, routeRow{
			fam: r.Family, dest: r.Destination, mask: r.Genmask, gw: r.Gateway,
			iface: r.Iface, flags: r.Flags, metric: r.Metric, isDefault: r.Default,
		})
	}
	sortRoutes(rows)

	rowsAvail := (h - 2) - 3
	if rowsAvail < 1 {
		rowsAvail = 1
	}

	cursor := m.cursor[tabRoutes]
	if cursor >= len(rows) {
		cursor = len(rows) - 1
	}
	if cursor < 0 {
		cursor = 0
	}
	off := m.offset[tabRoutes]
	if cursor < off {
		off = cursor
	}
	if cursor >= off+rowsAvail {
		off = cursor - rowsAvail + 1
	}
	if off < 0 {
		off = 0
	}

	head := padRight("FAMILY", 7) + padRight("DESTINATION", 20) + padRight("GENMASK", 18) +
		padRight("GATEWAY", 18) + padLeft("METRIC", 7) + "  " + padRight("IFACE", 10) + "FLAGS"
	lines := []string{styleHeaderRow.Render(truncateTo(head, bodyW))}

	for i := off; i < len(rows) && len(lines) <= rowsAvail; i++ {
		r := rows[i]
		line := padRight(r.fam, 7) + padRight(truncateTo(r.dest, 19), 20) +
			padRight(truncateTo(r.mask, 17), 18) + padRight(truncateTo(r.gw, 17), 18) +
			padLeft(fmt.Sprintf("%d", r.metric), 7) + "  " +
			padRight(r.iface, 10) + r.flags
		switch {
		case i == cursor:
			line = styleSel.Render(truncateTo(line, bodyW))
		case r.isDefault:
			line = styleOK.Render("* ") + truncateTo(line, bodyW-2)
		default:
			line = truncateTo(line, bodyW)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "",
		styleDim.Render(fmt.Sprintf("%d routes  j/k move  g/G top/bottom", len(rows))))

	left := panel("ROUTES", tableW, h, strings.Join(lines, "\n"))
	right := panel("DETAIL", sumW, h, m.routeDetail(sumW-4, h-2, rows, cursor))
	return hstack(left, right, tableW)
}

func (m Model) routeDetail(w, h int, rows []routeRow, sel int) string {
	if w < 16 || h < 4 {
		return ""
	}
	var out []string

	byFam := map[string]int{}
	byIface := map[string]int{}
	defaults := 0
	for _, r := range rows {
		byFam[r.fam]++
		if r.iface != "" {
			byIface[r.iface]++
		}
		if r.isDefault {
			defaults++
		}
	}
	out = append(out,
		styleDim.Render("total"), " "+styleBigNum.Render(fmt.Sprintf("%d", len(rows))),
		styleDim.Render("default"), " "+styleText.Render(fmt.Sprintf("%d", defaults)),
		"",
		styleDim.Render("families"),
		" "+styleText.Render(joinCounts(byFam, "  ")),
		"",
		styleDim.Render("interfaces"),
		" "+styleText.Render(joinCounts(byIface, "  ")),
		"")

	if sel < 0 || sel >= len(rows) {
		out = append(out, styleDim.Render("(no route selected)"))
		return strings.Join(fitLines(out, w, h), "\n")
	}
	r := rows[sel]
	mark := " "
	if r.isDefault {
		mark = styleOK.Render("*")
	}
	out = append(out,
		styleDim.Render("selected"), " "+mark+" "+styleText.Render(r.dest),
		"",
		styleDim.Render("family"), "  "+r.fam,
		styleDim.Render("genmask"), "  "+r.mask,
		styleDim.Render("gateway"), "  "+r.gw,
		styleDim.Render("iface"), "  "+r.iface,
		styleDim.Render("metric"), "  "+fmt.Sprintf("%d", r.metric),
		styleDim.Render("flags"), "  "+r.flags,
	)
	return strings.Join(fitLines(out, w, h), "\n")
}

func sortRoutes(rows []routeRow) {
	less := func(a, b routeRow) bool {
		if a.isDefault != b.isDefault {
			return a.isDefault
		}
		if a.fam != b.fam {
			return a.fam < b.fam
		}
		return a.metric < b.metric
	}
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && less(rows[j], rows[j-1]); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}
