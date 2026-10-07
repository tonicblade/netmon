package tui

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"netmon/internal/collector"
	"netmon/internal/config"
)

// TestLiveAllTabs drives a real monitor through Update/View on every tab to
// catch panics only live data triggers (growing series, new events, …).
func TestLiveAllTabs(t *testing.T) {
	cfg := config.Default()
	mon, err := collector.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	mon.Start(ctx)
	defer func() { cancel(); mon.Stop() }()

	m := New(ctx, mon, cfg)
	m.width, m.height = 120, 30

	keysBy := []tea.Msg{
		tea.KeyMsg{Type: tea.KeyDown},
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}},
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}},
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}},
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}},
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}},
	}

	for tab := 0; tab < tabCount; tab++ {
		m.tab = tab
		for i := 0; i < 3; i++ {
			m.snap = mon.Snapshot()
			nm, _ := m.Update(keysBy[(tab+i)%len(keysBy)])
			var ok bool
			m, ok = nm.(Model)
			if !ok {
				t.Fatal("model type changed")
			}
			if got := m.View(); got == "" {
				t.Fatalf("empty view on tab %d", tab)
			}
			time.Sleep(30 * time.Millisecond)
		}
	}

	// small terminal too
	m.width, m.height = 40, 12
	for tab := 0; tab < tabCount; tab++ {
		m.tab = tab
		m.snap = mon.Snapshot()
		if got := m.View(); got == "" {
			t.Fatalf("empty small view on tab %d", tab)
		}
	}
}
