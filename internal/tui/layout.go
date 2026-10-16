package tui

// Cockpit pane orientation (grove-52). The layout itself lives in the tmux
// session option @grove_layout — read live at L-press time, never cached on
// Model — so this file is only the pure cycle step.

// nextLayout flips the L hotkey's toggle: horizontal ↔ focus (grove-485).
// Anything that isn't focus (including "" and the retired vertical/tiled)
// flips to focus, so the default horizontal moves on first press even when
// the session option is unset.
func nextLayout(cur string) string {
	if cur == "focus" {
		return "horizontal"
	}
	return "focus"
}
