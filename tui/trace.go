package tui

import (
	"fmt"
	"strings"

	"netmon/internal/fmtutil"
)

func (m Model) renderTrace(w, h int) string {
	var lines []string

	switch m.mode {
	case inputTrace:
		lines = append(lines, styleTitle.Render(" TARGET ")+" "+m.input.View())
	default:
		lines = append(lines, styleDim.Render("target")+": "+styleText.Render(m.traceTarget))
	}

	state := styleDim.Render("idle — press t or enter to trace")
	if m.traceRunning {
		state = styleInfo.Render("tracing...")
	} else if m.traceReached {
		state = styleOK.Render(fmt.Sprintf("destination reached (%d hops)", len(m.traceHops)))
	} else if m.traceErr != "" {
		state = styleError.Render(m.traceErr)
	}
	lines = append(lines, state,
		styleDim.Render("enter/t target  r re-run"),
		"")

	if len(m.traceHops) > 0 {
		head := padLeft("#", 3) + padRight("HOST", 34) + padRight("ADDRESS", 20) +
			padLeft("LOSS%", 7) + padLeft("LAST", 9) + padLeft("AVG", 9) +
			padLeft("MIN", 9) + padLeft("MAX", 9)
		lines = append(lines, styleHeaderRow.Render(head))

		tableH := h - len(lines) - 1
		if tableH < 1 {
			tableH = 1
		}
		off := m.offset[tabTrace]
		if off > len(m.traceHops)-tableH {
			off = len(m.traceHops) - tableH
		}
		if off < 0 {
			off = 0
		}
		if off < 0 {
			off = 0
		}
		for i := off; i < len(m.traceHops) && len(lines) <= h-2; i++ {
			hp := m.traceHops[i]
			host := hp.Host
			if host == "" {
				host = "-"
			}
			addr := hp.Addr
			if addr == "" {
				addr = "*"
			}
			lossCol := styleText
			switch {
			case hp.LossPct >= 30:
				lossCol = styleError
			case hp.LossPct > 0:
				lossCol = styleWarn
			}
			glyph := "  "
			if addr == "*" {
				glyph = styleDim.Render("* ")
			}
			line := glyph + padLeft(fmt.Sprintf("%d", hp.Num), 3) +
				padRight(truncateTo(host, 33), 34) +
				padRight(truncateTo(addr, 19), 20) +
				lossCol.Render(padLeft(fmt.Sprintf("%.0f%%", hp.LossPct), 7)) +
				padLeft(fmtutil.Ms(hp.Last), 9) +
				padLeft(fmtutil.Ms(hp.Avg), 9) +
				padLeft(fmtutil.Ms(hp.Min), 9) +
				padLeft(fmtutil.Ms(hp.Max), 9)
			lines = append(lines, truncateTo(line, w))
		}
	}

	return panel("TRACEROUTE", w, h, strings.Join(lines, "\n"))
}
