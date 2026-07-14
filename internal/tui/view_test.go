package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/JollyGrin/grove/internal/state"
)

// grove-63 S0: viewHeader had no final width clamp — a long workspace label
// plus the gauge and counts could exceed m.width and hard-wrap the
// alt-screen on a narrow pane, even with no forest strip riding along.
func TestViewHeaderClampsWidth(t *testing.T) {
	m := New(nil, "", "a-very-long-workspace-label-that-will-not-fit-a-narrow-pane")
	m.width = 40
	m.fx = fxOff
	out := m.viewHeader()
	if w := lipgloss.Width(out); w > m.width {
		t.Errorf("header is %d cells on a %d-cell pane:\n%s", w, m.width, out)
	}
}

// grove-79: rowBudgets can budget ACTIVITY more rows than the feed holds —
// when the leftover height is too small for the scene floor, the scene yields
// and ACTIVITY inherits the whole leftover regardless of feed length. The
// unclamped items[:avail] slice panicked the cockpit's FIRST frame (events
// load async, so the feed is always empty then) on any pane whose leftover
// landed below the floor — the e2e tape size, and any brand-new fleet. The
// dashboard must render at every small size, empty feed or short feed,
// without panicking and without exceeding the pane width.
func TestViewRendersNarrowPanesWithSparseFeed(t *testing.T) {
	feeds := map[string][]state.Event{
		"empty": nil,
		"short": {{Type: state.EvTaskCreated, Ticket: "grove-1"}},
	}
	for name, events := range feeds {
		for _, fx := range []fxLevel{fxOff, fxCalm, fxFull} {
			for h := 5; h <= 30; h++ {
				m := New(nil, "", "")
				m.width, m.height = 40, h
				m.fx = fx
				m.events = events
				out := m.View() // must not panic
				for i, ln := range strings.Split(out, "\n") {
					if lw := lipgloss.Width(ln); lw > m.width {
						t.Errorf("feed=%s fx=%d h=%d: line %d is %d cells on a %d-cell pane",
							name, fx, h, i, lw, m.width)
					}
				}
			}
		}
	}
}

// The AGENTS table shows a dimmed task-title hint after AGE when the pane is
// wide, and drops it (with no broken layout — still one line per row) when the
// pane is narrow.
func TestViewAgentsTitleHint(t *testing.T) {
	m := New(nil, "", "")
	m.tasks = []*state.Task{{
		Ticket: "grove-18",
		Title:  "show a tiny task title after AGE",
		Repo:   "grove",
	}}

	// Wide: the title (or its truncated head) rides after AGE.
	m.width = 120
	wide := m.viewAgents()
	if !strings.Contains(wide, "show a tiny task title") {
		t.Errorf("wide pane should show the task title hint, got:\n%s", wide)
	}

	// Narrow: no room after AGE, so the title is omitted entirely.
	m.width = 60
	narrow := m.viewAgents()
	if strings.Contains(narrow, "task title") {
		t.Errorf("narrow pane should omit the task title hint, got:\n%s", narrow)
	}

	// At width 60 the legend stays a single line (grove-72): low-priority
	// hints drop instead of wrapping, matching footerHeight exactly.
	foot := strings.Split(m.viewFooter(), "\n")
	if len(foot) != 1 {
		t.Errorf("width 60: footer should be one line, got %d:\n%s",
			len(foot), strings.Join(foot, "\n"))
	}
	if len(foot) != m.footerHeight() {
		t.Errorf("width 60: footer is %d lines, footerHeight says %d", len(foot), m.footerHeight())
	}
	for i, ln := range foot {
		if lw := lipgloss.Width(ln); lw > 60 {
			t.Errorf("width 60: footer line %d is %d cells:\n%s", i, lw, ln)
		}
	}

	// Either way: never more than one line per row (header + one task = 2).
	for _, out := range []string{wide, narrow} {
		var rows int
		for _, ln := range strings.Split(out, "\n") {
			if strings.Contains(ln, "grove-18") || strings.Contains(ln, "TICKET") {
				rows++
			}
		}
		if rows != 2 {
			t.Errorf("expected 2 content rows (header + task), got %d in:\n%s", rows, out)
		}
	}
}
