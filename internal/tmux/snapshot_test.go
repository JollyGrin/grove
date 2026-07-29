// grove-149: the dash's 1s refresh reads the whole session through ONE
// list-windows + ONE list-panes (SessionSnapshot) instead of 3-4 window
// listings + a display-message per task per second. These tests pin (a) the
// exec count — the whole point of the ticket — and (b) that the batched
// matcher keeps the grove-47 glyph tolerance and the grove-116 sibling
// prefix rules the per-call helpers guarantee.
package tmux

import (
	"reflect"
	"testing"
)

// A three-window board: cockpit (active), a glyphed grove-1 worker, and its
// prefix-extending sibling grove-10 — the grove-116 reproduction shape.
const (
	snapWindowsOut = "@1\t1\tcockpit\n@2\t0\trepo · grove-1 ✳\n@3\t0\trepo · grove-10"
	snapPanesOut   = "@1\t%0\t0\t50\tgv\n" +
		"@2\t%1\t0\t48\tzsh\n" +
		"@2\t%2\t1\t48\tclaude\n" +
		"@3\t%3\t0\t42\t2.1.197"
)

func snapFixture() *SessionSnapshot {
	return ParseSessionSnapshot(snapWindowsOut, snapPanesOut)
}

func TestSnapshotWindowID(t *testing.T) {
	// After grove-1's window died only the prefix-extending sibling lives.
	onlySibling := ParseSessionSnapshot("@3\t0\trepo · grove-10", "")

	cases := []struct {
		name   string
		snap   *SessionSnapshot
		base   string
		wantID string
		wantOK bool
	}{
		{"glyphed own window resolves", snapFixture(), "repo · grove-1", "@2", true},
		{"sibling resolves to itself", snapFixture(), "repo · grove-10", "@3", true},
		{"dead window never resolves to prefix sibling", onlySibling, "repo · grove-1", "", false},
		{"glyph is required to be space-separated", onlySibling, "repo · grove-1", "", false},
		{"exact glyphless match", snapFixture(), "cockpit", "@1", true},
		{"empty base matches nothing", snapFixture(), "", "", false},
		{"nil snapshot", nil, "repo · grove-1", "", false},
	}
	for _, c := range cases {
		id, ok := c.snap.WindowID(c.base)
		if id != c.wantID || ok != c.wantOK {
			t.Errorf("%s: WindowID(%q) = (%q, %v), want (%q, %v)",
				c.name, c.base, id, ok, c.wantID, c.wantOK)
		}
		if got := c.snap.WindowExists(c.base); got != c.wantOK {
			t.Errorf("%s: WindowExists(%q) = %v, want %v", c.name, c.base, got, c.wantOK)
		}
	}
}

func TestSnapshotResolveWindowName(t *testing.T) {
	snap := snapFixture()
	if got := snap.ResolveWindowName("repo · grove-1"); got != "repo · grove-1 ✳" {
		t.Errorf("glyphed resolve = %q, want the live glyphed name", got)
	}
	if got := snap.ResolveWindowName("repo · grove-10"); got != "repo · grove-10" {
		t.Errorf("sibling resolve = %q, want itself", got)
	}
	if got := snap.ResolveWindowName("repo · grove-99"); got != "repo · grove-99" {
		t.Errorf("missing window must fall back to base, got %q", got)
	}
	var nilSnap *SessionSnapshot
	if got := nilSnap.ResolveWindowName("base"); got != "base" {
		t.Errorf("nil snapshot must fall back to base, got %q", got)
	}
}

func TestSnapshotActiveWindow(t *testing.T) {
	if got := snapFixture().ActiveWindow(); got != "cockpit" {
		t.Errorf("ActiveWindow = %q, want cockpit", got)
	}
	noActive := ParseSessionSnapshot("@1\t0\tcockpit", "")
	if got := noActive.ActiveWindow(); got != "" {
		t.Errorf("no active window should yield \"\", got %q", got)
	}
	var nilSnap *SessionSnapshot
	if got := nilSnap.ActiveWindow(); got != "" {
		t.Errorf("nil snapshot ActiveWindow = %q, want \"\"", got)
	}
}

func TestSnapshotClaudePane(t *testing.T) {
	snap := snapFixture()

	// grove-1's window: claude in pane 1 beats the worktree shell in pane 0.
	pane, height, ok := snap.ClaudePane("repo · grove-1")
	if !ok || pane != "%2" || height != 48 {
		t.Errorf("grove-1 ClaudePane = (%q, %d, %v), want (%%2, 48, true)", pane, height, ok)
	}
	// Lost split: a version-string title (Claude's process title) alone.
	pane, height, ok = snap.ClaudePane("repo · grove-10")
	if !ok || pane != "%3" || height != 42 {
		t.Errorf("grove-10 ClaudePane = (%q, %d, %v), want (%%3, 42, true)", pane, height, ok)
	}
	if _, _, ok := snap.ClaudePane("repo · grove-99"); ok {
		t.Error("missing window must not resolve a pane")
	}
	windowNoPanes := ParseSessionSnapshot("@9\t0\tempty", "")
	if _, _, ok := windowNoPanes.ClaudePane("empty"); ok {
		t.Error("window with no listed panes must not resolve a pane")
	}
	var nilSnap *SessionSnapshot
	if _, _, ok := nilSnap.ClaudePane("repo · grove-1"); ok {
		t.Error("nil snapshot must not resolve a pane")
	}
}

