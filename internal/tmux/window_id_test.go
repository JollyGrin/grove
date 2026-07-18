// grove-116: the window side of "session:window" targets must never be
// name-built. tmux prefix-matches window names, and worker names are
// prefixes of each other ("repo · grove-1" vs "repo · grove-10"), so a
// name target could kill/rename/paste into a SIBLING task's window once
// the task's own window was gone (or read as ambiguous while both lived).
// Everything now resolves through WindowID. Integration tests run on a
// scratch server (scratchServer, exact_test.go) per tmux-discipline §1.
package tmux

import (
	"strings"
	"testing"
)

func TestMatchWindowID(t *testing.T) {
	// Live board: grove-1's window is glyphed, grove-10's is not.
	both := "@1\trepo · grove-1 ✳\n@2\trepo · grove-10"
	// After grove-1's window died only the prefix-extending sibling lives.
	onlySibling := "@2\trepo · grove-10"

	cases := []struct {
		name   string
		out    string
		base   string
		wantID string
		wantOK bool
	}{
		{"glyphed own window resolves", both, "repo · grove-1", "@1", true},
		{"sibling resolves to itself", both, "repo · grove-10", "@2", true},
		{"dead window never resolves to prefix sibling", onlySibling, "repo · grove-1", "", false},
		{"exact glyphless match", "@7\trepo · grove-1", "repo · grove-1", "@7", true},
		{"glyph is required to be space-separated", "@2\trepo · grove-10", "repo · grove-1", "", false},
		{"empty base matches nothing", both, "", "", false},
		{"empty listing", "", "repo · grove-1", "", false},
		{"garbage line ignored", "garbage\n@3\trepo · grove-1", "repo · grove-1", "@3", true},
	}
	for _, c := range cases {
		id, ok := matchWindowID(c.out, c.base)
		if id != c.wantID || ok != c.wantOK {
			t.Errorf("%s: matchWindowID(%q, %q) = (%q, %v), want (%q, %v)",
				c.name, c.out, c.base, id, ok, c.wantID, c.wantOK)
		}
	}
}

func TestPickPaneID(t *testing.T) {
	cases := []struct {
		name   string
		out    string
		wantID string
		wantOK bool
	}{
		{"claude in pane 1", "%0 0 zsh\n%1 1 claude", "%1", true},
		{"lost split: claude alone", "%5 0 claude", "%5", true},
		{"version-string title counts", "%3 0 2.1.197", "%3", true},
		{"no claude → highest index", "%0 0 zsh\n%1 1 bash", "%1", true},
		{"empty", "", "", false},
		{"garbage", "garbage\n\n", "", false},
	}
	for _, c := range cases {
		id, ok := pickPaneID(c.out)
		if id != c.wantID || ok != c.wantOK {
			t.Errorf("%s: pickPaneID(%q) = (%q, %v), want (%q, %v)",
				c.name, c.out, id, ok, c.wantID, c.wantOK)
		}
	}
}

// siblingFixture builds the grove-116 reproduction on a scratch server:
// session with windows "repo · grove-1" (glyphed via RenameWorker, like a
// live worker) and "repo · grove-10". Returns the session name.
func siblingFixture(t *testing.T) string {
	t.Helper()
	scratchServer(t)
	const session = "gv116"
	dir := t.TempDir()
	if _, err := run("new-session", "-d", "-s", session, "-n", "repo · grove-1", "-c", dir); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	if _, err := run("new-window", "-d", "-t", Exact(session), "-n", "repo · grove-10", "-c", dir); err != nil {
		t.Fatalf("new-window: %v", err)
	}
	// Pin both names — tmux's automatic-rename would clobber them with the
	// shell's process name and the fixture would silently stop reproducing.
	for _, w := range []string{"repo · grove-1", "repo · grove-10"} {
		id, ok := WindowID(session, w)
		if !ok {
			t.Fatalf("fixture window %q did not resolve", w)
		}
		if err := DisableAutoRename(id); err != nil {
			t.Fatalf("automatic-rename off %q: %v", w, err)
		}
	}
	if err := RenameWorker(session, "repo · grove-1", "✳"); err != nil {
		t.Fatalf("glyph grove-1: %v", err)
	}
	return session
}

