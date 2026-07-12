package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JollyGrin/grove/internal/config"
)

func TestNextLayout(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"horizontal advances", "horizontal", "vertical"},
		{"vertical advances", "vertical", "tiled"},
		{"tiled wraps", "tiled", "horizontal"},
		{"empty advances like the default", "", "vertical"},
		{"unknown advances like the default", "garbage", "vertical"},
	}
	for _, tc := range cases {
		if got := nextLayout(tc.in); got != tc.want {
			t.Errorf("%s: nextLayout(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

// L must cycle in place: same mode, no overlay, a "layout: <name>" flash.
// The tmux calls are best-effort (_ =), so without a live server the key
// still lands on the config default and advances it.
func TestHandleKeyLayoutCycle(t *testing.T) {
	m := New(&config.Config{}, t.TempDir(), "test-52")
	out, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'L'}})
	got := out.(Model)
	if got.mode != m.mode {
		t.Errorf("L changed mode to %d — must cycle in place", got.mode)
	}
	// No tmux server in tests: CockpitLayout("") reads nothing, so cur is
	// the config default (horizontal) and the flash shows its successor.
	if got.flash != "layout: vertical" {
		t.Errorf("flash = %q, want %q", got.flash, "layout: vertical")
	}
}
