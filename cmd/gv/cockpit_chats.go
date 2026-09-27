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
	"strings"
	"time"

	"github.com/JollyGrin/grove/internal/chat"
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
	if len(found) == 0 {
		return nil
	}
	if deep {
		deepenCockpitChats(*ws, found, look)
	}
	rows := make([]tui.ChatRow, len(found))
	for i, f := range found {
		rows[i] = f.row
	}
	return rows
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
		if p.Session != cockpit {
			continue
		}
		if p.Remote == "" && !chat.IsOrchestratorPane(p.Dir, orchDir) {
			continue
		}
		found = append(found, cockpitChatPane{pane: p, row: tui.ChatRow{
			Pane: p.Pane, Session: p.Session, N: p.Index, Host: p.Remote, Model: p.Model,
			Busy: chat.Busy(p.Command), Created: p.Created,
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
		return found
	}
	check, err := isCockpit()
	if err != nil {
		// An unreadable registry cannot tell a chat from a cockpit: list
		// none rather than guess (cockpitSessionCheck's rule).
		return found
	}
	for _, c := range tmux.ChatSessionsIn(panes, ws.Label, check) {
		found = append(found, cockpitChatPane{
			pane: tmux.LivePane{Session: c.Session, PID: c.PID, Command: c.Command, Pane: c.Pane, Dir: c.Dir, ChatSession: c.SessionID},
			row: tui.ChatRow{
				Pane: c.Pane, Session: c.Session, Hidden: true, N: c.N, Model: c.Model,
				Busy: chat.Busy(c.Command), Created: c.Created,
			},
		})
	}
	return found
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
		pane, _, err := showChat(session, focus)
		return pane, err
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
}
