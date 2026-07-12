package tui

// Cockpit pane orientation (grove-52). The layout itself lives in the tmux
// session option @grove_layout — read live at L-press time, never cached on
// Model — so this file is only the pure cycle step.

// nextLayout advances the L hotkey's cycle: horizontal → vertical → tiled →
// horizontal. Unknown (including "") advances to vertical, so the default
// horizontal moves on first press even when the session option is unset.
func nextLayout(cur string) string {
	switch cur {
	case "vertical":
		return "tiled"
	case "tiled":
		return "horizontal"
	default:
		return "vertical"
	}
}
