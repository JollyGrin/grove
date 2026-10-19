package main

// grove-487: focus-mode minimizing. In the `focus` layout the cockpit window
// holds the dash and ONE chat; the others are hidden with chat-hide's
// primitives and live on as boxes in the rail. The decisions are in
// internal/tmux/focus.go; this file wires them to events, the registry and
// the rail. In `horizontal` nothing here moves a pane.

import (
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
