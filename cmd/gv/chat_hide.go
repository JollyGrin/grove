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
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/JollyGrin/grove/internal/chat"
	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/remote"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/tmux"
	"github.com/JollyGrin/grove/internal/workspace"
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
func cmdChatHide(args []string) error {
	pane, target, err := hideArg(args, os.Getenv("TMUX_PANE"))
	if err != nil {
		return err
	}
	session, err := hideChat(pane, target)
	if err != nil {
		return err
	}
	if host, remoteSession, ok := parseRemoteChatTarget(session); ok {
		fmt.Printf("✓ hidden %s — the attach pane is closed, the chat keeps running on %s; `gv chat show %s` re-attaches\n", session, host, remoteChatTarget(host, remoteSession))
		return nil
	}
	fmt.Printf("✓ hidden as %s — still running; `gv chat show %s` brings it back\n", session, session)
	return nil
}

// hideChat is the one implementation behind `gv chat hide` and the
// cockpit's `h` (grove-403): a %pane id, or a target for the chat report.
//
// Order: every refusal first (tmux's view of the pane, then the report's),
// then the event, then the move. The event is appended from inside
// tmux.HideChatPane — after the session name is reserved, before the pane
// leaves the cockpit — because the verb is run from inside the pane it
// moves.
func hideChat(pane, target string) (string, error) {
	isCockpit, err := cockpitSessionCheck()
	if err != nil {
		return "", err
	}
	// A %pane is asked about directly first: tmux knows a dashboard, a
	// worker window and a remote attachment from a chat, and says which —
	// the report would only say "no such row".
	if pane != "" {
		facts, err := tmux.PaneHidable(pane, isCockpit)
		if err != nil {
			return "", err
		}
		if facts.Remote != "" {
			return hideRemoteChat(pane, facts, isCockpit)
		}
	}
	recs, err := chatReport()
	if err != nil {
		return "", err
	}
	rec, err := hideRecord(recs, pane, target)
	if err != nil {
		return "", err
	}
	if refusal := chat.HideRefusal(rec.Row); refusal != "" {
		return "", fmt.Errorf("%s", refusal)
	}
	if rec.Pane == "" {
		return "", fmt.Errorf("%s has no live pane to hide", chatName(rec.Row))
	}
	session, err := tmux.HideChatPane(rec.Pane, rec.Row.Workspace, isCockpit, func(session string) error {
		return state.Append(config.StateDirAt(rec.Root), chatMoveEvent(state.EvChatHidden, session, rec.Pane, rec.Row))
	})
	if err != nil {
		if session != "" {
			return "", fmt.Errorf("hid the chat as %s, but: %w", session, err)
		}
		return "", err
	}
	return session, nil
}

// cmdChatShow joins a detached chat into its OWN workspace's cockpit window
// — any detached chat of the workspace, including one the phone spawned
// that was never in the cockpit.
func cmdChatShow(args []string) error {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return fmt.Errorf("usage: gv chat show <session>|@<host>/<session>   (a grove-chat-<label>-<n> session, or its session id; joins it into its workspace's cockpit — @<host>/<session> re-attaches a hidden REMOTE chat)")
	}
	if host, session, ok := parseRemoteChatTarget(strings.TrimSpace(args[0])); ok {
		pane, warnings, err := showRemoteChat(host, session, true)
		for _, w := range warnings {
			fmt.Fprintf(os.Stderr, "warning: %s\n", w)
		}
		if err != nil {
			return err
		}
		fmt.Printf("✓ shown %s — attached in pane %s; `gv chat hide %s` closes the attachment again\n", remoteChatTarget(host, session), pane, pane)
		return nil
	}
	pane, row, err := showChat(args[0], true)
	if err != nil {
		return err
	}
	fmt.Printf("✓ shown %s — back in the %s cockpit; `gv chat hide %s` moves it off-screen again\n", row.Session, row.Workspace, pane)
	return nil
}

// showChat is the one implementation behind `gv chat show` and the
// cockpit's `h` / `enter` on a hidden row (grove-403). focus=true is the
// verb's behavior: the shown pane takes the keyboard. focus=false leaves
// the cockpit window's active pane where it was.
func showChat(target string, focus bool) (string, chat.Row, error) {
	isCockpit, err := cockpitSessionCheck()
	if err != nil {
		return "", chat.Row{}, err
	}
	rec, err := findChat(target)
	if err != nil {
		return "", chat.Row{}, err
	}
	if refusal := chat.ShowRefusal(rec.Row); refusal != "" {
		return "", chat.Row{}, fmt.Errorf("%s", refusal)
	}
	session := rec.Row.Session
	show := tmux.ShowChatPaneUnfocused
	if focus {
		show = tmux.ShowChatPane
	}
	pane, err := show(session, cockpitSessionForLabel(rec.Row.Workspace), isCockpit, func(pane string) error {
		return state.Append(config.StateDirAt(rec.Root), chatMoveEvent(state.EvChatShown, session, pane, rec.Row))
	})
	if err != nil {
		if pane != "" {
			return "", chat.Row{}, fmt.Errorf("showed %s, but: %w", session, err)
		}
		return "", chat.Row{}, err
	}
	return pane, rec.Row, nil
}

// --- grove-404: remote chats ---
//
// A remote chat's cockpit pane is a local `ssh -t <host> tmux attach`; the
// chat lives on the host. Hide closes that attachment and records the chat
// on the cockpit session (internal/tmux/remote_hide.go); show opens a new
// attachment. Neither sends the host anything.

// remoteChatTarget names a remote chat on the command line: `@host/session`.
// The host is part of the name because the session alone is not one — the
// host numbers its chats `grove-chat-<label>-<n>` exactly as this machine
// numbers its own.
func remoteChatTarget(host, session string) string { return "@" + host + "/" + session }

