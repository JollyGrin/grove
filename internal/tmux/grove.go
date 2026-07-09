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

// cockpitHint is what the reserved placeholder window 0 shows before the
// cockpit is opened, so an operator who pops `Ctrl-b w` and lands on it
// knows the slot is the cockpit's and how to fill it.
const cockpitHint = `echo '⁂ cockpit reserved — run: gv   (builds the dashboard + orchestrator in this window)'`

// EnsureWorkspaceSession guarantees the workspace's single tmux session
// exists with window 0 reserved as the cockpit — the collapse that makes
// `Ctrl-b w` show one node per workspace (grove-<label>: cockpit + workers).
// Idempotent: called by both grab (which may create the session before the
// cockpit is ever opened) and buildCockpit. A freshly created session's
// window 0 is named `cockpit`, auto-rename pinned, and holds a placeholder
// hint that `gv` upgrades in place. dir is the session's root cwd (the
// workspace root) and must exist.
func EnsureWorkspaceSession(session, dir string) error {
	if !SessionExists(session) {
		if _, err := run("new-session", "-d", "-s", session, "-n", "cockpit", "-c", dir); err != nil {
			return err
		}
		if err := DisableAutoRename(session + ":cockpit"); err != nil {
			return err
		}
		return SendKeys(session+":cockpit.0", cockpitHint)
	}
	// Session exists but predates the reserved slot (or was built cockpit-
	// first without one): make sure a cockpit window is present so worker
	// windows never become window 0.
	if !WindowExists(session, "cockpit") {
		if err := CreateWindow(session, "cockpit", dir); err != nil {
			return err
		}
		return DisableAutoRename(session + ":cockpit")
	}
	return nil
}

// SelectWindow makes the given window the session's active one.
func SelectWindow(target string) error {
	_, err := run("select-window", "-t", target)
	return err
}

// MarkCockpitReady / CockpitReady track whether a session's reserved cockpit
// window has been upgraded from the placeholder into the real dash +
// orchestrator layout. Stored as a session user-option so a grab-created
// placeholder session (which exists but has no real cockpit yet) is
// distinguishable from an opened one — openCockpit builds when not ready.
func MarkCockpitReady(session string) error {
	_, err := run("set-option", "-t", session, "@grove_cockpit", "ready")
	return err
}

// CockpitReady reports whether session exists AND its cockpit has been built.
func CockpitReady(session string) bool {
	if !SessionExists(session) {
		return false
	}
	out, err := run("show-options", "-t", session, "-v", "@grove_cockpit")
	return err == nil && strings.TrimSpace(out) == "ready"
}

// RenameWorker pushes a live status glyph into a worker window's name so
// `Ctrl-b w` reads as a board (grove-29 P3): the window becomes
// "<base> <glyph>". base is the task's stored tmux_window — which is NEVER
// rewritten, so it stays a clean, stable resolution key; the glyph is only a
// display suffix. Targeting session:base prefix-matches the current (possibly
// already-glyphed) window, so re-glyphing on the next transition just works.
func RenameWorker(session, base, glyph string) error {
	if base == "" || glyph == "" {
		return nil
	}
	_, err := run("rename-window", "-t", session+":"+base, base+" "+glyph)
	return err
}

// WindowLive reports whether a worker window exists, tolerating a status
// glyph suffix that exact name-comparison (tmux.WindowExists) would miss:
// it targets session:base, which tmux prefix-matches to the glyphed window.
func WindowLive(session, base string) bool {
	_, err := run("list-panes", "-t", session+":"+base)
	return err == nil
}

// ResolveWindowName maps a stored base window name to the window's CURRENT
// full name (base, or "base <glyph>" once P3 has glyphed it). Callers that
// must hand an EXACT window name to a name-comparing helper (the byte-
// comparable detect probes) resolve first so the glyph never hides a live
// worker. Falls back to base when the session/window can't be listed.
func ResolveWindowName(session, base string) string {
	out, err := run("list-windows", "-t", session, "-F", "#{window_name}")
	if err != nil {
		return base
	}
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
		if name == base || strings.HasPrefix(name, base+" ") {
			return name
		}
	}
	return base
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

