package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"netmon/internal/collector"
	"netmon/internal/config"
)

func Run(ctx context.Context, mon *collector.Monitor, cfg *config.Config) error {
	p := tea.NewProgram(New(ctx, mon, cfg), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
