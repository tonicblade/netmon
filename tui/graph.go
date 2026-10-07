package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"netmon/pkg/types"
)

// GraphSeries is one plotted series.
type GraphSeries struct {
	Data []types.DataPoint
	Line lipgloss.Color
	Fill lipgloss.Color // empty = no area fill
	Max  bool           // downsample with peak instead of average
}

// GraphOptions configures the renderer.
type GraphOptions struct {
	Width   int // total width including the y-axis gutter
	Height  int // total height including axis and label rows
	YFormat func(float64) string
	Max     float64 // force y-max (0 = auto)
	XLeft   string  // e.g. "-5m"
	XRight  string  // e.g. "now"
}

type cell struct {
	ch    rune
	color lipgloss.Color
	set   bool
}

// RenderGraph draws line/area charts with box-drawing corners, a y-axis
// gutter and an x-axis, in the spirit of btop.
func RenderGraph(opts GraphOptions, series ...GraphSeries) string {
	if opts.Width < 6 || opts.Height < 3 {
		return ""
	}
	if opts.YFormat == nil {
		opts.YFormat = func(v float64) string { return fmt.Sprintf("%.0f", v) }
	}

	hasXLabels := opts.XLeft != "" || opts.XRight != ""
	axisRows := 1
	if hasXLabels {
		axisRows = 2
	}
	plotH := opts.Height - axisRows
	if plotH < 2 {
		return ""
	}

	// ---- y-scale and axis gutter ----
	rawMax := 0.0
	for _, s := range series {
		for _, p := range s.Data {
			if p.Value > rawMax {
				rawMax = p.Value
			}
		}
	}
	yMax := opts.Max
	if yMax <= 0 {
		yMax = niceCeil(rawMax)
	}
	if yMax <= 0 {
		yMax = 1
	}
	gutterWidth := func() int {
		g := 1
		for _, p := range []float64{yMax, yMax / 2, 0} {
			if l := len(opts.YFormat(p)); l > g {
				g = l
			}
		}
		return g
	}
	plotW := opts.Width - gutterWidth() - 1 // -1 for the '┤'
	if plotW < 4 {
		return ""
	}

	// ---- downsample to the final column count (two passes so the y-axis
	// labels from real, downsampled data can widen the gutter again) ----
	seriesVals := make([][]float64, len(series))
	for i, s := range series {
		seriesVals[i] = downsample(s.Data, plotW, s.Max)
	}
	if yMax <= 0 || opts.Max <= 0 {
		overall := 0.0
		for _, v := range seriesVals {
			for _, x := range v {
				if x > overall {
					overall = x
				}
			}
		}
		if opts.Max <= 0 {
			yMax = niceCeil(overall)
			if yMax <= 0 {
				yMax = 1
			}
		}
	}
	newW := opts.Width - gutterWidth() - 1
	if newW != plotW {
		plotW = newW
		if plotW < 4 {
			return ""
		}
		for i, s := range series {
			seriesVals[i] = downsample(s.Data, plotW, s.Max)
		}
	}
	gutter := gutterWidth()
	// never let series columns drift from the grid width
	for i := range seriesVals {
		seriesVals[i] = fitBins(seriesVals[i], plotW)
	}

	yOf := func(v float64) int {
		if v < 0 {
			v = 0
		}
		frac := v / yMax
		if frac > 1 {
			frac = 1
		}
		r := plotH - 1 - int(frac*float64(plotH-1)+0.5)
		if r < 0 {
			r = 0
		}
		if r > plotH-1 {
			r = plotH - 1
		}
		return r
	}

	grid := make([][]cell, plotH)
	for r := range grid {
		grid[r] = make([]cell, plotW)
	}

	// ---- area fills (drawn first, series order; lines win on top) ----
	for i, s := range series {
		if s.Fill == "" {
			continue
		}
		if len(seriesVals[i]) != plotW {
			continue
		}
		for x, v := range seriesVals[i] {
			for r := yOf(v); r < plotH; r++ {
				if !grid[r][x].set {
					grid[r][x] = cell{ch: '█', color: s.Fill, set: true}
				}
			}
		}
	}

	// ---- lines ----
	for i, s := range series {
		vals := seriesVals[i]
		if len(vals) != plotW || s.Line == "" {
			continue
		}
		ys := make([]int, len(vals))
		for x, v := range vals {
			ys[x] = yOf(v)
		}
		put := func(x, r int, ch rune) {
			if x >= 0 && x < plotW && r >= 0 && r < plotH {
				grid[r][x] = cell{ch: ch, color: s.Line, set: true}
			}
		}
		for x := 1; x < plotW; x++ {
			ya, yb := ys[x-1], ys[x]
			switch {
			case yb == ya:
				put(x-1, ya, '─')
			case yb > ya: // value dropped: ╮ ... ╰
				put(x-1, ya, '╮')
				for r := ya + 1; r < yb; r++ {
					put(x-1, r, '│')
				}
				put(x-1, yb, '╰')
			default: // value rose: ╯ ... ╭
				put(x-1, ya, '╯')
				for r := yb + 1; r < ya; r++ {
					put(x-1, r, '│')
				}
				put(x-1, yb, '╭')
			}
		}
		put(plotW-1, ys[plotW-1], '─')
		put(0, ys[0], '╴')
	}

	// ---- assemble rows ----
	labels := map[int]string{}
	labels[0] = opts.YFormat(yMax)
	if plotH > 2 {
		labels[plotH/2] = opts.YFormat(yMax / 2)
	}
	labels[plotH-1] = opts.YFormat(0)

	var b strings.Builder
	for r := 0; r < plotH; r++ {
		got := labels[r]
		b.WriteString(strings.Repeat(" ", gutter-len(got)))
		b.WriteString(got)
		b.WriteString("┤")
		b.WriteString(renderRow(grid[r]))
		b.WriteString("\n")
	}

	// axis
	b.WriteString(strings.Repeat(" ", gutter))
	b.WriteString("└")
	b.WriteString(strings.Repeat("─", plotW))
	b.WriteString("\n")

	if hasXLabels {
		b.WriteString(strings.Repeat(" ", gutter+1))
		line := opts.XLeft
		right := opts.XRight
		if len(line)+len(right)+1 <= plotW {
			pad := plotW - len(line) - len(right)
			b.WriteString(line)
			b.WriteString(strings.Repeat(" ", pad))
			b.WriteString(right)
		} else {
			b.WriteString(line)
		}
	}
	return b.String()
}