// windowNames lists the session's current window names.
func windowNames(t *testing.T, session string) []string {
	t.Helper()
	out, err := run("list-windows", "-t", Exact(session), "-F", "#{window_name}")
	if err != nil {
		t.Fatalf("list-windows: %v", err)
	}
	return strings.Split(out, "\n")
}

func TestWindowLiveSiblingPrefix(t *testing.T) {
	session := siblingFixture(t)

	// Failure 3 (audit): live glyphed worker + prefix-extending sibling —
	// the old name target read as ambiguous and the worker as dead.
	if !WindowLive(session, "repo · grove-1") {
		t.Fatal("WindowLive(grove-1) = false with a live glyphed window — sibling ambiguity regressed")
	}
	if !WindowLive(session, "repo · grove-10") {
		t.Fatal("WindowLive(grove-10) = false")
	}

	if err := KillWindow(session, "repo · grove-1"); err != nil {
		t.Fatalf("KillWindow(grove-1): %v", err)
	}
	names := windowNames(t, session)
	if len(names) != 1 || names[0] != "repo · grove-10" {
		t.Fatalf("after killing grove-1, windows = %q, want only grove-10", names)
	}

	// Failure 1 (audit): with grove-1's window gone the old target silently
	// resolved to grove-10 — WindowLive lied true and KillWindow killed the
	// sibling's live worker.
	if WindowLive(session, "repo · grove-1") {
		t.Fatal("WindowLive(grove-1) = true after its window died — resolved via sibling grove-10")
	}
	if err := KillWindow(session, "repo · grove-1"); err == nil {
		t.Fatal("KillWindow(grove-1) succeeded after its window died — killed sibling grove-10")
	}
	names = windowNames(t, session)
	if len(names) != 1 || names[0] != "repo · grove-10" {
		t.Fatalf("grove-10 did not survive: windows = %q", names)
	}
}

func TestRenameWorkerRefusesSibling(t *testing.T) {
	session := siblingFixture(t)

	// Re-glyphing the live (already-glyphed) worker still works.
	if err := RenameWorker(session, "repo · grove-1", "⏸"); err != nil {
		t.Fatalf("re-glyph live worker: %v", err)
	}
	if !WindowLive(session, "repo · grove-1") {
		t.Fatal("worker unresolvable after re-glyph")
	}

	if err := KillWindow(session, "repo · grove-1"); err != nil {
		t.Fatalf("KillWindow: %v", err)
	}
	// Failure 4 (audit): a late-firing grove-1 hook would re-badge
	// grove-10's window as "repo · grove-1 <glyph>".
	if err := RenameWorker(session, "repo · grove-1", "✳"); err == nil {
		t.Fatal("RenameWorker succeeded after the window died — renamed sibling grove-10")
	}
	names := windowNames(t, session)
	if len(names) != 1 || names[0] != "repo · grove-10" {
		t.Fatalf("sibling window renamed: windows = %q", names)
	}
}

func TestClaudePaneTargetRefusesSibling(t *testing.T) {
	session := siblingFixture(t)

	pane, err := ClaudePaneTarget(session, "repo · grove-1")
	if err != nil {
		t.Fatalf("ClaudePaneTarget(grove-1): %v", err)
	}
	owner, err := run("display-message", "-p", "-t", pane, "#{window_name}")
	if err != nil {
		t.Fatalf("display-message: %v", err)
	}
	if !strings.HasPrefix(owner, "repo · grove-1 ") {
		t.Fatalf("resolved pane lives in window %q, want grove-1's glyphed window", owner)
	}

	if err := KillWindow(session, "repo · grove-1"); err != nil {
		t.Fatalf("KillWindow: %v", err)
	}
	// Failure 2 (audit): with grove-1's window gone the old name target
	// resolved grove-10's pane and pasted the answer into the wrong agent.
	if _, err := ClaudePaneTarget(session, "repo · grove-1"); err == nil {
		t.Fatal("ClaudePaneTarget succeeded after the window died — resolved sibling grove-10's pane")
	}
	// ClaudePane keeps its historical fallback: 1, never the sibling's panes.
	if got := ClaudePane(session, "repo · grove-1"); got != 1 {
		t.Fatalf("ClaudePane fallback = %d, want 1", got)
	}
}
