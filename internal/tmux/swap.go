// The focus swap (grove-526, focus-swap car 02): with every hidden chat
// window already at the slot's size (car 01, slot.go), switching the shown
// chat is ONE swap-pane — no break/join, no re-tile, no SIGWINCH, so Claude
// Code never repaints on screen. Session names stay with chats, not slots:
// every local chat pane carries @grove_chat_home, its own
// `grove-chat-<label>-<n>`, and the hidden session that held the target is
// renamed to the previously shown chat's home. Whether a swap may take that
// path is DecideSwap, a pure function; anything else falls back to the
// break-pane/join-pane path, unchanged.
// Design: docs/plans/2026-10-19-focus-same-size-swap-design.md §2–§3.
package tmux

import (
	"fmt"
	"strings"
)

// ChatHomeOption is the PANE option naming a local chat's own hidden
// session, `grove-chat-<label>-<n>`. Stamped the first time the chat is
// hidden (or, for a chat that predates it, on its next move); it travels
// with the pane like every @grove_ option, so a chat keeps one session name
// for life.
const ChatHomeOption = "@grove_chat_home"

// Fallback reasons DecideSwap gives. Each is a precondition of the fast
// path that did not hold.
const (
	SwapNotFocus     = "not in focus"
	SwapRemote       = "remote chat"
	SwapNothingShown = "no single chat shown"
	SwapPanes        = "target session holds more or less than one pane"
	SwapSize         = "target pane is not slot-sized"
	SwapAttached     = "target session has a client attached"
)

// SwapInput is everything DecideSwap decides from.
type SwapInput struct {
	Layout      string // the cockpit's layout
	Remote      bool   // the target holds an ssh attachment
	Shown       string // the local chat pane in the slot, "" when none
	Slot        Slot   // that pane's size
	TargetPanes int    // panes in the target's hidden session
	Attached    bool   // a client is attached to the target's session
	TargetW     int    // the target pane's size — the PANE, not its window:
	TargetH     int    // a global pane-border-status makes them differ
}

// SwapDecision is DecideSwap's answer: Fast, or the reason it is not.
type SwapDecision struct {
	Fast   bool
	Reason string
}

// DecideSwap picks the fast path (one swap-pane) only when it can be
// invisible: focus, a local target alone in its session, a chat in the
// slot, and the target's pane already exactly the slot's size. A target
// someone is attached to directly is never swapped: the swap would leave
// that client watching the previously shown chat in its place (grove-531);
// the fallback's join takes the pane and ends that session instead.
func DecideSwap(in SwapInput) SwapDecision {
	switch {
	case in.Layout != "focus":
		return SwapDecision{Reason: SwapNotFocus}
	case in.Remote:
		return SwapDecision{Reason: SwapRemote}
	case in.Shown == "":
		return SwapDecision{Reason: SwapNothingShown}
	case in.TargetPanes != 1:
		return SwapDecision{Reason: SwapPanes}
	case in.Attached:
		return SwapDecision{Reason: SwapAttached}
	case in.TargetW != in.Slot.W || in.TargetH != in.Slot.H:
		return SwapDecision{Reason: SwapSize}
	}
	return SwapDecision{Fast: true}
}

// slotPane is FocusSlot's pane: the one local chat on screen in the
// cockpit window. ok=false when there is none, or more than one.
func slotPane(panes []SizePane, cockpitSession string) (SizePane, bool) {
	var win []SizePane
	for _, p := range panes {
		if p.Session == cockpitSession && p.WindowName == cockpitWindow {
			win = append(win, p)
		}
	}
	first, ok := 0, false
	for _, p := range win {
		if !ok || p.Index < first {
			first, ok = p.Index, true
		}
	}
	var chats []SizePane
	for _, p := range win {
		if p.Index != first && !p.Rail && !p.Remote {
			chats = append(chats, p)
		}
	}
	if len(chats) != 1 {
		return SizePane{}, false
	}
	return chats[0], true
}

// swapInput reads DecideSwap's input off one list-panes -a, plus the shown
// and target panes it names (zero values when absent).
func swapInput(panes []SizePane, cockpitSession, layout, target string) (SwapInput, SizePane, SizePane) {
	in := SwapInput{Layout: layout}
	shown, ok := slotPane(panes, cockpitSession)
	if ok && shown.PaneW > 0 && shown.PaneH > 0 {
		in.Shown, in.Slot = shown.Pane, Slot{W: shown.PaneW, H: shown.PaneH}
	}
	var tgt SizePane
	for _, p := range panes {
		if p.Session != target {
			continue
		}
		if in.TargetPanes == 0 {
			tgt = p
			in.TargetW, in.TargetH = p.PaneW, p.PaneH
		}
		in.TargetPanes++
		if p.Remote {
			in.Remote = true
		}
		if p.Attached {
			in.Attached = true
		}
	}
	return in, shown, tgt
}

