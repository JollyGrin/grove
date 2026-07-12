package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/JollyGrin/grove/internal/state"
)

// Every legend line stays within width at every tested size, with and
// without tasks — the terminal must never soft-wrap the footer (grove-60).
func TestFooterLinesFitWidth(t *testing.T) {
	for _, width := range []int{40, 60, 80, 120, 200} {
		for _, hasTasks := range []bool{true, false} {
			lines := footerLines(width, hasTasks)
			if len(lines) == 0 {
				t.Fatalf("width %d tasks %v: no lines", width, hasTasks)
			}
			for i, l := range lines {
				if strings.Contains(l, "\n") {
					t.Errorf("width %d tasks %v: line %d contains a raw newline", width, hasTasks, i)
				}
				if lw := lipgloss.Width(l); lw > width {
					t.Errorf("width %d tasks %v: line %d is %d cells (> %d):\n%s",
						width, hasTasks, i, lw, width, l)
				}
			}
			// No hint goes missing to make it fit — wrap, don't drop.
			joined := strings.Join(lines, "\n")
			for _, g := range footerGroups(hasTasks) {
				for _, h := range g {
					if !strings.Contains(joined, h.label) {
						t.Errorf("width %d tasks %v: hint %q dropped", width, hasTasks, h.label)
					}
				}
			}
		}
	}
}

