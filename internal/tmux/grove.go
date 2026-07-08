// Grove additions to the tmux wrapper: cockpit layout + orchestrator-pane
// spawning (grove-cockpit-design §4.2). New code goes here, not in the
// byte-comparable upstream files.
package tmux

import (
	"fmt"
	"strconv"
	"strings"
)

// MainVertical lays a session's first window out as main-vertical (main
// pane left, others stacked right) with the main pane holding width% of
// the window — the cockpit shape: TUI left, orchestrator chats right.
func MainVertical(session string, widthPercent int) error {
	if widthPercent <= 0 || widthPercent > 100 {
		widthPercent = 50
	}
	// Target the bare session (its active window) — window indexes depend
	// on the user's base-index, so ":0" is not safe to assume.
	if _, err := run("set-option", "-t", session, "-w", "main-pane-width",
		fmt.Sprintf("%d%%", widthPercent)); err != nil {
		return err
	}
	_, err := run("select-layout", "-t", session, "main-vertical")
	return err
}

// SetTitle drives the outer terminal-tab title for the session: enables
// title updates and pins the string to title. It's a session option
// (no -g), so worker windows and unrelated tmux sessions keep their own
// titles. A bare label is safe as the string — no tmux format expansion
// we depend on.
func SetTitle(session, title string) error {
	if _, err := run("set-option", "-t", session, "set-titles", "on"); err != nil {
		return err
	}
	_, err := run("set-option", "-t", session, "set-titles-string", title)
	return err
}

// SpawnPane opens a fresh shell pane in the session's first window rooted
// at dir, re-tiles main-vertical, types cmd into it, and focuses it.
// The command is typed (SendKeys) rather than passed to split-window so
// shell aliases in worker/orchestrator commands keep working, and the
// pane survives the command exiting.
func SpawnPane(session, dir, cmd string) (string, error) {
	paneID, err := run("split-window", "-t", session, "-c", dir, "-P", "-F", "#{pane_id}")
	if err != nil {
		return "", err
	}
	paneID = strings.TrimSpace(paneID)
	if _, err := run("select-layout", "-t", session, "main-vertical"); err != nil {
		return "", err
	}
	if err := SendKeys(paneID, cmd); err != nil {
		return "", err
	}
	if _, err := run("select-pane", "-t", paneID); err != nil {
		return "", err
	}
	return paneID, nil
}

// WorkerWindow builds a worker window's display name: "<repo-short> · <ticket>".
// The middle dot groups a workspace's repos visually in `Ctrl-b w`. tmux
// forbids "." and ":" in the parts (they collide with pane/window target
// syntax), so they're sanitized to "-"; the "·" and spaces are safe inside a
// single target argument (verified against tmux target resolution).
func WorkerWindow(repoShort, ticket string) string {
	r := strings.NewReplacer(".", "-", ":", "-")
	return r.Replace(repoShort) + " · " + r.Replace(ticket)
}

// NameWindow renames a window and pins the name by disabling
// automatic-rename, so a pane's foreground process (e.g. Claude Code sets
// its title to its bare version string, "2.1.204") can never clobber it.
// Scoped to the one window via its target — never a global option, so no
// window outside the given target is affected.
func NameWindow(target, name string) error {
	if _, err := run("rename-window", "-t", target, name); err != nil {
		return err
	}
	return DisableAutoRename(target)
}

// DisableAutoRename turns automatic-rename off for a single window (by
// target). Use when the name was already set at creation (new-window -n) and
// only the auto-rename latch needs disarming.
func DisableAutoRename(target string) error {
	_, err := run("set-window-option", "-t", target, "automatic-rename", "off")
	return err
}

// closablePane is the pure guard for self-close: a pane may only be killed
// when it lives in a grove cockpit session (grove / grove-<label>) and is
// not pane 0 — pane 0 is the dashboard, and the cockpit's whole point is
// that it survives. Keeps a mis-wired orchestrator from euthanizing the
// dashboard or a pane in some unrelated tmux session.
func closablePane(session string, index int) error {
	if session != "grove" && !strings.HasPrefix(session, "grove-") {
		return fmt.Errorf("pane is in session %q, not a grove cockpit — refusing to close", session)
	}
	if index == 0 {
		return fmt.Errorf("pane 0 is the dashboard — refusing to close it")
	}
	return nil
}

// PaneClosable reports whether pane (e.g. "%23") is a cockpit orchestrator
// pane safe to kill — queries its session/index and runs closablePane.
// Callers check this BEFORE any irreversible side effect (e.g. logging the
// dismissal) so a guard rejection never leaves a half-done record.
func PaneClosable(pane string) error {
	if strings.TrimSpace(pane) == "" {
		return fmt.Errorf("no pane id (are you inside a tmux pane?)")
	}
	info, err := run("display-message", "-p", "-t", pane, "-F", "#{session_name}\t#{pane_index}")
	if err != nil {
		return err
	}
	parts := strings.SplitN(strings.TrimSpace(info), "\t", 2)
	if len(parts) != 2 {
		return fmt.Errorf("unexpected pane info %q", info)
	}
	index, err := strconv.Atoi(parts[1])
	if err != nil {
		return fmt.Errorf("parse pane index %q: %w", parts[1], err)
	}
	return closablePane(parts[0], index)
}

// ClosePane kills a cockpit orchestrator pane by id after re-checking
// PaneClosable. The caller passes its own $TMUX_PANE, so this is how a
// fire-and-forget orchestrator dismisses itself: killing the pane also
// takes down the claude process running in it.
func ClosePane(pane string) error {
	if err := PaneClosable(pane); err != nil {
		return err
	}
	_, err := run("kill-pane", "-t", pane)
	return err
}
