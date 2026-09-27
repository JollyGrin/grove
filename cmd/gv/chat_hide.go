package main

// grove-401: `gv chat hide` / `gv chat show` — move a LOCAL cockpit chat
// pane off-screen and back. Car 1 of the chat-hide train.
//
// Hidden is not a new kind of chat. The pane becomes an ordinary detached
// chat session (grove-198 naming), so `gv chat ls` reports it as kind chat,
// `send`/`close` reach it, `gv park --chats` reaps it and the phone lists
// it — all with the code they already had. Nothing here spawns anything,
// and nothing hides by itself: a chat opens as a cockpit pane exactly as it
// did before, and stays one until the operator says otherwise.
//
// The decisions live below this file: which pane may move in
// internal/tmux (hidablePane/showableChat), which row a target names in
// internal/chat (MatchHide, HideRefusal, ShowRefusal).

import (
	"fmt"
	"os"
	"strings"

	"github.com/JollyGrin/grove/internal/chat"
	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/tmux"
)

const chatHideUsage = "usage: gv chat hide [<session-id>|<pane-id>]   (a pane id is tmux's %N; no argument hides THIS pane — run it inside the chat, e.g. `!gv chat hide`)"

// hideArg turns `gv chat hide`'s arguments into what to resolve: a %pane id
// to aim at directly, or a target for the chat report. No argument means
// the calling pane, which only exists inside tmux.
func hideArg(args []string, envPane string) (pane, target string, err error) {
	switch len(args) {
	case 0:
		if strings.TrimSpace(envPane) == "" {
			return "", "", fmt.Errorf("%s\n  $TMUX_PANE is not set, so there is no \"this pane\" — name the chat (`gv chat ls` lists them)", chatHideUsage)
		}
		return strings.TrimSpace(envPane), "", nil
	case 1:
		arg := strings.TrimSpace(args[0])
		if arg == "" {
			return "", "", fmt.Errorf("%s", chatHideUsage)
		}
		if strings.HasPrefix(arg, "%") {
			return arg, "", nil
		}
		return "", arg, nil
	default:
		return "", "", fmt.Errorf("%s", chatHideUsage)
	}
}

// hideRecord resolves hideArg's answer against a chat report: the record of
// the pane to hide. A %pane no row claims is not an orchestrator pane —
// the report lists every one of those (chat.IsOrchestratorPane).
func hideRecord(recs []chatRecord, pane, target string) (chatRecord, error) {
	if pane != "" {
		for _, r := range recs {
			if r.Pane == pane {
				return r, nil
			}
		}
		return chatRecord{}, fmt.Errorf("%s is not an orchestrator chat pane of any registered workspace — only a cockpit's chat panes can be hidden (`gv chat ls` lists them)", pane)
	}
	rows := make([]chat.Row, len(recs))
	for i, r := range recs {
		rows[i] = r.Row
	}
	i, err := chat.MatchHide(rows, target)
	if err != nil {
		return chatRecord{}, err
	}
	return recs[i], nil
}

// chatMoveEvent is the record both verbs append: the chat session, the
// pane's immutable id, the workspace, and the Claude session id when the
// pane wears one.
func chatMoveEvent(kind, session, pane string, row chat.Row) state.Event {
	data := map[string]string{"session": session, "pane": pane, "workspace": row.Workspace}
	if row.SessionID != nil && *row.SessionID != "" {
		data["session_id"] = *row.SessionID
	}
	return state.Event{Type: kind, Data: data}
}

// cmdChatHide moves a cockpit chat pane into its own detached session.
//
// Order: every refusal first (tmux's view of the pane, then the report's),
// then the event, then the move. The event is appended from inside
// tmux.HideChatPane — after the session name is reserved, before the pane
// leaves the cockpit — because this verb is run from inside the pane it
// moves.
func cmdChatHide(args []string) error {
	pane, target, err := hideArg(args, os.Getenv("TMUX_PANE"))
	if err != nil {
		return err
	}
	isCockpit, err := cockpitSessionCheck()
	if err != nil {
		return err
	}
	// A %pane is asked about directly first: tmux knows a dashboard, a
	// worker window and a remote attachment from a chat, and says which —
	// the report would only say "no such row".
	if pane != "" {
		if _, err := tmux.PaneHidable(pane, isCockpit); err != nil {
			return err
		}
	}
	recs, err := chatReport()
	if err != nil {
		return err
	}
	rec, err := hideRecord(recs, pane, target)
	if err != nil {
		return err
	}
	if refusal := chat.HideRefusal(rec.Row); refusal != "" {
		return fmt.Errorf("%s", refusal)
	}
	if rec.Pane == "" {
		return fmt.Errorf("%s has no live pane to hide", chatName(rec.Row))
	}
	session, err := tmux.HideChatPane(rec.Pane, rec.Row.Workspace, isCockpit, func(session string) error {
		return state.Append(config.StateDirAt(rec.Root), chatMoveEvent(state.EvChatHidden, session, rec.Pane, rec.Row))
	})
	if err != nil {
		if session != "" {
			return fmt.Errorf("hid the chat as %s, but: %w", session, err)
		}
		return err
	}
	fmt.Printf("✓ hidden as %s — still running; `gv chat show %s` brings it back\n", session, session)
	return nil
}

// cmdChatShow joins a detached chat into its OWN workspace's cockpit window
// — any detached chat of the workspace, including one the phone spawned
// that was never in the cockpit.
func cmdChatShow(args []string) error {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return fmt.Errorf("usage: gv chat show <session>   (a grove-chat-<label>-<n> session, or its session id; joins it into its workspace's cockpit)")
	}
	isCockpit, err := cockpitSessionCheck()
	if err != nil {
		return err
	}
	rec, err := findChat(args[0])
	if err != nil {
		return err
	}
	if refusal := chat.ShowRefusal(rec.Row); refusal != "" {
		return fmt.Errorf("%s", refusal)
	}
	session := rec.Row.Session
	pane, err := tmux.ShowChatPane(session, cockpitSessionForLabel(rec.Row.Workspace), isCockpit, func(pane string) error {
		return state.Append(config.StateDirAt(rec.Root), chatMoveEvent(state.EvChatShown, session, pane, rec.Row))
	})
	if err != nil {
		if pane != "" {
			return fmt.Errorf("showed %s, but: %w", session, err)
		}
		return err
	}
	fmt.Printf("✓ shown %s — back in the %s cockpit; `gv chat hide %s` moves it off-screen again\n", session, rec.Row.Workspace, pane)
	return nil
}