// renderRow run-length encodes a row for styled output.
func renderRow(cells []cell) string {
	var b strings.Builder
	i := 0
	for i < len(cells) {
		j := i
		for j < len(cells) && cells[j].ch == cells[i].ch && cells[j].color == cells[i].color {
			j++
		}
		run := make([]rune, 0, j-i)
		for k := i; k < j; k++ {
			ch := cells[k].ch
			if ch == 0 {
				ch = ' '
			}
			run = append(run, ch)
		}
		if cells[i].color != "" && cells[i].ch != 0 {
			b.WriteString(lipgloss.NewStyle().Foreground(cells[i].color).Render(string(run)))
		} else {
			b.WriteString(string(run))
		}
		i = j
	}
	return b.String()
}

// downsample reduces n points into `cols` buckets using avg or peak.
func downsample(pts []types.DataPoint, cols int, useMax bool) []float64 {
	if cols <= 0 || len(pts) == 0 {
		return nil
	}
	if len(pts) <= cols {
		out := make([]float64, cols)
		for i := 0; i < cols; i++ {
			if i < len(pts) {
				out[i] = pts[i].Value
			} else {
				out[i] = pts[len(pts)-1].Value // extend flat to "now"
			}
		}
		return out
	}
	out := make([]float64, cols)
	step := float64(len(pts)) / float64(cols)
	for c := 0; c < cols; c++ {
		start := int(float64(c) * step)
		end := int(float64(c+1) * step)
		if end <= start {
			end = start + 1
		}
		if end > len(pts) {
			end = len(pts)
		}
		sum := 0.0
		mx := 0.0
		n := 0
		for _, p := range pts[start:end] {
			sum += p.Value
			if p.Value > mx {
				mx = p.Value
			}
			n++
		}
		if n == 0 {
			out[c] = 0
			continue
		}
		if useMax {
			out[c] = mx
		} else {
			out[c] = sum / float64(n)
		}
	}
	return out
}

// fitBins trims or extends a downsample result to exactly n columns so the
// data width can never drift from the grid width.
func fitBins(vals []float64, n int) []float64 {
	if n <= 0 || len(vals) == 0 {
		return nil
	}
	if len(vals) == n {
		return vals
	}
	out := make([]float64, n)
	last := vals[len(vals)-1]
	if len(vals) > n {
		copy(out, vals[:n])
	} else {
		copy(out, vals)
		for i := len(vals); i < n; i++ {
			out[i] = last
		}
	}
	return out
}

// niceCeil rounds a value up to a friendly axis maximum.
func niceCeil(v float64) float64 {
	if v <= 0 {
		return 1
	}
	mag := math.Pow(10, math.Floor(math.Log10(v)))
	norm := v / mag
	var nice float64
	switch {
	case norm <= 1:
		nice = 1
	case norm <= 1.5:
		nice = 1.5
	case norm <= 2:
		nice = 2
	case norm <= 2.5:
		nice = 2.5
	case norm <= 5:
		nice = 5
	default:
		nice = 10
	}
	return nice * mag
}

// rangeLabel formats the x-axis left label for a graph window.
func rangeLabel(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("-%dh", int(d.Hours()))
	case d >= time.Minute:
		return fmt.Sprintf("-%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("-%ds", int(d.Seconds()))
	}
}