// WorkerWindowProfile is WorkerWindow with an active model profile appended
// as a third "·"-joined segment ("<repo> · <ticket> · <profile>"), so a
// profiled worker (grove-36) reads at a glance in `Ctrl-b w` / `gv ls`
// instead of hiding behind Claude Code's unreliable /status. An empty
// profile returns exactly WorkerWindow(repoShort, ticket) — byte-identical to
// the no-profile window name, the load-bearing invariant the feature promises.
// The profile is sanitized like the other parts (tmux forbids "." and ":" in
// a window name).
func WorkerWindowProfile(repoShort, ticket, profile string) string {
	base := WorkerWindow(repoShort, ticket)
	if profile == "" {
		return base
	}
	r := strings.NewReplacer(".", "-", ":", "-")
	return base + " · " + r.Replace(profile)
}

// SetPaneProfile tags a pane with its model-profile name in a pane USER
// OPTION (@grove_profile), so a profiled orchestrator pane is distinguishable
// from the default (Anthropic) pane sharing the cockpit window. Unlike a pane
// title, a user option cannot be touched by the pane's foreground program —
// Claude Code sets the terminal title via OSC on boot, which would clobber a
// select-pane -T tag; @grove_profile survives (grove-36 T1). The tag only
// becomes visible once ShowPaneBorders has turned the window's pane-border
// status line on. An empty profile clears the option.
func SetPaneProfile(pane, profile string) error {
	if profile == "" {
		_, err := run("set-option", "-p", "-t", pane, "-u", "@grove_profile")
		return err
	}
	_, err := run("set-option", "-p", "-t", pane, "@grove_profile", profile)
	return err
}

// paneBorderFormat renders each pane's index plus, when the pane carries a
// @grove_profile user option (SetPaneProfile), a "⚡ <profile>" tag; otherwise
// it falls back to whatever title the pane's foreground program set. So a
// profiled pane shows its backend permanently while unprofiled panes keep
// Claude Code's own titles — the no-profile path is visually unchanged.
const paneBorderFormat = "#{pane_index}: #{?#{@grove_profile},⚡ #{@grove_profile},#{pane_title}}"

// ShowPaneBorders turns on a top pane-border status line, scoped to ONE window
// via its target (e.g. session:cockpit) — never a global/session-wide option,
// so worker windows keep their borderless look. Pairs with SetPaneProfile to
// surface a profiled orchestrator pane's backend. Two set-window-option calls,
// matching the DisableAutoRename per-window scoping pattern.
func ShowPaneBorders(windowTarget string) error {
	if _, err := run("set-window-option", "-t", windowTarget, "pane-border-status", "top"); err != nil {
		return err
	}
	_, err := run("set-window-option", "-t", windowTarget, "pane-border-format", paneBorderFormat)
	return err
}

// DisableAutoRename turns automatic-rename off for a single window (by
// target), so a pane's foreground process (e.g. Claude Code sets its title
// to its bare version string, "2.1.204") can never clobber the name set at
// creation. Scoped to the one window via its target — never a global option,
// so no window outside the given target is affected.
func DisableAutoRename(target string) error {
	_, err := run("set-window-option", "-t", target, "automatic-rename", "off")
	return err
}

// closablePane is the pure guard for self-close: a pane may only be killed
// when it lives in a grove cockpit session (grove / grove-<label>) and is
// not pane 0 — pane 0 is the dashboard, and the cockpit's whole point is
// that it survives. Keeps a mis-wired orchestrator from euthanizing the
// dashboard or a pane in some unrelated tmux session.
//
// Since the workspace collapse (grove-29 P2) worker windows also live in
// grove-<label>, so they now pass the session check too — but this guard is
// only ever invoked for an orchestrator's self-dismissal (PaneClosable with
// its own $TMUX_PANE, in the cockpit window), and every worker window's
// pane 0 is the protected worktree shell, so no worker pane is ever at risk.
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
