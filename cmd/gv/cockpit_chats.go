package main

// grove-402: the cockpit's CHATS box — this workspace's live chats, read
// for the dashboard on beats it already has. The same panes `gv chat ls`
// reports (chatRecords), minus everything the cockpit must not pay for or
// do: no archived scan, no project-dir listing, and NO stamping — this is
// a pure read, so a pane the process table cannot identify falls back to
// the stamp it already wears and otherwise simply has no label yet.
//
// Two passes (the cockpit RAM rule): the cheap one is exactly one tmux
// invocation and nothing else; the costly one adds `ps`, one transcript
// read per identified chat and one pane capture per running chat.

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/JollyGrin/grove/internal/chat"
	"github.com/JollyGrin/grove/internal/chatrail"
	"github.com/JollyGrin/grove/internal/chatweb"
	"github.com/JollyGrin/grove/internal/tmux"
	"github.com/JollyGrin/grove/internal/transcript"
	"github.com/JollyGrin/grove/internal/tui"
	"github.com/JollyGrin/grove/internal/workspace"
)

// cockpitChatLookup is the impure half, injected in one bundle (chatLookup's
// pattern) so a test can count exactly what each pass costs.
type cockpitChatLookup struct {
	panes     func() []tmux.LivePane // ONE `list-panes -a`
	isCockpit func() (tmux.CockpitCheck, error)
	procs     func() []chat.Proc
	capture   func(pane string) (string, error)
	// transcript reads one chat's transcript: its title, its last line and
	// when it was last written. ok=false when the file is not there.
	transcript func(path string) (label, last string, mod time.Time, ok bool)
	configDir  func(ws workspace.Workspace) string
}

// liveCockpitChatLookup is the production bundle.
func liveCockpitChatLookup() cockpitChatLookup {
	return cockpitChatLookup{
		panes:      tmux.Panes,
		isCockpit:  cockpitSessionCheck,
		procs:      scanProcs,
		capture:    tmux.CapturePane,
		transcript: readChatTranscript,
		configDir:  workspaceClaudeConfigDir,
	}
}

// cockpitChatPane is one row in the making: the pane plus where its
// transcript would live.
type cockpitChatPane struct {
	pane tmux.LivePane
	row  tui.ChatRow
}

// cockpitChats builds the CHATS box rows for one workspace. ws nil (the
// global cockpit, which owns no chats) costs nothing at all.
func cockpitChats(ws *workspace.Workspace, deep bool, look cockpitChatLookup) []tui.ChatRow {
	if ws == nil || ws.Label == "" {
		return nil
	}
	panes := look.panes()
	if len(panes) == 0 {
		return nil
	}
	found := cockpitChatPanes(*ws, panes, look.isCockpit)
	hidden := hiddenRemoteRows(*ws, panes)
	if len(found)+len(hidden) == 0 {
		return nil
	}
	if deep {
		deepenCockpitChats(*ws, found, look)
	}
	rows := make([]tui.ChatRow, len(found), len(found)+len(hidden))
	for i, f := range found {
		rows[i] = f.row
	}
	return append(rows, hidden...)
}

// hiddenRemoteRows are the workspace's hidden REMOTE chats (grove-404), read
// off the pane list already in hand: the cockpit session's record rides
// every one of its panes. A row is the record and nothing more — host,
// session, profile — on the cheap pass and the costly one alike: the cockpit
// never dials a host on a beat.
func hiddenRemoteRows(ws workspace.Workspace, panes []tmux.LivePane) []tui.ChatRow {
	cockpit := cockpitSessionForLabel(ws.Label)
	for _, p := range panes {
		if p.Session != cockpit {
			continue
		}
		if p.HiddenRemote == "" {
			return nil
		}
		recs := tmux.ParseHiddenRemotes(p.HiddenRemote)
		rows := make([]tui.ChatRow, len(recs))
		for i, r := range recs {
			rows[i] = tui.ChatRow{Session: r.Session, Hidden: true, N: chatSessionNumber(r.Session), Host: r.Host, Model: r.Profile}
		}
		return rows
	}
	return nil
}

