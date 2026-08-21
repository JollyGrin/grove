// Overstory additions to the copied parkranger tmux wrapper. New
// functionality lives here so tmux.go stays byte-comparable with upstream.
package tmux

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// SplitVerticalWindow splits a specific window (not the session's current
// one) horizontally, side-by-side, and returns the new pane's immutable
// "%N" id. Needed because grab creates detached windows that are never the
// active window. Callers send the worker command to the returned id, never
// to a numeric ".1" — pane indices depend on the user's pane-base-index
// (grove-168: under the common `pane-base-index 1` a ".1" target is the
// FIRST pane, the worktree shell, not this split).
func SplitVerticalWindow(windowTarget, workDir string) (string, error) {
	out, err := run("split-window", "-h", "-t", windowTarget, "-c", workDir, "-P", "-F", "#{pane_id}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// PaneInfo is one pane's index and foreground command.
type PaneInfo struct {
	Index   int
	Command string
}

// ClaudePane resolves which pane of a window holds the claude session.
// Worker windows normally run claude in the split (second) pane, but a
// window can lose its split (observed live: DEV-4761) — then claude is the
// only pane. Every successful path returns a REAL index straight from
// list-panes, so the result is correct under any pane-base-index
// (grove-168); the historical literal-1 fallback fires only when the
// window is unlistable, where any composed target fails anyway, so it
// stays for behavior compatibility — never add new literal indices. The
// window is resolved by id (grove-116): a raw name target prefix-matches,
// so this used to list a prefix-extending SIBLING window's panes once the
// task's own window was gone.
func ClaudePane(session, window string) int {
	id, ok := WindowID(session, window)
	if !ok {
		return 1
	}
	out, err := run("list-panes", "-t", id,
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

// pasteSettle is the gap between the paste landing in the pane and the
// Enter that submits it. Zero gap was the grove-144 bug: Claude's TUI can
// still be ingesting the paste when Enter arrives and swallows it into the
// input, leaving an unsent "[Pasted text]" while gv reported success.
var pasteSettle = 250 * time.Millisecond

// submitSettle is how long the TUI gets to redraw before each scrape of the
// pane that confirms the submit landed.
var submitSettle = 300 * time.Millisecond

// PasteText delivers multi-line or special-character text to a pane via
// load-buffer + a BRACKETED paste-buffer (-p), lets the receiving TUI settle,
// then submits with a separate Enter and verifies that the submit actually
// landed: one retry Enter, then an error (grove-144), so callers never record
// an answer the agent never received. SendKeys is unsafe for prose: it is
// single-line and tmux interprets key-name lookalikes.
func PasteText(target, text string) error {
	load := exec.Command("tmux", "load-buffer", "-b", "gv-relay", "-")
	load.Stdin = strings.NewReader(text)
	if out, err := load.CombinedOutput(); err != nil {
		return &execError{op: "load-buffer", out: string(out), err: err}
	}
	// -p brackets the paste when the foreground app requested bracketed
	// paste mode (Claude's TUI does; a plain shell does not, and tmux then
	// sends the buffer bare). The doc comment claimed this for a year while
	// the flag was missing.
	if _, err := run("paste-buffer", "-d", "-p", "-b", "gv-relay", "-t", target); err != nil {
		return err
	}
	time.Sleep(pasteSettle)
	enter := func() error {
		_, err := run("send-keys", "-t", target, "Enter")
		return err
	}
	if err := enter(); err != nil {
		return err
	}
	// Whole visible pane, not CapturePaneBottom: that helper reads the pane's
	// bottom N ROWS, which are blank whenever the app draws from the top
	// (caught in e2e/relay.sh — the scrape saw nothing but empty rows and
	// called every relay landed). inputBoxContent finds the box itself.
	return verifySubmit(target, text,
		func() (string, error) { return CapturePane(target) },
		enter,
		func() { time.Sleep(submitSettle) })
}

// verifySubmit is the check → retry-Enter → check ladder that runs after a
// relayed paste, with its tmux calls injected so the sequencing is testable
// without a live server. Returns nil the moment the submit looks landed;
// after one failed retry it reports an actionable error and the caller must
// NOT record the answer.
func verifySubmit(target, text string, capture func() (string, error), enter func() error, settle func()) error {
	for attempt := 0; attempt < 2; attempt++ {
		settle()
		out, err := capture()
		if err != nil || pasteLanded(out, text) {
			// Unreadable pane counts as landed: scraping is garnish, and a
			// false alarm here would strand a delivered answer.
			return nil
		}
		if attempt == 0 {
			if err := enter(); err != nil {
				return err
			}
		}
	}
	// One line: this also renders as the cockpit's flash message.
	return fmt.Errorf("relay to %s never submitted — the text is still in the agent's input box after two Enters, nothing was recorded (recover: tmux send-keys -t %s Enter)", target, target)
}

// pasteProbeRunes is how much of the relayed text the scrape looks for. Long
// enough to be distinctive, short enough to survive a narrow input box.
const pasteProbeRunes = 24

// pasteLanded reports whether a relayed paste appears to have been SUBMITTED,
// by scraping the agent's input box out of a pane capture. Deliberately
// permissive — pane chrome has changed under us before (hooks are truth,
// scraping is garnish), so anything unreadable as an input box counts as
// landed. Only a box that still visibly holds our text, or a pending
// "[Pasted text]" chip, is treated as unsent.
func pasteLanded(capture, text string) bool {
	box := squeeze(inputBoxContent(capture))
	if box == "" {
		return true
	}
	if strings.Contains(box, "[Pastedtext") { // squeezed "[Pasted text …]"
		return false
	}
	probe := squeeze(text)
	if r := []rune(probe); len(r) > pasteProbeRunes {
		probe = string(r[:pasteProbeRunes])
	}
	if probe == "" {
		return true
	}
	return !strings.Contains(box, probe)
}

// footerSlack is how many non-box lines may sit below the input box (Claude
// prints a hint/token line or two) before the scrape gives up looking. Bounded
// so a box scrolled far up in the transcript is never mistaken for the input.
const footerSlack = 6

// inputBoxContent extracts the bottom-most bordered box from a pane capture —
// Claude's input box. Tolerates a top border that has scrolled off. Returns ""
// when no box is visible (a plain shell pane, or unfamiliar chrome).
func inputBoxContent(capture string) string {
	lines := strings.Split(capture, "\n")
	end, skipped := -1, 0
	for i := len(lines) - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		switch {
		case t == "":
			continue
		case isBottomBorder(t):
			end = i - 1
		case isBoxSide(t):
			end = i // bottom border scrolled off
		default:
			if skipped++; skipped > footerSlack {
				return ""
			}
			continue
		}
		break
	}
	if end < 0 {
		return ""
	}
	start := end + 1
	for i := end; i >= 0; i-- {
		if !isBoxSide(strings.TrimSpace(lines[i])) {
			break
		}
		start = i
	}
	if start > end {
		return ""
	}
	return strings.Join(lines[start:end+1], "\n")
}

func isBottomBorder(trimmed string) bool {
	return strings.HasPrefix(trimmed, "╰") || strings.HasPrefix(trimmed, "└")
}

func isBoxSide(trimmed string) bool {
	return strings.HasPrefix(trimmed, "│") || strings.HasPrefix(trimmed, "┃")
}

// squeeze drops every whitespace and box-drawing rune, so a comparison
// survives the input box's borders AND its wrapping (which can break the text
// at a space or mid-word).
func squeeze(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsSpace(r) || strings.ContainsRune(boxRunes, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

const boxRunes = "│┃╭╮╰╯┌┐└┘─━├┤"

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
