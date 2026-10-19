// The focus slot (grove-525, focus-swap car 01): while the cockpit is in
// `focus`, every hidden local chat window of the workspace is kept at exactly
// the size of the cockpit's chat slot, so a later swap between two
// same-sized panes sends no SIGWINCH and Claude Code never repaints on
// screen. A resize of a hidden window repaints that chat off screen, where
// nobody waits for it. Design: docs/plans/2026-10-19-focus-same-size-swap-design.md §1.
//
// Hard rule: window-size is only ever set per window (-w -t <@win>). A
// global `set -g window-size manual` kills the tmux server on the next new
// window (LEARNINGS 2026-10-19); TestNoGlobalWindowSize guards it.
package tmux

import (
	"os"
	"strconv"
	"strings"
)

// Slot is the shown chat pane's live size in the cockpit window. It is read
// from the real pane after the re-tile, never computed from main-pane-width:
// the rail's height, the borders and the configurable dash width
// (cockpit.focus_dash_width) all fall out of the measurement.
type Slot struct{ W, H int }

// SizePane is one pane of the server as the slot planner sees it.
type SizePane struct {
	Session    string
	WindowID   string
	WindowName string
	WindowW    int
	WindowH    int
	Pane       string
	Index      int
	PaneW      int
	PaneH      int
	Remote     bool   // @grove_remote: an ssh attachment, not a local chat
	Rail       bool   // @grove_rail: the focus rail
	Home       string // @grove_chat_home: the chat's own hidden session (grove-526)
}

// slotPaneFormat feeds the planner from ONE list-panes -a. Ends in a literal
// "." field: run() trims its output, which would otherwise eat the trailing
// empty @grove_rail of the last line (LEARNINGS 2026-10-19).
const slotPaneFormat = "#{session_name}\t#{window_id}\t#{window_name}\t#{window_width}\t#{window_height}\t#{pane_id}\t#{pane_index}\t#{pane_width}\t#{pane_height}\t#{@grove_remote}\t#{@grove_rail}\t#{@grove_chat_home}\t."

func parseSizePanes(out string) []SizePane {
	var panes []SizePane
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(f) < 12 || f[0] == "" {
			continue
		}
		atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }
		panes = append(panes, SizePane{
			Session: f[0], WindowID: f[1], WindowName: f[2],
			WindowW: atoi(f[3]), WindowH: atoi(f[4]),
			Pane: f[5], Index: atoi(f[6]), PaneW: atoi(f[7]), PaneH: atoi(f[8]),
			Remote: f[9] != "", Rail: f[10] != "",
		})
		if len(f) >= 13 {
			panes[len(panes)-1].Home = f[11]
		}
	}
	return panes
}

// FocusSlot finds the slot in the cockpit session's cockpit window: the one
// local chat pane on screen — not the dash (the lowest live index, never
// `== 0`), not the rail, not an ssh attachment. No chat shown, or more than
// one local chat (a moment mid-move), is no slot: nothing is sized to a
// guess.
func FocusSlot(panes []SizePane, cockpitSession string) (Slot, bool) {
	p, ok := slotPane(panes, cockpitSession)
	if !ok || p.PaneW <= 0 || p.PaneH <= 0 {
		return Slot{}, false
	}
	return Slot{W: p.PaneW, H: p.PaneH}, true
}

// paneSlot reads one pane's live size as a slot; false when it is gone.
func paneSlot(pane string) (Slot, bool) {
	out, err := run("display-message", "-p", "-t", pane, "#{pane_width}\t#{pane_height}")
	if err != nil {
		return Slot{}, false
	}
	f := strings.Split(strings.TrimSpace(out), "\t")
	if len(f) != 2 {
		return Slot{}, false
	}
	w, werr := strconv.Atoi(f[0])
	h, herr := strconv.Atoi(f[1])
	if werr != nil || herr != nil || w <= 0 || h <= 0 {
		return Slot{}, false
	}
	return Slot{W: w, H: h}, true
}

// hiddenWindow is one window of a workspace's hidden chat session: its
// size, and its first pane's.
type hiddenWindow struct {
	id     string
	w, h   int
	pw, ph int
	panes  int
	remote bool
}

// hiddenWindows are the windows of every `grove-chat-<label>-<n>` session of
// the label — focus-minimized and hand-hidden alike, a hand-hidden chat can
// be focused too — in first-seen order. The registry's cockpit check wins a
// name collision (ParseChatSessions' rule): a nil check yields nothing.
func hiddenWindows(panes []SizePane, label string, isCockpit CockpitCheck) []hiddenWindow {
	if label == "" {
		return nil
	}
	prefix := chatSessionPrefix(label)
	at := map[string]int{}
	var wins []hiddenWindow
	for _, p := range panes {
		if _, ok := chatIndex(p.Session, prefix); !ok || isCockpit.cockpit(p.Session) {
			continue
		}
		i, seen := at[p.WindowID]
		if !seen {
			i = len(wins)
			at[p.WindowID] = i
			wins = append(wins, hiddenWindow{id: p.WindowID, w: p.WindowW, h: p.WindowH, pw: p.PaneW, ph: p.PaneH})
		}
		wins[i].panes++
		if p.Remote {
			wins[i].remote = true
		}
	}
	return wins
}

