package main

import "testing"

// In horizontal (or with no layout readable) the spawn path moves nothing:
// the helper reports it did not run, which is also what gates the rail
// rewrite. Isolated tmux: no server exists under the scratch TMUX_TMPDIR.
func TestFocusBeforeSpawnGatedOnLayout(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	if focusBeforeSpawn("grove-nope", "nope", "") {
		t.Error("ran without a focus layout")
	}
	if focusBeforeSpawn("grove", "", "") {
		t.Error("ran for the unlabelled global cockpit")
	}
}
