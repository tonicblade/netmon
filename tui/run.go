// Package tui is the Bubble Tea interface: a btop-style live dashboard.
package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"netmon/internal/collector"
	"netmon/internal/config"
)

// Run starts the full-screen TUI and blocks until the user quits.
func Run(ctx context.Context, mon *collector.Monitor, cfg *config.Config) error {
	p := tea.NewProgram(New(ctx, mon, cfg), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