// pinWindowSize is the per-window pin: window-size manual on that window
// (never -g), then the size. It is the chat's PANE that must match the
// slot (grove-526): under the operator's own `set -g pane-border-status`
// a lone pane is a row shorter than its window, so a one-pane window is
// sized to the slot plus that border. Nothing when the pane already has it.
func pinWindowSize(w hiddenWindow, slot Slot) [][]string {
	pw, ph := w.pw, w.ph
	dw, dh := w.w-w.pw, w.h-w.ph
	if w.panes != 1 || dw < 0 || dh < 0 {
		// An operator's split: no border to read, size the window as is.
		pw, ph, dw, dh = w.w, w.h, 0, 0
	}
	if pw == slot.W && ph == slot.H {
		return nil
	}
	return [][]string{
		{"set-option", "-w", "-t", w.id, "window-size", "manual"},
		{"resize-window", "-t", w.id, "-x", strconv.Itoa(slot.W + dw), "-y", strconv.Itoa(slot.H + dh)},
	}
}

// PlanHiddenSizes is the pure planner behind SyncHiddenSizes: the commands
// that bring every local hidden chat window of the label to the cockpit's
// slot. No slot, nothing; a window already slot-sized, nothing; a window
// holding an ssh attachment (a remote chat) is skipped.
func PlanHiddenSizes(panes []SizePane, cockpitSession, label string, isCockpit CockpitCheck) [][]string {
	slot, ok := FocusSlot(panes, cockpitSession)
	if !ok {
		return nil
	}
	var cmds [][]string
	for _, w := range hiddenWindows(panes, label, isCockpit) {
		if !w.remote {
			cmds = append(cmds, pinWindowSize(w, slot)...)
		}
	}
	return cmds
}

// PlanHiddenSizeReset is the leave-focus planner: unset the window-level
// window-size on every hidden chat window of the label, so horizontal and a
// later direct attach behave exactly as before focus pinned them.
func PlanHiddenSizeReset(panes []SizePane, label string, isCockpit CockpitCheck) [][]string {
	var cmds [][]string
	for _, w := range hiddenWindows(panes, label, isCockpit) {
		cmds = append(cmds, []string{"set-option", "-wu", "-t", w.id, "window-size"})
	}
	return cmds
}

func sizePanes() ([]SizePane, error) {
	out, err := run("list-panes", "-a", "-F", slotPaneFormat)
	if err != nil {
		return nil, err
	}
	return parseSizePanes(out), nil
}

// runPlan runs every command and returns the first error: one window that
// vanished mid-plan must not leave the others unsized.
func runPlan(cmds [][]string) error {
	var firstErr error
	for _, c := range cmds {
		if _, err := run(c...); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// SyncHiddenSizes sizes every hidden local chat window of the label to the
// cockpit's slot: one list-panes -a read, then two execs per window whose
// size differs. Callers re-tile first — the slot is read, not predicted.
func SyncHiddenSizes(cockpitSession, label string, isCockpit CockpitCheck) error {
	panes, err := sizePanes()
	if err != nil {
		return err
	}
	return runPlan(PlanHiddenSizes(panes, cockpitSession, label, isCockpit))
}

// ResetHiddenSizes is SyncHiddenSizes' undo, run on leaving focus.
func ResetHiddenSizes(label string, isCockpit CockpitCheck) error {
	panes, err := sizePanes()
	if err != nil {
		return err
	}
	return runPlan(PlanHiddenSizeReset(panes, label, isCockpit))
}

// FocusWorkspace resolves a cockpit session to the workspace label and the
// registry's cockpit check, for the sync at the end of RetileFocus — every
// focus path re-tiles there, but only cmd/gv can read the registry. Injected
// by cmd/gv; nil, or ok=false (the global cockpit, an unregistered session),
// syncs nothing.
var FocusWorkspace func(cockpitSession string) (label string, isCockpit CockpitCheck, ok bool)

// syncAfterRetile is RetileFocus' last step. Best effort: a window left at
// a stale size costs one repaint on its next show, never a broken layout, so
// it never fails the re-tile that already happened.
func syncAfterRetile(session string) {
	if FocusWorkspace == nil {
		return
	}
	if label, isCockpit, ok := FocusWorkspace(session); ok {
		_ = SyncHiddenSizes(session, label, isCockpit)
	}
}

// SyncSizesCommand is the cockpit window-resized hook's second command: this
// very binary (absolute path — tmux-discipline §3) re-syncs the hidden chats
// once the resize has rescaled the slot. Quiet: run-shell would show any
// output or a non-zero exit in the operator's window.
func SyncSizesCommand(session string) string {
	exe := "gv"
	if p, err := os.Executable(); err == nil {
		exe = p
	}
	sh := shellQuote(exe) + " chat sync-sizes --session " + shellQuote(session) + " >/dev/null 2>&1 || true"
	return "run-shell -b " + tmuxQuote(sh)
}

// tmuxQuote double-quotes s for tmux's command parser (a hook body is parsed
// when it runs): backslash, double quote and $ are escaped, and # is doubled
// because run-shell expands its argument as a format.
func tmuxQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, `#`, `##`)
	return `"` + r.Replace(s) + `"`
}
