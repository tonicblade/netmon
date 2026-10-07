// Package fmtutil holds small human-formatting helpers shared by CLI and TUI.
package fmtutil

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Bps formats bits-per-second: 184.2 Mbps, 1.2 Kbps, 842 bps.
func Bps(bps float64) string {
	switch {
	case bps >= 1_000_000_000:
		return fmt.Sprintf("%.2f Gbps", bps/1_000_000_000)
	case bps >= 1_000_000:
		return fmt.Sprintf("%.1f Mbps", bps/1_000_000)
	case bps >= 1_000:
		return fmt.Sprintf("%.1f Kbps", bps/1_000)
	default:
		return fmt.Sprintf("%.0f bps", bps)
	}
}

// BpsAxis formats a compact axis label.
func BpsAxis(bps float64) string {
	switch {
	case bps >= 1_000_000_000:
		return fmt.Sprintf("%.1fG", bps/1_000_000_000)
	case bps >= 1_000_000:
		return fmt.Sprintf("%.0fM", bps/1_000_000)
	case bps >= 1_000:
		return fmt.Sprintf("%.0fK", bps/1_000)
	default:
		return fmt.Sprintf("%.0f", bps)
	}
}

// Bytes formats bytes-per-second style magnitudes.
func Bytes(b float64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.2f GiB", b/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MiB", b/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KiB", b/(1<<10))
	default:
		return fmt.Sprintf("%.0f B", b)
	}
}

// Count formats an integer with thousands separators.
func Count(n int) string {
	return Count64(int64(n))
}

func Count64(n int64) string {
	s := fmt.Sprintf("%d", n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

// Ms formats milliseconds compactly.
func Ms(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "-"
	}
	switch {
	case v >= 100:
		return fmt.Sprintf("%.1fms", v)
	case v >= 10:
		return fmt.Sprintf("%.2fms", v)
	default:
		return fmt.Sprintf("%.2fms", v)
	}
}

// Uptime formats a duration like 3d 14h.
func Uptime(d time.Duration) string {
	if d <= 0 {
		return "0m"
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

// Bar renders a proportional block bar of the given width.
func Bar(v, max float64, width int) string {
	if width <= 0 {
		return ""
	}
	if max <= 0 || v <= 0 {
		return strings.Repeat(" ", width)
	}
	frac := v / max
	if frac > 1 {
		frac = 1
	}
	filled := int(frac*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	return strings.Repeat("█", filled) + strings.Repeat(" ", width-filled)
}

// BarChar is like Bar but uses a custom fill rune.
func BarRune(v, max float64, width int, r rune) string {
	if width <= 0 {
		return ""
	}
	if max <= 0 || v <= 0 {
		return strings.Repeat(" ", width)
	}
	frac := math.Min(v/max, 1)
	filled := int(frac*float64(width) + 0.5)
	return strings.Repeat(string(r), filled) + strings.Repeat(" ", width-filled)
}

// Pct formats a percentage.
func Pct(v float64) string {
	return fmt.Sprintf("%.1f%%", v)
}
