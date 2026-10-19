package tui

import (
	"testing"

	"github.com/JollyGrin/grove/internal/state"
)

// grove-441: one sentinel parser for every surface. The plain kickoff form
// renders exactly as before; the tolerant variants now render too.
func TestDoneBlurb(t *testing.T) {
	cases := []struct{ msg, want string }{
		{"All finished.\n\nSTATUS: DONE — Added URL-persisted filters.\nCheck the preview.", "Added URL-persisted filters."},
		{"**STATUS: DONE** — shipped", "shipped"},
		{"STATUS: DONE: shipped", "shipped"},
		{"STATUS: DONE", ""},
		{"STATUS: DONE — early\n\nSTATUS: BLOCKED — creds", ""},
		{"no sentinel here", ""},
	}
	for _, c := range cases {
		if got := doneBlurb(&state.Task{LastMessage: c.msg}); got != c.want {
			t.Errorf("doneBlurb(%q) = %q, want %q", c.msg, got, c.want)
		}
	}
}
