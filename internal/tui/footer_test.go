package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/JollyGrin/grove/internal/state"
)

// grove-72: the legend NEVER wraps. At every width from the bare-trio floor
// up, footerLegend is exactly one line, within the pane, and the minimum
// trio O/)/? is present — everything hidden stays documented behind ?.
// (No label contains a capital O, ) or ? — key presence checks are exact.)
func TestFooterLegendOneLineAlways(t *testing.T) {
	for width := 12; width <= 220; width++ {
		for _, hasTasks := range []bool{true, false} {
			line := footerLegend(width, hasTasks)
			if strings.Contains(line, "\n") {
				t.Fatalf("width %d tasks %v: legend contains a newline", width, hasTasks)
			}
			if lw := lipgloss.Width(line); lw > width {
				t.Errorf("width %d tasks %v: legend is %d cells (> width):\n%s",
					width, hasTasks, lw, line)
			}
			for _, key := range []string{"O", ")", "?"} {
				if !strings.Contains(line, key) {
					t.Errorf("width %d tasks %v: minimum key %q missing:\n%s",
						width, hasTasks, key, line)
				}
			}
		}
	}
}

// Hints drop lowest-priority-first as the pane narrows: at every width the
// labels present form a prefix of the keep-priority order — a hint never
// survives while a higher-priority one is gone. (Sweep starts at 41, the
// narrowest width where the trio still carries its labels.)
func TestFooterLegendDropsByPriority(t *testing.T) {
	prio := []string{
		"help", "new chat", "profiled chat", "reply",
		"layout", "costs", "effects", "park", "quit",
	}
	for width := 41; width <= 220; width++ {
		line := footerLegend(width, true)
		gapAt := ""
		for _, label := range prio {
			if strings.Contains(line, label) {
				if gapAt != "" {
					t.Errorf("width %d: %q present but higher-priority %q missing:\n%s",
						width, label, gapAt, line)
				}
			} else if gapAt == "" {
				gapAt = label
			}
		}
	}
}

