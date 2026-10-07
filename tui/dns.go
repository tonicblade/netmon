package tui

import (
	"fmt"
	"sort"
	"strings"

	"netmon/internal/fmtutil"
	"netmon/pkg/types"
)

func (m Model) renderDNS(w, h int) string {
	var lines []string

	switch m.mode {
	case inputDNS:
		lines = append(lines, styleTitle.Render(" QUERY ")+" "+m.input.View())
	default:
		q := m.dnsRes.Query
		if q == "" {
			q = "(enter to query)"
		}
		lines = append(lines, styleDim.Render("query")+": "+styleText.Render(q)+
			styleDim.Render("  type")+": "+styleText.Render(dnsTypes[m.dnsTypeIdx])+
			styleDim.Render("  server")+": "+styleText.Render(dnsServerLabel(m.dnsServer, m.cfg.DNS.Resolvers)))
	}
	lines = append(lines, styleDim.Render("enter query  n type  s server  b benchmark  r re-run"),
		"")

	if m.dnsLoading {
		lines = append(lines, styleInfo.Render("querying..."))
	}
	if m.dnsBenchLoading {
		lines = append(lines, styleInfo.Render("benchmarking resolvers..."))
	}

	res := m.dnsRes
	if res.Query != "" && !m.dnsLoading {
		status := styleOK.Render("ok")
		if res.Err != "" {
			status = styleError.Render(res.Err)
		}
		lines = append(lines, fmt.Sprintf("%s %s -> %s  in %s",
			styleText.Render(res.Query), styleDim.Render(res.Type), status, fmtutil.Ms(res.RTT)))
		if len(res.Records) > 0 {
			lines = append(lines, "")
			lines = append(lines, styleHeaderRow.Render(
				padRight("TTL", 8)+padRight("TYPE", 7)+"VALUE"))
			for _, rec := range res.Records {
				lines = append(lines, styleDim.Render(padLeft(fmt.Sprintf("%d", rec.TTL), 7))+
					padRight(rec.Type, 7)+truncateTo(rec.Value, maxInt(0, w-15)))
			}
		}
	}

	if m.dnsBenchLoaded && len(m.dnsBench) > 0 {
		lines = append(lines, "", styleHeaderRow.Render(
			padRight("RESOLVER", 26)+padLeft("RTT", 9)+"  STATUS"))
		rows := append([]types.DNSBenchmarkRow{}, m.dnsBench...)
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].OK != rows[j].OK {
				return rows[i].OK
			}
			return rows[i].RTT < rows[j].RTT
		})
		for _, r := range rows {
			st := styleError.Render("error: " + r.Error)
			if r.OK {
				st = styleOK.Render("ok")
			}
			lines = append(lines, padRight(truncateTo(r.Server, 25), 26)+
				padLeft(fmtutil.Ms(r.RTT), 9)+"  "+st)
		}
	}

	return panel("DNS", w, h, strings.Join(lines, "\n"))
}

func dnsServerLabel(cur string, resolvers []string) string {
	if cur == "" {
		return "system"
	}
	return cur
}
