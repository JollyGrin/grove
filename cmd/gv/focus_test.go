package main

import (
	"strings"
	"testing"
)

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

// grove-488: the refusals that come before anything is resolved or moved.
func TestFocusChatRefusals(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	if _, err := focusChat("@box/grove-chat-x-1", true); err == nil || !strings.Contains(err.Error(), "gv chat show") {
		t.Errorf("remote target must be refused naming gv chat show, got %v", err)
	}
	if err := cmdChatFocus(nil); err == nil || !strings.Contains(err.Error(), "usage: gv chat focus") {
		t.Errorf("no argument must print usage, got %v", err)
	}
	if err := cmdChatFocus([]string{"a", "b"}); err == nil {
		t.Error("two arguments must be refused")
	}
}
