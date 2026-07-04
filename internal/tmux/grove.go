// Grove additions to the tmux wrapper: cockpit layout + orchestrator-pane
// spawning (grove-cockpit-design §4.2). New code goes here, not in the
// byte-comparable upstream files.
package tmux

import (
	"fmt"
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
