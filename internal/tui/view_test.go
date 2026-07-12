package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/JollyGrin/grove/internal/state"
)

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

	// At width 60 the footer legend wraps deliberately (grove-60): several
	// real lines, each within the pane, matching footerHeight exactly.
	foot := strings.Split(m.viewFooter(), "\n")
	if len(foot) < 2 {
		t.Errorf("width 60: footer should wrap onto multiple lines, got:\n%s", foot[0])
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
