package tui

func (m Model) renderHelp() string {
	rows := [][2]string{
		{"1-8", "switch tab"},
		{"tab / shift+tab", "next / previous tab"},
		{"j / k  up/down", "move cursor"},
		{"g / G", "top / bottom"},
		{"ctrl+d / ctrl+u  pgup/pgdn", "page down / up"},
		{"/", "filter (interfaces, connections, events)"},
		{"h / l", "graph time range  (-1m .. -1h)"},
		{"x", "graph mode: RX+TX / RX / TX"},
		{"p", "pause / resume collectors"},
		{"enter", "DNS query / trace target"},
		{"r", "re-run DNS query / traceroute"},
		{"n", "DNS record type (A, AAAA, ...)"},
		{"s", "DNS resolver / connection sort"},
		{"b", "benchmark DNS resolvers"},
		{"t", "start traceroute"},
		{"?", "toggle this help"},
		{"q / ctrl+c", "quit"},
	}

	var body string
	for _, r := range rows {
		body += styleHelpKey.Render(padRight(r[0], 26)) + styleHelpDesc.Render(r[1]) + "\n"
	}

	return panel("HELP", minInt(m.width, 72), m.height, body)
}
