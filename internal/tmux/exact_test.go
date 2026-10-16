// grove-78: session targets must be exact-matched. tmux resolves a bare
// -t target against window names across ALL sessions, so a session
// literally named "grove" (the legacy cockpit — and any dogfooding
// workspace) collides with every "grove · <ticket>" worker window.
// The integration test below runs against a scratch tmux server, fully
// isolated per tmux-discipline §1: TMUX_TMPDIR points at a temp dir and
// $TMUX is unset (it would override TMUX_TMPDIR and hit the real server).
package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestExact(t *testing.T) {
	if got := Exact("grove"); got != "=grove" {
		t.Errorf("Exact(grove) = %q, want =grove", got)
	}
}

func TestExactActive(t *testing.T) {
	if got := ExactActive("grove"); got != "=grove:" {
		t.Errorf("ExactActive(grove) = %q, want =grove:", got)
	}
}

// scratchServer isolates every tmux call in this test process onto a
// throwaway server. Cleanups run LIFO: the scoped kill-server registered
// here fires while the scratch env is still in place, then t.Setenv's own
// cleanup restores the real environment.
func scratchServer(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	// Not t.TempDir(): its test-name-derived path overflows sun_path (unix
	// sockets cap at ~104 bytes on darwin) — tmux then can't create its
	// socket at <dir>/tmux-<uid>/default.
	sockDir, err := os.MkdirTemp("", "gv78")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	t.Setenv("TMUX_TMPDIR", sockDir)
	// $TMUX beats TMUX_TMPDIR — unset it or a test run from inside a tmux
	// pane targets the operator's REAL server (the grove-7 crash class).
	t.Setenv("TMUX", "")
	os.Unsetenv("TMUX")
	t.Cleanup(func() {
		// Scoped: TMUX_TMPDIR still points at the scratch dir here.
		_, _ = run("kill-server")
	})
}

func TestCreateWindowSessionExactCollision(t *testing.T) {
	scratchServer(t)
	dir := t.TempDir()

	// The live repro (grove-78): session "grove" with only window 0, plus a
	// workspace session whose worker window is named "grove · grove-75-x".
	// Bare `new-window -t grove` matched that WINDOW (in the other session!)
	// and died with "create window failed: index 1 in use".
	if _, err := run("new-session", "-d", "-s", "grove", "-c", dir); err != nil {
		t.Fatalf("new-session grove: %v", err)
	}
	if _, err := run("new-session", "-d", "-s", "grove-ws", "-n", "cockpit", "-c", dir); err != nil {
		t.Fatalf("new-session grove-ws: %v", err)
	}
	if _, err := run("new-window", "-d", "-t", "=grove-ws", "-n", "grove · grove-75-x", "-c", dir); err != nil {
		t.Fatalf("new-window worker: %v", err)
	}

	if err := CreateWindow("grove", "gv-r · task-1", dir); err != nil {
		t.Fatalf("CreateWindow into session grove collided with a worker window: %v", err)
	}
	groveWins, err := run("list-windows", "-t", "=grove", "-F", "#{window_name}")
	if err != nil {
		t.Fatalf("list-windows grove: %v", err)
	}
	if !strings.Contains(groveWins, "gv-r · task-1") {
		t.Errorf("window landed outside session grove; grove has %q", groveWins)
	}
	wsWins, err := run("list-windows", "-t", "=grove-ws", "-F", "#{window_name}")
	if err != nil {
		t.Fatalf("list-windows grove-ws: %v", err)
	}
	if strings.Contains(wsWins, "gv-r · task-1") {
		t.Errorf("window leaked into session grove-ws: %q", wsWins)
	}

	// Exact anchoring must NOT break window-name prefix tolerance: a glyphed
	// worker window ("base <glyph>") still resolves from its stored base name.
	if _, err := run("rename-window", "-t", "=grove-ws:grove · grove-75-x", "grove · grove-75-x ✳"); err != nil {
		t.Fatalf("rename-window: %v", err)
	}
	if !WindowLive("grove-ws", "grove · grove-75-x") {
		t.Error("WindowLive lost glyph prefix tolerance under the exact session anchor")
	}
	if got := ResolveWindowName("grove-ws", "grove · grove-75-x"); got != "grove · grove-75-x ✳" {
		t.Errorf("ResolveWindowName = %q, want glyphed name", got)
	}

	// Session existence stays exact on both sides of the fix.
	if !SessionExists("grove") {
		t.Error("SessionExists(grove) = false, want true")
	}
	if SessionExists("grov") {
		t.Error("SessionExists(grov) matched a longer session name")
	}
}