// At generous widths the legend is a single line with group separators —
// nothing visibly changes except the │ breaks between groups.
func TestFooterLinesWideSingleLine(t *testing.T) {
	lines := footerLines(200, true)
	if len(lines) != 1 {
		t.Fatalf("width 200: %d lines, want 1:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if got := strings.Count(lines[0], "│"); got != 2 {
		t.Errorf("width 200: %d group separators, want 2 (row│spawn│global)", got)
	}
}

// A narrow pane wraps at hint boundaries onto several fully visible lines.
func TestFooterLinesNarrowWraps(t *testing.T) {
	lines := footerLines(60, true)
	if len(lines) < 2 {
		t.Fatalf("width 60 with tasks should wrap, got 1 line:\n%s", lines[0])
	}
}

// grove-69: slimming the legend to rowHints={enter}, globalHints minus
// gardens must fit one line at typical widths and never wrap past two
// lines down to a fairly narrow pane.
func TestFooterLinesSlimmedFitsOneLine(t *testing.T) {
	for _, width := range []int{110, 120, 160, 200} {
		lines := footerLines(width, true)
		if len(lines) != 1 {
			t.Errorf("width %d: %d lines, want 1 (slimmed legend should fit one line at >=110 cols):\n%s",
				width, len(lines), strings.Join(lines, "\n"))
		}
	}
}

func TestFooterLinesSlimmedNeverExceedsTwoLines(t *testing.T) {
	for width := 60; width <= 200; width++ {
		if lines := footerLines(width, true); len(lines) > 2 {
			t.Errorf("width %d: %d lines, want <=2:\n%s", width, len(lines), strings.Join(lines, "\n"))
		}
	}
}

// Zero tasks drop the row group entirely — the empty-state line already
// teaches gv grab — and the shorter legend stops wrapping at usual widths.
func TestFooterLinesZeroTasks(t *testing.T) {
	joined := strings.Join(footerLines(120, false), "\n")
	for _, dead := range []string{"reply", "attach", "reviewing", "nudge"} {
		if strings.Contains(joined, dead) {
			t.Errorf("zero tasks: row hint %q should be absent:\n%s", dead, joined)
		}
	}
	for _, want := range []string{"new chat", "help", "quit"} {
		if !strings.Contains(joined, want) {
			t.Errorf("zero tasks: hint %q missing:\n%s", want, joined)
		}
	}
	if lines := footerLines(120, false); len(lines) != 1 {
		t.Errorf("zero tasks at width 120: %d lines, want 1", len(lines))
	}
}

// Group order: row acts first, then spawn, then global — and ? leads the
// global group so help is the first thing a lost operator sees.
func TestFooterGroupOrder(t *testing.T) {
	joined := strings.Join(footerLines(200, true), "\n")
	order := []string{"reply", "new chat", "profiled chat", "help", "quit"}
	last := -1
	for _, s := range order {
		i := strings.Index(joined, s)
		if i < 0 {
			t.Fatalf("hint %q missing from legend", s)
		}
		if i < last {
			t.Errorf("hint %q out of order (index %d < %d)", s, i, last)
		}
		last = i
	}
	if globalHints[0].key != "?" {
		t.Errorf("first global hint = %q, want ?", globalHints[0].key)
	}
}

// Absurdly narrow panes degrade to a single truncated line rather than eat
// the screen.
func TestFooterLinesDegradeNarrow(t *testing.T) {
	for _, width := range []int{5, 12, 19} {
		lines := footerLines(width, true)
		if len(lines) != 1 {
			t.Fatalf("width %d: %d lines, want 1 (degraded)", width, len(lines))
		}
		if lw := lipgloss.Width(lines[0]); lw > width {
			t.Errorf("width %d: degraded line is %d cells", width, lw)
		}
	}
}

// footerHeight mirrors footerLines exactly, and confirm modes stay single
// line — viewActivity's budget depends on the match.
func TestFooterHeight(t *testing.T) {
	m := New(nil, "", "")
	m.tasks = []*state.Task{{Ticket: "grove-60", Repo: "grove"}}
	for _, width := range []int{40, 60, 80, 120, 200} {
		m.width = width
		if got, want := m.footerHeight(), len(footerLines(width, true)); got != want {
			t.Errorf("width %d: footerHeight = %d, want %d", width, got, want)
		}
		if got := strings.Count(m.viewFooter(), "\n") + 1; got != m.footerHeight() {
			t.Errorf("width %d: viewFooter emits %d lines, footerHeight says %d",
				width, got, m.footerHeight())
		}
	}
	m.width = 60
	m.mode = modeConfirmDone
	m.detail = m.tasks[0]
	if m.footerHeight() != 1 {
		t.Errorf("confirm-done footerHeight = %d, want 1", m.footerHeight())
	}
	if lw := lipgloss.Width(m.viewFooter()); lw > 60 {
		t.Errorf("confirm-done footer is %d cells, want ≤ 60", lw)
	}
	m.mode = modeConfirmClose
	if m.footerHeight() != 1 {
		t.Errorf("confirm-close footerHeight = %d, want 1", m.footerHeight())
	}
	if lw := lipgloss.Width(m.viewFooter()); lw > 60 {
		t.Errorf("confirm-close footer is %d cells, want ≤ 60", lw)
	}
}

// The flash is the only surface errors have: it must stay visible across
// the wrap-threshold widths where the LAST legend line runs nearly full —
// it rides the roomiest line instead of being silently dropped.
func TestFooterFlashVisibleAtThresholdWidths(t *testing.T) {
	m := New(nil, "", "")
	m.tasks = []*state.Task{{Ticket: "grove-60", Repo: "grove"}}
	m.flash = "error: worktree missing"
	// Recomputed for the slimmed grove-69 legend (rowHints down to one
	// entry, globalHints minus gardens): the legend now collapses to two
	// lines at width 56 and to one line at width 107 (was 179–184 with the
	// old, wider legend). 58–65: multi-line wrap with a nearly full last
	// line. 107–112: the single-line legend exactly fills the pane — hints
	// must yield.
	var widths []int
	for w := 58; w <= 65; w++ {
		widths = append(widths, w)
	}
	for w := 107; w <= 112; w++ {
		widths = append(widths, w)
	}
	for _, width := range widths {
		m.width = width
		out := m.viewFooter()
		if !strings.Contains(out, "error") {
			t.Errorf("width %d: flash dropped from footer:\n%s", width, out)
		}
		lines := strings.Split(out, "\n")
		if len(lines) != m.footerHeight() {
			t.Errorf("width %d: flash changed the line count (%d vs footerHeight %d)",
				width, len(lines), m.footerHeight())
		}
		for i, l := range lines {
			if lw := lipgloss.Width(l); lw > width {
				t.Errorf("width %d: line %d with flash is %d cells", width, i, lw)
			}
		}
	}
}

// A flash rides an existing legend line — it never adds one, so the height
// budget holds while a flash is up.
func TestFooterFlashKeepsHeight(t *testing.T) {
	m := New(nil, "", "")
	m.tasks = []*state.Task{{Ticket: "grove-60", Repo: "grove"}}
	for _, width := range []int{40, 60, 200} {
		m.width = width
		m.flash = "⬢ grove-60 merged — the canopy grows and this flash is quite long"
		out := m.viewFooter()
		if got := strings.Count(out, "\n") + 1; got != m.footerHeight() {
			t.Errorf("width %d with flash: %d lines, footerHeight says %d", width, got, m.footerHeight())
		}
		for i, l := range strings.Split(out, "\n") {
			if lw := lipgloss.Width(l); lw > width {
				t.Errorf("width %d with flash: line %d is %d cells", width, i, lw)
			}
		}
	}
}

// The whole View respects m.height — ACTIVITY yields rows to a wrapped
// footer instead of letting the render scroll the alt-screen.
func TestViewHeightBudget(t *testing.T) {
	m := New(nil, "", "")
	m.tasks = []*state.Task{{Ticket: "grove-60", Repo: "grove", Created: time.Now()}}
	for i := 0; i < 50; i++ {
		m.events = append(m.events, state.Event{
			Type: state.EvAnswered, Ticket: "grove-60", Time: time.Now(),
		})
	}
	for _, tc := range []struct{ w, h int }{
		{200, 30}, {120, 30}, {60, 30}, {60, 24}, {40, 30},
		// Short panes: the old 'avail < 3' floor overflowed exactly here —
		// a 4-line footer at 60 cols left no room for a forced 3-row feed.
		{60, 15}, {60, 16}, {60, 17}, {60, 18}, {40, 15},
	} {
		m.width, m.height = tc.w, tc.h
		out := m.View()
		if got := strings.Count(out, "\n") + 1; got > tc.h {
			t.Errorf("%dx%d: View renders %d lines (> height)", tc.w, tc.h, got)
		}
	}

	// The ? overlay obeys the same budget — a stock 80x24 terminal (and
	// shorter) must not overflow the alt-screen on '?'.
	m.mode = modeHelp
	for _, tc := range []struct{ w, h int }{{80, 24}, {120, 15}, {60, 40}} {
		m.width, m.height = tc.w, tc.h
		out := m.View()
		if got := strings.Count(out, "\n") + 1; got > tc.h {
			t.Errorf("help %dx%d: View renders %d lines (> height)", tc.w, tc.h, got)
		}
	}
}
