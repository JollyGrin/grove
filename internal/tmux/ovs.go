// Overstory additions to the copied parkranger tmux wrapper. New
// functionality lives here so tmux.go stays byte-comparable with upstream.
package tmux

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// SplitVerticalWindow splits a specific window (not the session's current
// one) horizontally, side-by-side. Needed because grab creates detached
// windows that are never the active window.
func SplitVerticalWindow(windowTarget, workDir string) error {
	_, err := run("split-window", "-h", "-t", windowTarget, "-c", workDir)
	return err
}

// PaneInfo is one pane's index and foreground command.
type PaneInfo struct {
	Index   int
	Command string
}

// ClaudePane resolves which pane of a window holds the claude session.
// Worker windows normally run claude in pane 1, but a window can lose its
// split (observed live: DEV-4761) — then claude is the only pane, at index
// 0. Falls back to the historical pane 1 when the window can't be listed,
// so callers behave exactly as before this helper existed.
func ClaudePane(session, window string) int {
	out, err := run("list-panes", "-t", Exact(session)+":"+window,
		"-F", "#{pane_index} #{pane_current_command}")
	if err != nil {
		return 1
	}
	panes := parsePaneList(out)
	if len(panes) == 0 {
		return 1
	}
	return pickPane(panes)
}

// pickPane picks the highest-index claude-ish pane (claude may present as
// node or bun depending on install), else the highest-index pane — the
// worker layout always puts claude right/last, so a node dev-server in
// pane 0 must lose to claude in pane 1.
func pickPane(panes []PaneInfo) int {
	if len(panes) == 0 {
		return 0
	}
	best, bestClaude := -1, -1
	for _, p := range panes {
		if p.Index > best {
			best = p.Index
		}
		if isClaudeish(p.Command) && p.Index > bestClaude {
			bestClaude = p.Index
		}
	}
	if bestClaude >= 0 {
		return bestClaude
	}
	return best
}

// isClaudeish reports whether a pane's foreground command looks like a
// claude session. Claude Code sets its process title to its bare version
// string (observed live: "2.1.197"), so version-shaped names count too.
func isClaudeish(cmd string) bool {
	switch cmd {
	case "claude", "node", "bun":
		return true
	}
	return versionRe.MatchString(cmd)
}

var versionRe = regexp.MustCompile(`^\d+(\.\d+)+$`)

func parsePaneList(out string) []PaneInfo {
	var panes []PaneInfo
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		idx, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		panes = append(panes, PaneInfo{Index: idx, Command: fields[1]})
	}
	return panes
}

// PasteText delivers multi-line or special-character text to a pane via
// load-buffer + paste-buffer (bracketed paste), then submits with a separate
// Enter. SendKeys is unsafe for prose: it is single-line and tmux interprets
// key-name lookalikes.
func PasteText(target, text string) error {
	load := exec.Command("tmux", "load-buffer", "-b", "gv-relay", "-")
	load.Stdin = strings.NewReader(text)
	if out, err := load.CombinedOutput(); err != nil {
		return &execError{op: "load-buffer", out: string(out), err: err}
	}
	if _, err := run("paste-buffer", "-d", "-b", "gv-relay", "-t", target); err != nil {
		return err
	}
	_, err := run("send-keys", "-t", target, "Enter")
	return err
}

// SendRawKey sends a single key without an Enter — for answering option
// pickers (numbered prompts, plan approval) where Enter-wrapping breaks
// the interaction.
func SendRawKey(target, key string) error {
	_, err := run("send-keys", "-t", target, "-l", key)
	return err
}

type execError struct {
	op  string
	out string
	err error
}

func (e *execError) Error() string {
	return "tmux " + e.op + ": " + e.err.Error() + "\n" + strings.TrimSpace(e.out)
}