// grove-99: tmux 3.6a rejects a bare "=name" -t on commands whose target is
// a pane or window — set-option, show-options, select-layout, split-window
// all died with "no such session"/"can't find pane", so every cockpit build
// failed at SetCockpitLayout. Those helpers must anchor via ExactActive
// ("=name:"); this exercises each one against a real server.
func TestPaneTargetHelpersExactSession(t *testing.T) {
	scratchServer(t)
	dir := t.TempDir()

	if _, err := run("new-session", "-d", "-s", "grove-x", "-n", "cockpit", "-c", dir); err != nil {
		t.Fatalf("new-session grove-x: %v", err)
	}

	if err := SetCockpitLayout("grove-x", "horizontal"); err != nil {
		t.Fatalf("SetCockpitLayout: %v", err)
	}
	if got := CockpitLayout("grove-x"); got != "horizontal" {
		t.Errorf("CockpitLayout = %q, want horizontal", got)
	}
	if err := SetTitle("grove-x", "grove-x"); err != nil {
		t.Fatalf("SetTitle: %v", err)
	}
	if CockpitReady("grove-x") {
		t.Error("CockpitReady before MarkCockpitReady, want false")
	}
	if err := MarkCockpitReady("grove-x"); err != nil {
		t.Fatalf("MarkCockpitReady: %v", err)
	}
	if !CockpitReady("grove-x") {
		t.Error("CockpitReady after MarkCockpitReady, want true")
	}
	// SpawnPane covers split-window + the CockpitLayout read + SelectLayout.
	if _, err := SpawnPane("grove-x", dir, "true"); err != nil {
		t.Fatalf("SpawnPane: %v", err)
	}
	if err := MainVertical("grove-x", 55); err != nil {
		t.Fatalf("MainVertical: %v", err)
	}
	if err := SelectLayout("grove-x", "horizontal"); err != nil {
		t.Fatalf("SelectLayout: %v", err)
	}
}

// grove-485: the cockpit has two layouts. focus is main-vertical with the
// dash (first pane) at cockpitMainWidth% and the chats stacked right;
// horizontal is even-horizontal — every pane a full-height column.
func TestSelectLayoutFocusHorizontal(t *testing.T) {
	scratchServer(t)
	dir := t.TempDir()

	if _, err := run("new-session", "-d", "-s", "grove-l", "-n", "cockpit", "-x", "200", "-y", "50", "-c", dir); err != nil {
		t.Fatalf("new-session grove-l: %v", err)
	}
	for i := 0; i < 2; i++ { // two chat panes beside the dash
		if _, err := run("split-window", "-t", ExactActive("grove-l"), "-c", dir); err != nil {
			t.Fatalf("split-window: %v", err)
		}
	}

	type pane struct{ left, top, width, height int }
	geometry := func() (winW int, layout string, panes []pane) {
		t.Helper()
		out, err := run("display-message", "-p", "-t", ExactActive("grove-l"), "#{window_width}\t#{window_layout}")
		if err != nil {
			t.Fatalf("display-message: %v", err)
		}
		head := strings.SplitN(strings.TrimSpace(out), "\t", 2)
		if _, err := fmt.Sscan(head[0], &winW); err != nil || len(head) != 2 {
			t.Fatalf("parse window info %q: %v", out, err)
		}
		out, err = run("list-panes", "-t", ExactActive("grove-l"), "-F", "#{pane_left} #{pane_top} #{pane_width} #{pane_height}")
		if err != nil {
			t.Fatalf("list-panes: %v", err)
		}
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			var p pane
			if _, err := fmt.Sscan(line, &p.left, &p.top, &p.width, &p.height); err != nil {
				t.Fatalf("parse pane %q: %v", line, err)
			}
			panes = append(panes, p)
		}
		return winW, head[1], panes
	}

	if err := SelectLayout("grove-l", "focus"); err != nil {
		t.Fatalf("SelectLayout focus: %v", err)
	}
	winW, layout, panes := geometry()
	if len(panes) != 3 {
		t.Fatalf("got %d panes, want 3", len(panes))
	}
	// main-vertical: a left column, then a [..] vertical stack on the right.
	if !strings.Contains(layout, "[") {
		t.Errorf("focus window_layout = %q, want a main-vertical stack", layout)
	}
	want := winW * cockpitMainWidth / 100
	if d := panes[0].width - want; d < -1 || d > 1 {
		t.Errorf("focus dash width = %d of %d, want %d%% (%d) ± 1", panes[0].width, winW, cockpitMainWidth, want)
	}
	if panes[1].left != panes[2].left || panes[1].top == panes[2].top {
		t.Errorf("focus chats not stacked right: %+v", panes[1:])
	}

	if err := SelectLayout("grove-l", "horizontal"); err != nil {
		t.Fatalf("SelectLayout horizontal: %v", err)
	}
	_, layout, panes = geometry()
	if strings.Contains(layout, "[") {
		t.Errorf("horizontal window_layout = %q, want even-horizontal (no stack)", layout)
	}
	for i, p := range panes {
		if p.top != panes[0].top || p.height != panes[0].height {
			t.Errorf("horizontal pane %d = %+v, want a full-height column", i, p)
		}
	}
}