// At generous widths every hint shows on the single line, in canonical
// display order, with the two group separators.
func TestFooterLegendWideCanonical(t *testing.T) {
	line := footerLegend(200, true)
	if got := strings.Count(line, "│"); got != 2 {
		t.Errorf("width 200: %d group separators, want 2 (row│spawn│global):\n%s", got, line)
	}
	order := []string{
		"reply", "new chat", "profiled chat",
		"help", "layout", "costs", "effects", "park", "quit",
	}
	last := -1
	for _, s := range order {
		i := strings.Index(line, s)
		if i < 0 {
			t.Fatalf("hint %q missing from legend at width 200", s)
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

// Dropping a whole group drops its separator too: at widths where only the
// spawn+global trio survives, exactly one │ remains.
func TestFooterLegendSeparatorsFollowGroups(t *testing.T) {
	line := footerLegend(45, true) // labeled trio (40) fits; +enter (56) doesn't
	if got := strings.Count(line, "│"); got != 1 {
		t.Errorf("width 45: %d group separators, want 1:\n%s", got, line)
	}
	if strings.Contains(line, "reply") {
		t.Errorf("width 45: row hint should have dropped:\n%s", line)
	}
}

// Below the labeled trio the trio sheds labels lowest-priority-first —
// ) first, then O, then ? — and the keys themselves never drop.
func TestFooterLegendTrioShedsLabels(t *testing.T) {
	for _, tc := range []struct {
		width int
		gone  []string
		kept  []string
	}{
		{30, []string{"profiled chat"}, []string{"new chat", "help"}},
		{20, []string{"profiled chat", "new chat"}, []string{"help"}},
		{12, []string{"profiled chat", "new chat", "help"}, nil},
	} {
		line := footerLegend(tc.width, true)
		for _, label := range tc.gone {
			if strings.Contains(line, label) {
				t.Errorf("width %d: label %q should have shed:\n%s", tc.width, label, line)
			}
		}
		for _, label := range tc.kept {
			if !strings.Contains(line, label) {
				t.Errorf("width %d: label %q should survive:\n%s", tc.width, label, line)
			}
		}
	}
}

// Zero tasks drop the row group entirely, at every width — the empty-state
// line already teaches gv grab.
func TestFooterLegendZeroTasks(t *testing.T) {
	for width := 12; width <= 220; width++ {
		if line := footerLegend(width, false); strings.Contains(line, "reply") {
			t.Errorf("width %d zero tasks: row hint present:\n%s", width, line)
		}
	}
	line := footerLegend(120, false)
	for _, want := range []string{"new chat", "profiled chat", "help", "quit"} {
		if !strings.Contains(line, want) {
			t.Errorf("zero tasks at width 120: hint %q missing:\n%s", want, line)
		}
	}
}

// footerHeight is 1 in every list-mode state — confirm footers included —
// and viewFooter emits exactly one line within the pane at every width.
func TestFooterHeightAlwaysOne(t *testing.T) {
	m := New(nil, "", "")
	m.localTasks = []*state.Task{{Ticket: "grove-72", Repo: "grove"}}
	m.assemble()
	for _, width := range []int{12, 40, 60, 80, 120, 200} {
		m.width = width
		if got := m.footerHeight(); got != 1 {
			t.Errorf("width %d: footerHeight = %d, want 1", width, got)
		}
		out := m.viewFooter()
		if got := strings.Count(out, "\n") + 1; got != 1 {
			t.Errorf("width %d: viewFooter emits %d lines, want 1:\n%s", width, got, out)
		}
		if lw := lipgloss.Width(out); lw > width {
			t.Errorf("width %d: footer is %d cells (> width)", width, lw)
		}
	}
	m.width = 60
	m.mode = modeConfirmDone
	m.detail = m.localTasks[0]
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
	// grove-203: park does NOT reap the workspace's detached chats, and the
	// kill takes this dashboard down — so the modal is the last chance to
	// say so. Still one line, still within the width.
	m.parkChats = []string{"grove-chat-unbrewed-1", "grove-chat-unbrewed-2"}
	if m.footerHeight() != 1 {
		t.Errorf("confirm-close-with-chats footerHeight = %d, want 1", m.footerHeight())
	}
	m.width = 220
	out := m.viewFooter()
	for _, want := range []string{"2 chat(s) KEEP RUNNING", "grove-chat-unbrewed-1", "gv park --chats"} {
		if !strings.Contains(out, want) {
			t.Errorf("park modal must name the surviving chats (%q):\n%s", want, out)
		}
	}
	for width := 40; width <= 220; width++ {
		m.width = width
		if lw := lipgloss.Width(m.viewFooter()); lw > width {
			t.Errorf("width %d: confirm-close-with-chats footer is %d cells", width, lw)
		}
	}
}

// The flash is the only surface errors have: at every usable width it stays
// visible on the single line — optional hints yield to make room (never the
// O/)/? trio), and the flash itself truncates only as last resort.
func TestFooterFlashEvictsOptionalHints(t *testing.T) {
	m := New(nil, "", "")
	m.localTasks = []*state.Task{{Ticket: "grove-72", Repo: "grove"}}
	m.assemble()
	m.flash = "error: worktree missing"
	for width := 21; width <= 220; width++ {
		m.width = width
		out := m.viewFooter()
		if strings.Contains(out, "\n") {
			t.Fatalf("width %d: flash wrapped the footer:\n%s", width, out)
		}
		if lw := lipgloss.Width(out); lw > width {
			t.Errorf("width %d: footer with flash is %d cells (> width)", width, lw)
		}
		if !strings.Contains(out, "error") {
			t.Errorf("width %d: flash dropped from footer:\n%s", width, out)
		}
		for _, key := range []string{"O", ")", "?"} {
			if !strings.Contains(out, key) {
				t.Errorf("width %d: minimum key %q evicted by flash:\n%s", width, key, out)
			}
		}
	}

	// At width 58 the full legend (56 cells) leaves no room: the enter hint
	// yields and the flash rides whole. Without the flash, enter stays.
	m.width = 58
	out := m.viewFooter()
	if !strings.Contains(out, "error: worktree missing") {
		t.Errorf("width 58: flash should be fully visible:\n%s", out)
	}
	if strings.Contains(out, "reply") {
		t.Errorf("width 58: enter hint should yield to the flash:\n%s", out)
	}
	m.flash = ""
	if out := m.viewFooter(); !strings.Contains(out, "reply") {
		t.Errorf("width 58 without flash: enter hint should return:\n%s", out)
	}
}

// A long flash never adds a line — it truncates instead, and the height
// budget holds while a flash is up.
func TestFooterFlashKeepsHeight(t *testing.T) {
	m := New(nil, "", "")
	m.localTasks = []*state.Task{{Ticket: "grove-72", Repo: "grove"}}
	m.assemble()
	for _, width := range []int{40, 60, 200} {
		m.width = width
		m.flash = "⬢ grove-72 merged — the canopy grows and this flash is quite long"
		out := m.viewFooter()
		if got := strings.Count(out, "\n") + 1; got != m.footerHeight() {
			t.Errorf("width %d with flash: %d lines, footerHeight says %d", width, got, m.footerHeight())
		}
		if lw := lipgloss.Width(out); lw > width {
			t.Errorf("width %d with flash: footer is %d cells", width, lw)
		}
	}
}

// The whole View respects m.height — with the footer now a constant single
// line, ACTIVITY/scene absorb the freed rows without overflowing.
func TestViewHeightBudget(t *testing.T) {
	m := New(nil, "", "")
	m.localTasks = []*state.Task{{Ticket: "grove-72", Repo: "grove", Created: time.Now()}}
	m.assemble()
	for i := 0; i < 50; i++ {
		m.events = append(m.events, state.Event{
			Type: state.EvAnswered, Ticket: "grove-72", Time: time.Now(),
		})
	}
	for _, tc := range []struct{ w, h int }{
		{200, 30}, {120, 30}, {60, 30}, {60, 24}, {40, 30},
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