func TestParseSessionSnapshotTolerance(t *testing.T) {
	snap := ParseSessionSnapshot("garbage\n\n@1\t1\tcockpit", "noise\n@1\t%0\tNaN\t50\tgv\n@1\t%1\t0\tx\tgv")
	if got := snap.ActiveWindow(); got != "cockpit" {
		t.Errorf("garbage window lines must be skipped, ActiveWindow = %q", got)
	}
	// The NaN-index pane is dropped; the bad-height pane survives at height 0.
	pane, height, ok := snap.ClaudePane("cockpit")
	if !ok || pane != "%1" || height != 0 {
		t.Errorf("tolerant pane parse = (%q, %d, %v), want (%%1, 0, true)", pane, height, ok)
	}
}

// The exec-count seam: a snapshot of a whole session is EXACTLY two tmux
// invocations, both `=`-anchored session-scoped reads (grove-78/grove-99 —
// list-panes -s takes a target-session, so Exact applies).
func TestSnapshotSessionTwoExecs(t *testing.T) {
	old := execTmux
	defer func() { execTmux = old }()
	var calls [][]string
	execTmux = func(args ...string) (string, error) {
		calls = append(calls, args)
		switch args[0] {
		case "list-windows":
			return snapWindowsOut, nil
		case "list-panes":
			return snapPanesOut, nil
		}
		t.Fatalf("unexpected tmux exec: %v", args)
		return "", nil
	}

	snap, err := SnapshotSession("grove-test")
	if err != nil {
		t.Fatalf("SnapshotSession: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("SnapshotSession spawned %d tmux processes, want exactly 2: %v", len(calls), calls)
	}
	wantWindows := []string{"list-windows", "-t", "=grove-test", "-F", "#{window_id}\t#{window_active}\t#{window_name}"}
	if !reflect.DeepEqual(calls[0], wantWindows) {
		t.Errorf("first exec = %v, want %v", calls[0], wantWindows)
	}
	wantPanes := []string{"list-panes", "-s", "-t", "=grove-test", "-F", "#{window_id}\t#{pane_id}\t#{pane_index}\t#{pane_height}\t#{pane_current_command}"}
	if !reflect.DeepEqual(calls[1], wantPanes) {
		t.Errorf("second exec = %v, want %v", calls[1], wantPanes)
	}

	// The snapshot answers every per-task question from those two reads.
	if id, ok := snap.WindowID("repo · grove-1"); !ok || id != "@2" {
		t.Errorf("snapshot WindowID = (%q, %v), want (@2, true)", id, ok)
	}
	if pane, _, ok := snap.ClaudePane("repo · grove-10"); !ok || pane != "%3" {
		t.Errorf("snapshot ClaudePane = (%q, %v), want (%%3, true)", pane, ok)
	}
}

// CapturePaneBottomKnown is one exec — the per-call display-message height
// query is gone; the height comes from the snapshot.
func TestCapturePaneBottomKnownOneExec(t *testing.T) {
	old := execTmux
	defer func() { execTmux = old }()
	var calls [][]string
	execTmux = func(args ...string) (string, error) {
		calls = append(calls, args)
		return "pane content", nil
	}

	out, err := CapturePaneBottomKnown("%2", 48, 30)
	if err != nil || out != "pane content" {
		t.Fatalf("CapturePaneBottomKnown = (%q, %v)", out, err)
	}
	if len(calls) != 1 {
		t.Fatalf("capture spawned %d tmux processes, want exactly 1: %v", len(calls), calls)
	}
	want := []string{"capture-pane", "-p", "-J", "-S", "18", "-t", "%2"}
	if !reflect.DeepEqual(calls[0], want) {
		t.Errorf("exec = %v, want %v (start offset = height 48 - lines 30)", calls[0], want)
	}

	// A short pane captures from the top; a zero/unknown height never execs.
	calls = nil
	if _, err := CapturePaneBottomKnown("%2", 10, 30); err != nil {
		t.Fatal(err)
	}
	if calls[0][4] != "0" {
		t.Errorf("short pane start offset = %q, want 0", calls[0][4])
	}
	calls = nil
	if out, err := CapturePaneBottomKnown("%2", 0, 30); err != nil || out != "" {
		t.Errorf("zero height = (%q, %v), want (\"\", nil)", out, err)
	}
	if len(calls) != 0 {
		t.Errorf("zero height spawned %d tmux processes, want 0", len(calls))
	}
}
