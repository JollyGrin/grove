package tmux

import "testing"

func TestPickPane(t *testing.T) {
	cases := []struct {
		name  string
		panes []PaneInfo
		want  int
	}{
		{"claude in pane 1 of 2", []PaneInfo{{0, "zsh"}, {1, "claude"}}, 1},
		{"claude alone in pane 0", []PaneInfo{{0, "claude"}}, 0},
		{"single shell pane", []PaneInfo{{0, "zsh"}}, 0},
		{"no claude, two panes → highest", []PaneInfo{{0, "zsh"}, {1, "bash"}}, 1},
		// The DEV-4761-class regression: a node dev-server in pane 0 must
		// lose to claude in pane 1 (highest-index claude-ish wins).
		{"node dev-server pane 0, claude pane 1", []PaneInfo{{0, "node"}, {1, "claude"}}, 1},
		{"two claude-ish panes → highest", []PaneInfo{{0, "node"}, {1, "node"}}, 1},
		{"node counts as claude-ish", []PaneInfo{{0, "zsh"}, {1, "node"}, {2, "zsh"}}, 1},
		{"bun counts as claude-ish", []PaneInfo{{0, "bun"}, {1, "zsh"}}, 0},
		// Claude Code sets its process title to its version string
		// (observed live: pane_current_command = "2.1.197").
		{"version-string title counts as claude-ish", []PaneInfo{{0, "2.1.197"}}, 0},
		{"version title in pane 0, shell pane 1", []PaneInfo{{0, "2.1.197"}, {1, "zsh"}}, 0},
		{"empty list", nil, 0},
		// Renumbered panes (pane 0 closed): indexes need not start at 0.
		{"non-zero single pane", []PaneInfo{{1, "claude"}}, 1},
	}
	for _, c := range cases {
		if got := pickPane(c.panes); got != c.want {
			t.Errorf("%s: pickPane = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestParsePaneList(t *testing.T) {
	out := "0 zsh\n1 claude\n"
	panes := parsePaneList(out)
	if len(panes) != 2 || panes[0].Index != 0 || panes[0].Command != "zsh" ||
		panes[1].Index != 1 || panes[1].Command != "claude" {
		t.Errorf("parsePaneList = %+v", panes)
	}
	if got := parsePaneList("garbage\n\n"); len(got) != 0 {
		t.Errorf("garbage should parse to empty, got %+v", got)
	}
}

func TestClaudePaneNonexistentWindow(t *testing.T) {
	// Missing window → fall back to pane 1 (the historical layout), so
	// callers behave exactly as before this helper existed.
	if got := ClaudePane("pr-test-nonexistent-session-xyz", "nope"); got != 1 {
		t.Errorf("ClaudePane fallback = %d, want 1", got)
	}
}