// chatSessionNumber is the <n> of a `grove-chat-<label>-<n>` name, 0 when
// the name has none.
func chatSessionNumber(session string) int {
	i := strings.LastIndexByte(session, '-')
	if i < 0 {
		return 0
	}
	n, err := strconv.Atoi(session[i+1:])
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// cockpitChatPanes classifies the server's panes with chatRecords' own
// rules: a `grove-chat-<label>-<n>` session the registry does not claim as
// a cockpit is a hidden chat; a pane of this workspace's cockpit session
// sitting in the brain dir is a chat on screen. One addition — a cockpit
// pane wearing @grove_remote is a remote chat on screen (its agent runs on
// the host, so its cwd is not the brain dir and `gv chat ls` leaves it to
// the host's own report).
//
// The registry is read only when a session NAME looks like a chat of this
// workspace: a cockpit with no hidden chat pays for the pane list alone.
func cockpitChatPanes(ws workspace.Workspace, panes []tmux.LivePane, isCockpit func() (tmux.CockpitCheck, error)) []cockpitChatPane {
	var found []cockpitChatPane
	cockpit, orchDir := cockpitSessionForLabel(ws.Label), orchestratorDirAt(ws.Root)
	for _, p := range panes {
		if p.Session != cockpit || p.Rail {
			continue
		}
		if p.Remote == "" && !chat.IsOrchestratorPane(p.Dir, orchDir) {
			continue
		}
		found = append(found, cockpitChatPane{pane: p, row: tui.ChatRow{
			Pane: p.Pane, Session: p.Session, N: p.Index, Host: p.Remote, Model: p.Model,
			Busy: chat.Busy(p.Command), Created: p.Created, Title: chatrail.PaneTitle(p.Title),
		}})
	}
	prefix := "grove-chat-" + ws.Label + "-"
	candidate := false
	for _, p := range panes {
		if strings.HasPrefix(p.Session, prefix) {
			candidate = true
			break
		}
	}
	if !candidate || isCockpit == nil {
		nameChats(found)
		return found
	}
	check, err := isCockpit()
	if err != nil {
		// An unreadable registry cannot tell a chat from a cockpit: list
		// none rather than guess (cockpitSessionCheck's rule).
		nameChats(found)
		return found
	}
	for _, c := range tmux.ChatSessionsIn(panes, ws.Label, check) {
		found = append(found, cockpitChatPane{
			pane: tmux.LivePane{Session: c.Session, PID: c.PID, Command: c.Command, Pane: c.Pane, Dir: c.Dir, ChatSession: c.SessionID, Name: c.Name},
			row: tui.ChatRow{
				Pane: c.Pane, Session: c.Session, Hidden: true, N: c.N, Model: c.Model,
				Busy: chat.Busy(c.Command), Created: c.Created, Title: chatrail.PaneTitle(c.Title),
			},
		})
	}
	nameChats(found)
	return found
}

// nameChats gives every chat its stable name (grove-510). A pane's name
// stamp wins — it was frozen before the pane's first move, so the original
// cockpit orchestrator stays cockpit·1 after focus minimizes it into a
// grove-chat session. An unstamped pane is named by position (cockpit·<pane
// index>, chat-<session n>), unless a stamp already holds that name: then
// it takes the lowest free number of its kind. Pure, and deterministic for
// a given pane list, so every reader names a chat the same way.
func nameChats(found []cockpitChatPane) {
	// No map: a workspace holds a handful of chats, and this runs on the
	// cockpit's 1s beat (the RAM rule).
	taken := func(name string) bool {
		for i := range found {
			if found[i].row.Name == name {
				return true
			}
		}
		return false
	}
	for i := range found {
		if n := found[i].pane.Name; n != "" && !taken(n) {
			found[i].row.Name = n
		}
	}
	for i := range found {
		r := &found[i].row
		if r.Name != "" {
			continue
		}
		kind := "cockpit·"
		if r.Hidden {
			kind = "chat-"
		}
		name := kind + strconv.Itoa(r.N)
		for n := 1; taken(name); n++ {
			name = kind + strconv.Itoa(n)
		}
		r.Name = name
	}
}

// freezeChatNames stamps every unstamped local chat of the workspace with
// the name it wears right now (grove-510). Every path that moves a chat
// between the cockpit window and a grove-chat session calls it first, so
// the name rides the pane through the move instead of tracking its
// position. Remote panes are left alone: hiding one closes it. Best-effort —
// a failed stamp only means that chat is named by position.
func freezeChatNames(label string, isCockpit tmux.CockpitCheck) {
	ws, ok := labelWorkspace(label)
	if !ok {
		return
	}
	check := func() (tmux.CockpitCheck, error) { return isCockpit, nil }
	for _, s := range namesToStamp(cockpitChatPanes(ws, tmux.Panes(), check)) {
		_ = tmux.SetPaneChatName(s[0], s[1])
	}
}

// namesToStamp is freezeChatNames' pure half: the [pane, name] pairs of the
// local chats that wear no name stamp yet.
func namesToStamp(found []cockpitChatPane) [][2]string {
	var out [][2]string
	for _, f := range found {
		if f.pane.Name == "" && f.row.Host == "" && f.pane.Pane != "" {
			out = append(out, [2]string{f.pane.Pane, f.row.Name})
		}
	}
	return out
}

// labelWorkspace is the registered workspace with this label.
func labelWorkspace(label string) (workspace.Workspace, bool) {
	if label == "" {
		return workspace.Workspace{}, false
	}
	list, err := workspace.LoadRegistry()
	if err != nil {
		return workspace.Workspace{}, false
	}
	targets, err := chatWorkspaces(list, label)
	if err != nil || len(targets) != 1 {
		return workspace.Workspace{}, false
	}
	return targets[0], true
}

// deepenCockpitChats fills the costly fields in place: which conversation
// each local pane runs (the process table, else the stamp it wears), that
// transcript's title / last line / last write, and — one capture per pane
// with an agent on it — whether it is waiting on the operator.
func deepenCockpitChats(ws workspace.Workspace, found []cockpitChatPane, look cockpitChatLookup) {
	var procs []chat.Proc
	scanned := false
	configDir, resolved := "", false
	for i := range found {
		f := &found[i]
		remote := f.row.Host != ""
		if !remote {
			if !scanned && look.procs != nil {
				procs, scanned = look.procs(), true
			}
			id := chat.PaneSessionID(procs, f.pane.PID)
			if id == "" {
				id = f.pane.ChatSession
			}
			if id != "" && chat.ValidSessionID(id) && look.transcript != nil {
				if !resolved && look.configDir != nil {
					configDir, resolved = look.configDir(ws), true
				}
				path := filepath.Join(transcript.ProjectDirIn(configDir, f.pane.Dir), id+".jsonl")
				if label, last, mod, ok := look.transcript(path); ok {
					f.row.Label, f.row.Last, f.row.LastActive = label, last, mod
				}
			}
			if !f.row.Busy {
				f.row.Turn = chatweb.TurnStopped
				continue
			}
		}
		if look.capture == nil {
			continue
		}
		out, err := look.capture(f.pane.Pane)
		if err != nil || out == "" {
			continue
		}
		f.row.Waiting = chatweb.Waiting(out)
		f.row.Turn = chatweb.ClassifyTurn(out, true).State
	}
}

// chatTailBytes bounds the last-line read: the end of the file, never the
// whole transcript.
const chatTailBytes = 64 << 10

// readChatTranscript is the production transcript read.
func readChatTranscript(path string) (label, last string, mod time.Time, ok bool) {
	info, err := os.Stat(path)
	if err != nil {
		return "", "", time.Time{}, false
	}
	return chat.Label(path, ""), lastChatLine(path, info.Size()), info.ModTime(), true
}

// lastChatLine is the last line anybody SAID in the transcript at path:
// the final non-empty line of its last text entry (tool calls, thinking
// and harness chrome are skipped). Reads at most chatTailBytes off the end.
func lastChatLine(path string, size int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	start := max(size-chatTailBytes, 0)
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return ""
	}
	buf, err := io.ReadAll(io.LimitReader(f, chatTailBytes))
	if err != nil {
		return ""
	}
	lines := strings.Split(string(buf), "\n")
	if start > 0 {
		lines = lines[1:] // the seek landed mid-line
	}
	return lastSaid(lines)
}

