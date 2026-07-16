package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// grove-53 cause (b): the WindowSizeMsg handler stored the new dims but
// returned nil, so a shrink or a SIGWINCH replay on tmux re-attach left
// stale cells from the old geometry. It must now return a command
// (tea.ClearScreen) so every resize repaints from a clean slate.
func TestResizeReturnsClearCmd(t *testing.T) {
	m := New(nil, "", "")
	next, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	nm := next.(Model)
	if nm.width != 80 || nm.height != 24 {
		t.Fatalf("dims not stored: got %dx%d, want 80x24", nm.width, nm.height)
	}
	if cmd == nil {
		t.Fatal("resize returned a nil cmd — no clear-on-resize, stale cells survive a shrink/re-attach")
	}
}

// grove-53: the two bare-line footers #61 did not touch — viewCostsFooter
// and viewProfilePick's foot — must clamp to m.width so they never hard-wrap
// the alt-screen renderer (which desyncs row bookkeeping → duplicated bars,
// ghost panels). viewHeader and the main viewFooter are already clamped.
func TestBareFootersNeverWrap(t *testing.T) {
	for _, w := range []int{60, 30} {
		m := twoProfiles()
		m.width = w
		next, _ := m.handleKey(runeKey(")"))
		pm := next.(Model)

		foot := lastLine(pm.viewProfilePick())
		if lw := lipgloss.Width(foot); lw > w {
			t.Errorf("profile-pick footer is %d cells on a %d-cell pane:\n%q", lw, w, foot)
		}

		cf := m.viewCostsFooter()
		if strings.Contains(cf, "\n") {
			t.Errorf("costs footer has an embedded newline at width %d:\n%q", w, cf)
		}
		if lw := lipgloss.Width(cf); lw > w {
			t.Errorf("costs footer is %d cells on a %d-cell pane:\n%q", lw, w, cf)
		}
	}
}

func lastLine(s string) string {
	lines := strings.Split(s, "\n")
	return lines[len(lines)-1]
}
