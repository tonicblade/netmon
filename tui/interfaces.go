package tui

import (
	"fmt"
	"strings"

	"netmon/internal/fmtutil"
	"netmon/pkg/types"
)

func (m Model) renderInterfaces(w, h int) string {
	if len(m.snap.Interfaces) == 0 {
		return styleDim.Render("collecting interfaces...")
	}

	listW := w * 45 / 100
	if listW < 28 {
		listW = 28
	}
	if listW > 58 {
		listW = 58
	}
	detailW := w - listW
	if detailW < 32 {
		detailW = 32
		listW = w - detailW
	}

	if m.cursor[tabInterfaces] >= len(m.snap.Interfaces) {
		m.cursor[tabInterfaces] = len(m.snap.Interfaces) - 1
	}
	if m.cursor[tabInterfaces] < 0 {
		m.cursor[tabInterfaces] = 0
	}
	sel := m.snap.Interfaces[m.cursor[tabInterfaces]]

	list := m.ifaceList(listW-4, h-2, m.filters[tabInterfaces])
	detail := m.ifaceDetail(detailW-4, h-2, sel)

	left := panel("INTERFACES", listW, h, list)
	right := panel("DETAIL", detailW, h, detail)
	return hstack(left, right, listW)
}

func (m Model) ifaceList(w, h int, filter string) string {
	if w < 8 || h < 1 {
		return ""
	}
	filter = strings.ToLower(filter)
	lines := []string{styleHeaderRow.Render(padLeft("RX", 9) + padLeft("TX", 9) + "  " + padRight("NAME", 12))}
	for i, is := range m.snap.Interfaces {
		if filter != "" && !strings.Contains(strings.ToLower(is.Info.Name), filter) {
			continue
		}
		nameStyle := styleText
		name := is.Info.Name
		if !is.Info.IsUp {
			name += " [down]"
			nameStyle = styleDim
		}
		line := padLeft(fmtutil.Bps(is.RxBps), 9) + padLeft(fmtutil.Bps(is.TxBps), 9) +
			"  " + nameStyle.Render(padRight(truncateTo(name, w-6), w-4))
		if i == m.cursor[tabInterfaces] {
			line = styleSel.Render(truncateTo(line, w))
		} else {
			line = truncateTo(line, w)
		}
		lines = append(lines, line)
	}
	return joinScroll(lines, h, 0)
}

func (m Model) ifaceDetail(w, h int, is types.InterfaceSnapshot) string {
	if w < 10 || h < 4 {
		return ""
	}
	inf := is.Info

	status := styleOK.Render("up")
	if !inf.IsUp {
		status = styleError.Render("down")
	}
	head := styleBigNum.Render(inf.Name) + "  " + status +
		"  " + styleDim.Render(fmt.Sprintf("%s  mtu %d  index %d", inf.Kind, inf.MTU, inf.Index))

	addrs := styleDim.Render("addr4 ") + styleText.Render(strings.Join(inf.Addr4, ", "))
	if len(inf.Addr6) > 0 {
		addrs += "\n" + styleDim.Render("addr6 ") + styleText.Render(strings.Join(inf.Addr6, ", "))
	}
	extra := styleDim.Render("mac ") + styleText.Render(inf.MAC)
	if inf.Speed > 0 {
		extra += "  " + styleDim.Render("speed ") + styleText.Render(fmtutil.Bps(float64(inf.Speed)))
	}

	graphH := h - 9
	if graphH < 4 {
		graphH = 4
	}
	rng := graphRanges[m.graphRangeIdx]
	var g string
	if rx, ok := m.snap.IfaceRx[inf.Name]; ok {
		tx := m.snap.IfaceTx[inf.Name]
		opts := GraphOptions{
			Width:   w,
			Height:  graphH,
			YFormat: func(v float64) string { return fmtutil.BpsAxis(v) },
			XLeft:   rangeLabel(rng),
			XRight:  "now",
		}
		switch m.graphModeIdx {
		case 1:
			g = RenderGraph(opts, GraphSeries{Data: rx.Window(rng), Line: cRXLine, Fill: cRXFill})
		case 2:
			g = RenderGraph(opts, GraphSeries{Data: tx.Window(rng), Line: cTXLine, Fill: cTXFill, Max: true})
		default:
			g = RenderGraph(opts,
				GraphSeries{Data: rx.Window(rng), Line: cRXLine},
				GraphSeries{Data: tx.Window(rng), Line: cTXLine},
			)
		}
	}

	counters := fmt.Sprintf(
		"rx %s (%s pps)   tx %s (%s pps)\nerrors %d/%d   drops %d/%d   total rx %s / tx %s",
		fmtutil.Bps(is.RxBps), fmtutil.Count(int(is.RxPps)),
		fmtutil.Bps(is.TxBps), fmtutil.Count(int(is.TxPps)),
		is.Counters.RxErrors, is.Counters.TxErrors,
		is.Counters.RxDrops, is.Counters.TxDrops,
		fmtutil.Bytes(float64(is.Counters.RxBytes)), fmtutil.Bytes(float64(is.Counters.TxBytes)),
	)

	body := vstack(
		head, addrs, extra, "",
		fmt.Sprintf("%s %s   %s %s   %s %s",
			styleDim.Render("rx"), styleBigNum.Render(fmtutil.Bps(is.RxBps)),
			styleDim.Render("tx"), styleBigNum.Render(fmtutil.Bps(is.TxBps)),
			styleDim.Render("pps"), styleText.Render(fmt.Sprintf("%d/%d", int(is.RxPps), int(is.TxPps))),
		),
		"",
		g,
		"",
		styleDim.Render(counters),
	)
	return fitWidth(body, w)
}

func joinScroll(lines []string, h, offset int) string {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(lines) {
		offset = 0
	}
	end := offset + h
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[offset:end], "\n")
}