// PickHome is the session name a chat pane hides into: its stamped home
// when that is one of the label's chat names and no OTHER session holds it
// (self — the session about to be renamed — does not count); otherwise the
// lowest `grove-chat-<label>-<n>` that is neither a live session nor another
// chat's home.
func PickHome(stamp, label, self string, sessions, homes []string) string {
	if _, ok := chatIndex(stamp, chatSessionPrefix(label)); ok {
		taken := false
		for _, s := range sessions {
			if s == stamp && s != self {
				taken = true
				break
			}
		}
		if !taken {
			return stamp
		}
	}
	return NextChatSession(label, append(append([]string(nil), sessions...), homes...))
}

// sessionsAndHomes are the server's session names and every pane's
// @grove_chat_home, off the same list-panes -a.
func sessionsAndHomes(panes []SizePane) (sessions, homes []string) {
	seen := map[string]bool{}
	for _, p := range panes {
		if !seen[p.Session] {
			seen[p.Session] = true
			sessions = append(sessions, p.Session)
		}
		if p.Home != "" {
			homes = append(homes, p.Home)
		}
	}
	return sessions, homes
}

// SwapFocusHid is @grove_focus_hid after a swap: the target's old session
// name (out) is replaced, in place, by the one the shown chat now lives in
// (in). A target that was hidden by hand is not in the list, so in is
// appended — the shown chat was minimized by focus either way.
func SwapFocusHid(list []string, out, in string) []string {
	var next []string
	placed := false
	for _, s := range list {
		switch s {
		case out:
			if !placed {
				next = append(next, in)
				placed = true
			}
		case in:
			if !placed {
				next = append(next, in)
				placed = true
			}
		default:
			next = append(next, s)
		}
	}
	if !placed {
		next = append(next, in)
	}
	return next
}

// SwapResult says what FocusSwap did. Decision.Fast=false means nothing
// moved and the caller takes the fallback path.
type SwapResult struct {
	Decision SwapDecision
	Shown    string // the target's pane, now in the slot
	Hidden   string // the pane that left the slot
	HiddenAs string // the session it now lives in (its home)
	From     string // the session the target left, renamed to HiddenAs
}

// planSwap is the fast path's command list, one tmux exec: stamp the
// target's home when it has none (it is the session it is leaving), the
// swap itself, the rename that hands the session's name back to the chat
// now in it, the shown chat's home stamp when it changed, the minimized
// list, and the select-pane when the caller wants the keyboard there.
func planSwap(cockpitSession string, shown, tgt SizePane, target, home string, hid []string, focus bool) []string {
	var cmds [][]string
	if tgt.Home == "" {
		cmds = append(cmds, []string{"set-option", "-p", "-t", tgt.Pane, ChatHomeOption, target})
	}
	cmds = append(cmds, []string{"swap-pane", "-d", "-s", tgt.Pane, "-t", shown.Pane})
	if home != target {
		cmds = append(cmds, []string{"rename-session", "-t", Exact(target), home})
	}
	if shown.Home != home {
		cmds = append(cmds, []string{"set-option", "-p", "-t", shown.Pane, ChatHomeOption, home})
	}
	cmds = append(cmds, []string{"set-option", "-t", ExactActive(cockpitSession), FocusHidOption, strings.Join(SwapFocusHid(hid, target, home), " ")})
	if focus {
		cmds = append(cmds, []string{"select-pane", "-t", tgt.Pane})
	}
	var argv []string
	for i, c := range cmds {
		if i > 0 {
			argv = append(argv, ";")
		}
		argv = append(argv, c...)
	}
	return argv
}

// FocusSwap switches the cockpit's shown chat to the hidden chat session
// target with one swap-pane when DecideSwap allows it. layout is the
// cockpit's, as the caller already read it. On a fallback decision nothing
// is touched. No re-tile: the slot did not change size.
func FocusSwap(cockpitSession, label, layout, target string, focus bool) (SwapResult, error) {
	panes, err := sizePanes()
	if err != nil {
		return SwapResult{}, err
	}
	in, shown, tgt := swapInput(panes, cockpitSession, layout, target)
	res := SwapResult{Decision: DecideSwap(in)}
	if !res.Decision.Fast {
		return res, nil
	}
	sessions, homes := sessionsAndHomes(panes)
	home := PickHome(shown.Home, label, target, sessions, homes)
	if _, err := run(planSwap(cockpitSession, shown, tgt, target, home, FocusHid(cockpitSession), focus)...); err != nil {
		return SwapResult{}, fmt.Errorf("swap %s into the slot: %w", target, err)
	}
	res.Shown, res.Hidden, res.HiddenAs, res.From = tgt.Pane, shown.Pane, home, target
	return res, nil
}