// parseRemoteChatTarget is remoteChatTarget's inverse; ok=false for anything
// else, a local session name included.
func parseRemoteChatTarget(target string) (host, session string, ok bool) {
	rest, isRemote := strings.CutPrefix(target, "@")
	if !isRemote {
		return "", "", false
	}
	host, session, found := strings.Cut(rest, "/")
	if !found || host == "" || session == "" {
		return "", "", false
	}
	return host, session, true
}

// cockpitWorkspace resolves a cockpit session to its workspace's label and
// state dir — by registry, never by taking the name apart (grove-199). The
// global cockpit (`grove`) has no label and logs to the global state dir.
func cockpitWorkspace(session string) (label, stateDirPath string, err error) {
	if session == "grove" {
		return "", config.StateDirAt(""), nil
	}
	list, err := workspace.LoadRegistry()
	if err != nil {
		return "", "", err
	}
	for _, ws := range list {
		if cockpitSessionForLabel(ws.Label) == session {
			return ws.Label, config.StateDirAt(ws.Root), nil
		}
	}
	return "", "", fmt.Errorf("session %q is not a registered workspace's cockpit (`gv workspaces` lists the workspaces)", session)
}

// remoteChatMoveEvent is chat_hidden / chat_shown for a remote chat: the
// local chat's keys, plus `host`. `session` is the chat's session ON that
// host; `pane` is the local attach pane.
func remoteChatMoveEvent(kind, host, session, pane, label string) state.Event {
	return state.Event{Type: kind, Data: map[string]string{
		"session": session, "pane": pane, "workspace": label, "host": host,
	}}
}

// hideRemoteChat is hideChat's remote branch. Returns the chat's
// `@host/session` name.
func hideRemoteChat(pane string, facts tmux.PaneFacts, isCockpit tmux.CockpitCheck) (string, error) {
	label, dir, err := cockpitWorkspace(facts.Session)
	if err != nil {
		return "", err
	}
	rec, err := tmux.HideRemotePane(pane, isCockpit, func(r tmux.HiddenRemote) error {
		return state.Append(dir, remoteChatMoveEvent(state.EvChatHidden, r.Host, r.Session, pane, label))
	})
	if rec.Host == "" {
		return "", err
	}
	name := remoteChatTarget(rec.Host, rec.Session)
	if err != nil {
		return "", fmt.Errorf("hid %s, but: %w", name, err)
	}
	return name, nil
}

// showRemoteChat is the one implementation behind `gv chat show
// @host/session` and the cockpit's `h` / `enter` on a hidden remote row: a
// fresh attach pane in THIS workspace's cockpit. Warnings are returned, not
// written — the cockpit calls this from inside the tea loop.
func showRemoteChat(host, session string, focus bool) (pane string, warnings []string, err error) {
	if ambient.ws == nil || ambient.ws.Label == "" {
		return "", nil, fmt.Errorf("@%s: no ambient workspace — a hidden remote chat belongs to the cockpit it was hidden from, so run this from inside that workspace", host)
	}
	cfg, err := loadCfg()
	if err != nil {
		return "", nil, err
	}
	h, err := cfg.Host(host)
	if err != nil {
		return "", nil, err
	}
	isCockpit, err := cockpitSessionCheck()
	if err != nil {
		return "", nil, err
	}
	label, root := ambient.ws.Label, ambient.ws.Root
	pane, warnings, err = tmux.ShowRemotePane(cockpitSessionForLabel(label), host, session,
		remoteChatAttachCmd(h.SSH, session), root, focus, isCockpit)
	if pane == "" {
		return "", nil, err
	}
	if aerr := state.Append(config.StateDirAt(root), remoteChatMoveEvent(state.EvChatShown, host, session, pane, label)); aerr != nil && err == nil {
		err = aerr
	}
	if err != nil {
		return pane, warnings, fmt.Errorf("showed %s, but: %w", remoteChatTarget(host, session), err)
	}
	return pane, warnings, nil
}

// closeRemoteChat is the cockpit's `x` on a remote row: end the chat ON its
// host through the relay `gv chat close <s> --host <h>` already is, then
// drop what is left of it here — the attach pane of a shown chat, the record
// of a hidden one. pane names a shown chat (its host and session are read
// off the pane's stamps, never trusted from the caller); "" a hidden one.
//
// A host that could not be reached, or that refused, changes nothing here:
// a row that vanished while its agent ran on would be a chat nobody can see.
func closeRemoteChat(host, session, pane string) error {
	isCockpit, err := cockpitSessionCheck()
	if err != nil {
		return err
	}
	if pane != "" {
		facts, err := tmux.RemotePane(pane, isCockpit)
		if err != nil {
			return err
		}
		host, session = facts.Remote, facts.RemoteSession
	} else if ambient.ws == nil || ambient.ws.Label == "" {
		return fmt.Errorf("@%s: no ambient workspace", host)
	}
	cfg, err := loadCfg()
	if err != nil {
		return err
	}
	var out, errBuf bytes.Buffer
	code, err := remote.RunDetached(cfg, host, "chat", []string{"close", session}, &out, &errBuf)
	if err != nil {
		return fmt.Errorf("@%s: %v", host, err)
	}
	if code != 0 {
		if line := firstNonEmptyLine(errBuf.String()); line != "" {
			return fmt.Errorf("@%s: %s", host, strings.TrimPrefix(line, "gv: "))
		}
		return fmt.Errorf("@%s: chat close failed (exit %d) — nothing closed here", host, code)
	}
	if pane == "" {
		return tmux.ForgetHiddenRemote(cockpitSessionForLabel(ambient.ws.Label), host, session)
	}
	return closeCockpitPane(pane, map[string]string{"reason": "closed from the cockpit", "host": host, "session": session})
}
