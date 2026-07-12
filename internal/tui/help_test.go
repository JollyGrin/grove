package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	tea "github.com/charmbracelet/bubbletea"
)

// ? opens the cheat sheet from the list; ? or esc closes it back to the list.
func TestHelpOpenClose(t *testing.T) {
	m := New(nil, "", "")
	m.width, m.height = 100, 40

	next, _ := m.handleKey(runeKey("?"))
	m = next.(Model)
	if m.mode != modeHelp {
		t.Fatalf("?: mode = %d, want modeHelp", m.mode)
	}

	m, _ = mustModel(m.handleKey(runeKey("?")))
	if m.mode != modeList {
		t.Fatalf("? again: mode = %d, want modeList", m.mode)
	}

	m, _ = mustModel(m.handleKey(runeKey("?")))
	m, _ = mustModel(m.handleKey(tea.KeyMsg{Type: tea.KeyEsc}))
	if m.mode != modeList {
		t.Fatalf("esc: mode = %d, want modeList", m.mode)
	}
}

// Every footer key is documented in the overlay, and the overlay names its
// three semantic groups.
func TestHelpCoversEveryFooterKey(t *testing.T) {
	documented := map[string]bool{}
	for _, s := range helpSections {
		for _, e := range s.entries {
			documented[e.key] = true
		}
	}
	for _, g := range footerGroups(true) {
		for _, h := range g {
			if !documented[h.key] {
				t.Errorf("footer key %q has no help entry", h.key)
			}
		}
	}

	m := New(nil, "", "")
	m.width, m.height = 120, 40
	m.mode = modeHelp
	view := m.View()
	for _, want := range []string{"ROW", "SPAWN", "GLOBAL", "silences re-pings"} {
		if !strings.Contains(view, want) {
			t.Errorf("help overlay missing %q", want)
		}
	}
}

// The overlay clamps every line to the pane width — same no-soft-wrap rule
// as the footer itself.
func TestHelpLinesFitWidth(t *testing.T) {
	m := New(nil, "", "")
	m.mode = modeHelp
	for _, width := range []int{60, 100} {
		m.width, m.height = width, 40
		for i, l := range strings.Split(m.viewHelp(), "\n") {
			if lw := lipgloss.Width(l); lw > width {
				t.Errorf("width %d: help line %d is %d cells:\n%s", width, i, lw, l)
			}
		}
	}
}
