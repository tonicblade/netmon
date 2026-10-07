package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"netmon/pkg/types"
)

func (m Model) renderEvents(w, h int) string {
	evs := m.snap.Events
	filter := strings.ToLower(m.filters[tabEvents])
	if filter != "" {
		out := evs[:0:0]
		for _, e := range evs {
			if strings.Contains(strings.ToLower(e.Text+" "+e.Detail), filter) {
				out = append(out, e)
			}
		}
		evs = out
	}

	tableW := w * 2 / 3
	detW := w - tableW
	if detW < 30 {
		detW = 30
		tableW = w - detW
	}
	if tableW < 46 {
		tableW = 46
		detW = w - tableW
	}

	rows := m.eventRows(evs, tableW-4)

	cursor := m.cursor[tabEvents]
	if cursor >= len(rows) {
		cursor = len(rows) - 1
	}
	if cursor < 0 {
		cursor = 0
	}
	avail := h - 5
	if avail < 1 {
		avail = 1
	}
	off := m.offset[tabEvents]
	if cursor < off {
		off = cursor
	}
	if cursor >= off+avail {
		off = cursor - avail + 1
	}
	if off < 0 {
		off = 0
	}

	end := off + avail
	if end > len(rows) {
		end = len(rows)
	}

	lines := []string{styleDim.Render(fmt.Sprintf(
		"%d events  / filter  j/k scroll  g/G top/bottom", len(m.snap.Events)))}
	for i := off; i < end; i++ {
		if i == cursor {
			lines = append(lines, styleSel.Render(truncateTo(rows[i], tableW-4)))
		} else {
			lines = append(lines, truncateTo(rows[i], tableW-4))
		}
	}
	if len(rows) == 0 {
		lines = append(lines, styleDim.Render("no events"))
	}
	left := panel("EVENTS", tableW, h, strings.Join(lines, "\n"))

	var sel types.Event
	hasSel := false
	if cursor < len(evs) {
		sel = evs[cursor]
		hasSel = true
	}
	right := panel("DETAIL", detW, h, m.eventDetail(detW-4, h-2, m.snap.Events, sel, hasSel))
	return hstack(left, right, tableW)
}

func (m Model) eventRows(evs []types.Event, w int) []string {
	if w < 10 {
		return nil
	}
	var rows []string
	for i := len(evs) - 1; i >= 0; i-- {
		e := evs[i]
		ts := e.Time.Format("15:04:05")
		detail := e.Detail
		row := strings.TrimRight(e.Text+"  "+detail, " ")
		rows = append(rows, styleDim.Render(ts)+" "+levelGlyph(int(e.Level))+" "+
			truncateTo(row, maxInt(0, w-13)))
	}

	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows
}

func (m Model) eventDetail(w, h int, all []types.Event, sel types.Event, hasSel bool) string {
	if w < 14 || h < 4 {
		return ""
	}
	var out []string

	counts := map[types.Level]int{}
	for _, e := range all {
		counts[e.Level]++
	}
	tally := ""
	for _, lv := range []types.Level{types.LevelError, types.LevelWarn, types.LevelInfo, types.LevelOK} {
		if n := counts[lv]; n > 0 {
			tally += levelGlyph(int(lv)) + " " + fmt.Sprintf("%d ", n)
		}
	}
	out = append(out,
		styleDim.Render("levels"), " "+strings.TrimSpace(tally),
		"",
		styleDim.Render("error rate"),
		" "+styleText.Render(fmt.Sprintf("%.1f%% of %d events",
			pctOf(counts, types.LevelError, len(all)), len(all))),
		"")

	if !hasSel {
		out = append(out, styleDim.Render("(no event selected)"))
		return strings.Join(fitLines(out, w, h), "\n")
	}

	out = append(out,
		styleDim.Render("time"), "  "+styleText.Render(sel.Time.Format("2006-01-02 15:04:05")),
		styleDim.Render("level"), "  "+levelGlyph(int(sel.Level))+" "+levelName(sel.Level),
		"",
		styleDim.Render("event"),
	)
	for _, line := range wrapText(sel.Text, w) {
		out = append(out, " "+line)
	}
	out = append(out, "")
	if sel.Detail != "" {
		out = append(out, styleDim.Render("detail"))
		for _, line := range wrapText(sel.Detail, w) {
			out = append(out, " "+line)
		}
	}
	return strings.Join(fitLines(out, w, h), "\n")
}

func pctOf(counts map[types.Level]int, lv types.Level, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(counts[lv]) * 100 / float64(total)
}

func levelName(lv types.Level) string {
	switch lv {
	case types.LevelError:
		return "error"
	case types.LevelWarn:
		return "warn"
	case types.LevelInfo:
		return "info"
	default:
		return "ok"
	}
}

func wrapText(s string, w int) []string {
	if w < 8 {
		return []string{truncateTo(s, maxInt(w, 1))}
	}
	lines := strings.Split(lipgloss.NewStyle().Width(w).Render(s), "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines
}
