// grove-78: session targets must be exact-matched. tmux resolves a bare
// -t target against window names across ALL sessions, so a session
// literally named "grove" (the legacy cockpit — and any dogfooding
// workspace) collides with every "grove · <ticket>" worker window.
// The integration test below runs against a scratch tmux server, fully
// isolated per tmux-discipline §1: TMUX_TMPDIR points at a temp dir and
// $TMUX is unset (it would override TMUX_TMPDIR and hit the real server).
package tmux

import (
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
