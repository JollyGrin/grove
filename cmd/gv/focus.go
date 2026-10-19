package main

// grove-487: focus-mode minimizing. In the `focus` layout the cockpit window
// holds the dash and ONE chat; the others are hidden with chat-hide's
// primitives and live on as boxes in the rail. The decisions are in
// internal/tmux/focus.go; this file wires them to events, the registry and
// the rail. In `horizontal` nothing here moves a pane.

import (
	"fmt"
	"strings"

	"github.com/JollyGrin/grove/internal/chat"
	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/tmux"
	"github.com/JollyGrin/grove/internal/workspace"
)

// focusMoved is the post-move event hook for a cockpit session: chat_hidden
// / chat_shown with the chat-hide keys plus reason "focus", appended after
// the tmux move succeeded (#412).
func focusMoved(label, dir string) tmux.FocusMoved {
	return func(kind, session, pane string) {
		typ := state.EvChatHidden
		if kind == "shown" {
			typ = state.EvChatShown
		}
		data := map[string]string{"session": session, "pane": pane, "workspace": label, "reason": "focus"}
		if id := tmux.PaneOption(pane, "@grove_chat_session"); id != "" {
			data["session_id"] = id
		}
		_ = state.Append(dir, state.Event{Type: typ, Data: data})
	}
}

// applyCockpitLayout is what the dash's `L` runs (tui.ApplyLayout): the
// layout switch plus, for a registered workspace, the minimize / restore
// that goes with it. The caller has already persisted @grove_layout.
func applyCockpitLayout(session, name string) error {
	label, dir, err := cockpitWorkspace(session)
	if err != nil || label == "" {
		return tmux.ApplyLayout(session, name)
	}
	isCockpit, err := cockpitSessionCheck()
	if err != nil {
		return tmux.ApplyLayout(session, name)
	}
	moved := focusMoved(label, dir)
	if name != "focus" {
		// Rail first so the restored chats join a plain window, then one
		// re-tile into the layout (the rail is already gone).
		if err := tmux.KillRail(session); err != nil {
			return err
		}
		rerr := tmux.RestoreMinimized(session, label, isCockpit, moved)
		if err := tmux.SelectLayout(session, name); err != nil {
			return err
		}
		return rerr
	}
	if err := tmux.ApplyLayout(session, name); err != nil {
		return err
	}
	merr := tmux.MinimizeChats(session, label, isCockpit, moved)
	railWriteNow(label, session)
	return merr
}

// focusBeforeSpawn minimizes the shown chat when the cockpit is in focus, so
// the chat about to open is the one on screen. Gated on the layout: in
// horizontal it does nothing (and reads one option). Returns whether it ran.
func focusBeforeSpawn(session, label, root string) bool {
	if label == "" || tmux.CockpitLayout(session) != "focus" {
		return false
	}
	isCockpit, err := cockpitSessionCheck()
	if err != nil {
		return false
	}
	_ = tmux.MinimizeShown(session, label, isCockpit, focusMoved(label, config.StateDirAt(root)))
	return true
}

// focusAfterSpawn refreshes the rail once the new chat is in place.
func focusAfterSpawn(session, label string, ran bool) {
	if ran {
		railWriteNow(label, session)
	}
}

// spawnLabel is the workspace label of a spawn target; "" for the global
// cockpit, where focus does not minimize (chat sessions need a label).
func spawnLabel(ws *workspace.Workspace) string {
	if ws == nil {
		return ""
	}
	return ws.Label
}

// --- grove-488: switch the shown chat ---

// focusResult says what focusChat did: Mode "swapped" (focus layout: the
// shown chat was minimized and the target took its place), "shown" (any
// other layout: exactly `gv chat show`) or "already" (the target is the
// chat on screen — nothing moved).
type focusResult struct {
	Pane string
	Row  chat.Row
	Mode string
}

const focusRemoteRefusal = "remote chats: `gv chat show @<host>/<session>` only, for now — focus switching moves local chats"

// focusChat is the one implementation behind `gv chat focus` and the
// cockpit's show keys on a CHATS row. Every refusal comes before the first
// move; then in focus the shown local chat is minimized and the target joins
// (each move followed by its event), @grove_focus_hid and the rail follow.
// Outside focus it is showChat, call for call.
func focusChat(target string, focus bool) (focusResult, error) {
	if _, _, ok := parseRemoteChatTarget(strings.TrimSpace(target)); ok {
		return focusResult{}, fmt.Errorf("%s", focusRemoteRefusal)
	}
	rec, isCockpit, err := showTarget(target)
	if err != nil {
		return focusResult{}, err
	}
	cockpit := cockpitSessionForLabel(rec.Row.Workspace)
	if tmux.CockpitLayout(cockpit) != "focus" {
		pane, row, err := showRecord(rec, isCockpit, focus)
		return focusResult{Pane: pane, Row: row, Mode: "shown"}, err
	}
	if rec.Row.Kind == chat.KindCockpit {
		if rec.Pane != "" && tmux.PaneOption(rec.Pane, "@grove_remote") != "" {
			return focusResult{}, fmt.Errorf("%s", focusRemoteRefusal)
		}
		return focusResult{Pane: rec.Pane, Row: rec.Row, Mode: "already"}, nil
	}
	if refusal := chat.ShowRefusal(rec.Row); refusal != "" {
		return focusResult{}, fmt.Errorf("%s", refusal)
	}
	// The same "not running" answer `gv chat show` gives, before anything moves.
	if _, err := tmux.ChatShowable(rec.Row.Session, cockpit, isCockpit); err != nil {
		return focusResult{}, err
	}
	label := rec.Row.Workspace
	if err := tmux.MinimizeShown(cockpit, label, isCockpit, focusMoved(label, config.StateDirAt(rec.Root))); err != nil {
		railWriteNow(label, cockpit)
		return focusResult{}, err
	}
	pane, row, err := showRecord(rec, isCockpit, focus)
	if err != nil {
		railWriteNow(label, cockpit)
		return focusResult{}, err
	}
	ferr := tmux.ForgetFocusHid(cockpit, rec.Row.Session)
	railWriteNow(label, cockpit)
	if ferr != nil {
		return focusResult{Pane: pane, Row: row, Mode: "swapped"}, fmt.Errorf("swapped to %s, but: %w", row.Session, ferr)
	}
	return focusResult{Pane: pane, Row: row, Mode: "swapped"}, nil
}

// cmdChatFocus is `gv chat focus <session>`.
func cmdChatFocus(args []string) error {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return fmt.Errorf("usage: gv chat focus <session>   (a grove-chat-<label>-<n> session, its session id, or a prefix; in the focus layout the shown chat is minimized and this one takes its place — in horizontal it is `gv chat show`)")
	}
	res, err := focusChat(strings.TrimSpace(args[0]), true)
	if err != nil {
		return err
	}
	switch res.Mode {
	case "already":
		fmt.Printf("✓ %s is already the shown chat — nothing moved\n", res.Row.Session)
	case "swapped":
		fmt.Printf("✓ focused %s — the previous chat is minimized to the rail; `gv chat hide %s` moves it off-screen\n", res.Row.Session, res.Pane)
	default:
		fmt.Printf("✓ shown %s — the cockpit is in horizontal, so this is `gv chat show`; back in the %s cockpit, `gv chat hide %s` moves it off-screen again\n", res.Row.Session, res.Row.Workspace, res.Pane)
	}
	return nil
}