// lastSaid is lastChatLine's pure half.
func lastSaid(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		entries := chat.NewProjector().Line([]byte(lines[i]))
		for j := len(entries) - 1; j >= 0; j-- {
			if entries[j].Kind != chat.EntryText {
				continue
			}
			said := strings.Split(strings.TrimSpace(entries[j].Text), "\n")
			if l := strings.TrimSpace(said[len(said)-1]); l != "" {
				return l
			}
		}
	}
	return ""
}

// wireChatKeys hands the cockpit the functions behind the chat verbs
// (grove-403): the CHATS row keys call these — the same code the CLI runs,
// never a second implementation and never a `gv` subprocess.
func wireChatKeys() {
	tui.HideChat = func(pane string) (string, error) { return hideChat(pane, "") }
	tui.ShowChat = func(session string, focus bool) (string, error) {
		// grove-488: in the focus layout the same key swaps.
		res, err := focusChat(session, focus)
		return res.Pane, err
	}
	tui.FocusChat = func(pane string) error {
		isCockpit, err := cockpitSessionCheck()
		if err != nil {
			return err
		}
		return tmux.FocusChatPane(pane, isCockpit)
	}
	tui.SendChat = func(session, text string) (string, error) {
		warn, _, err := relayChat(session, text)
		return warn, err
	}
	tui.CloseChatPane = func(pane string) error {
		return closeCockpitPane(pane, map[string]string{"reason": "closed from the cockpit"})
	}
	tui.CloseChatSession = func(session string) error {
		_, err := closeChat(session)
		return err
	}
	// grove-404: the remote rows. Hide needs no entry of its own — hideChat
	// tells a remote pane from a local one by its stamps.
	tui.ShowRemoteChat = func(host, session string, focus bool) (string, error) {
		pane, _, err := showRemoteChat(host, session, focus)
		return pane, err
	}
	tui.CloseRemoteChat = closeRemoteChat
}
