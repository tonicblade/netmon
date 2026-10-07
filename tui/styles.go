package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Tokyo-night inspired palette.
var (
	cText    = lipgloss.Color("#C0CAF5")
	cDim     = lipgloss.Color("#565F89")
	cBorder  = lipgloss.Color("#3B4261")
	cAccent  = lipgloss.Color("#7DCFFF")
	cBlue    = lipgloss.Color("#7AA2F7")
	cGreen   = lipgloss.Color("#9ECE6A")
	cYellow  = lipgloss.Color("#E0AF68")
	cOrange  = lipgloss.Color("#FF9E64")
	cRed     = lipgloss.Color("#F7768E")
	cMagenta = lipgloss.Color("#BB9AF7")
	cBg      = lipgloss.Color("#1A1B26")
	cSelBg   = lipgloss.Color("#283457")

	cRXLine  = lipgloss.Color("#7DCFFF")
	cRXFill  = lipgloss.Color("#28547A")
	cTXLine  = lipgloss.Color("#BB9AF7")
	cTXFill  = lipgloss.Color("#4C3A78")
	cLat     = lipgloss.Color("#9ECE6A")
	cLatFill = lipgloss.Color("#31563A")
)

var (
	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(cBg).
			Background(cAccent).
			Padding(0, 1)

	styleHeaderKey = lipgloss.NewStyle().Foreground(cDim).Bold(true)
	styleHeaderVal = lipgloss.NewStyle().Foreground(cText)

	stylePanelTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(cAccent)

	styleDim  = lipgloss.NewStyle().Foreground(cDim)
	styleText = lipgloss.NewStyle().Foreground(cText)

	styleSel = lipgloss.NewStyle().
			Foreground(cText).
			Background(cSelBg).
			Bold(true)

	styleHeaderRow = lipgloss.NewStyle().
			Foreground(cDim).
			Bold(true)

	styleOK    = lipgloss.NewStyle().Foreground(cGreen)
	styleWarn  = lipgloss.NewStyle().Foreground(cYellow)
	styleError = lipgloss.NewStyle().Foreground(cRed)
	styleInfo  = lipgloss.NewStyle().Foreground(cBlue)

	styleBigNum = lipgloss.NewStyle().Bold(true).Foreground(cText)
	styleBigLbl = lipgloss.NewStyle().Foreground(cDim)

	styleGood = lipgloss.NewStyle().Foreground(cGreen)
	styleBad  = lipgloss.NewStyle().Foreground(cRed)

	styleHealthBar = lipgloss.NewStyle().Foreground(cGreen)
	styleTabActive = lipgloss.NewStyle().
			Bold(true).
			Foreground(cBg).
			Background(cAccent).
			Padding(0, 1)
	styleTabInactive = lipgloss.NewStyle().
				Foreground(cDim).
				Padding(0, 1)

	styleHelpKey  = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	styleHelpDesc = lipgloss.NewStyle().Foreground(cText)
)

// panel renders a bordered panel with a title embedded in the top border.
// w and h are the outer dimensions including the border; the body is laid out
// at w-4 columns and h-2 rows (1 border + 1 space padding per side).
func panel(title string, w, h int, body string) string {
	if w < 5 {
		w = 5
	}
	if h < 3 {
		h = 3
	}
	innerW := w - 2 // between the two vertical borders
	rows := h - 2   // between top and bottom borders
	contentW := innerW - 2
	if contentW < 0 {
		contentW = 0
	}

	border := lipgloss.NewStyle().Foreground(cBorder)
	var b strings.Builder

	// top border with centered title
	t := " " + title + " "
	if lipgloss.Width(t) > innerW-1 {
		t = truncateTo(t, innerW-2) + " "
	}
	halves := innerW - lipgloss.Width(t)
	left := halves / 2
	if left < 1 {
		left = 1
	}
	right := innerW - lipgloss.Width(t) - left
	if right < 1 {
		right = 1
	}
	b.WriteString(border.Render("╭" + strings.Repeat("─", left)))
	b.WriteString(lipgloss.NewStyle().Foreground(cAccent).Bold(true).Render(t))
	b.WriteString(border.Render(strings.Repeat("─", right) + "╮"))

	bodyLines := strings.Split(body, "\n")
	for r := 0; r < rows; r++ {
		line := ""
		if r < len(bodyLines) {
			line = bodyLines[r]
		}
		line = truncateTo(line, contentW)
		line += strings.Repeat(" ", contentW-lipgloss.Width(line))
		b.WriteString("\n")
		b.WriteString(border.Render("│"))
		b.WriteString(" ")
		b.WriteString(line)
		b.WriteString(" ")
		b.WriteString(border.Render("│"))
	}

	b.WriteString("\n")
	b.WriteString(border.Render("╰" + strings.Repeat("─", innerW) + "╯"))
	return b.String()
}

// panelNoBorder renders a titled block without a border (for simple sections).
func sectionTitle(title string) string {
	return stylePanelTitle.Render(title)
}

func levelStyle(level int) lipgloss.Style {
	switch level {
	case 1: // ok
		return styleOK
	case 2: // warn
		return styleWarn
	case 3: // error
		return styleError
	}
	return styleInfo
}

// levelGlyph returns a short level tag padded to 2 display columns, so
// "OK", "W ", "E " and "I " all leave the same gap for the following text.
func levelGlyph(level int) string {
	var g string
	switch level {
	case 1:
		g = styleOK.Render("OK")
	case 2:
		g = styleWarn.Render("W ")
	case 3:
		g = styleError.Render("E ")
	default:
		g = styleInfo.Render("I ")
	}
	return g + strings.Repeat(" ", 2-lipgloss.Width(g))
}
