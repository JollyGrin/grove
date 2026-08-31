package main

// grove-215: `gv chat ls` — the join between a workspace's live chat panes
// and the Claude session ids in its orchestrator project dir, and the first
// verb of the `gv chat` contract the phone UI (grove-218) is a client of.
//
// The impure half lives here: tmux, the registry, the transcript dir, and
// the ONE mutation this command performs — stamping a resolved session id
// onto its pane as the durable user option @grove_chat_session. Every
// decision (which transcript belongs to which pane, what kind a pane is,
// whether it is writable) lives in internal/chat, table-tested without a
// tmux server.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JollyGrin/grove/internal/chat"
	"github.com/JollyGrin/grove/internal/tmux"
	"github.com/JollyGrin/grove/internal/transcript"
	"github.com/JollyGrin/grove/internal/workspace"
)

// cmdChat dispatches the `gv chat` verbs. `ls` is the only one today;
// unknown subcommands fail loudly rather than defaulting, because the later
// verbs (tail, send) are write-shaped and a typo must never fall through to
// something else.
func cmdChat(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: gv chat ls [--workspace <label>] [--json]")
	}
	switch args[0] {
	case "ls":
		return cmdChatLs(args[1:])
	default:
		return fmt.Errorf("unknown `gv chat` subcommand %q (have: ls)", args[0])
	}
}

// cmdChatLs lists every orchestrator chat this machine can see, in all
// three states. Deliberately NOT ambient-scoped by default (grove-191's
// workspace-transparent shape): a phone asking "what chats do I have" wants
// the machine's answer, and every row carries its `workspace`.
func cmdChatLs(args []string) error {
	fs := flag.NewFlagSet("chat ls", flag.ExitOnError)
	wsFlag := fs.String("workspace", "", "only this registered workspace label (default: every registered workspace)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	parseAnywhere(fs, args)

	list, err := workspace.LoadRegistry()
	if err != nil {
		return err
	}
	targets, err := chatWorkspaces(list, *wsFlag)
	if err != nil {
		return err
	}
	// The registry is what separates a cockpit session from a chat session
	// whose name merely looks like one — a hard error, never a guess
	// (cockpitSessionCheck's rule): the two readings differ by a dashboard.
	isCockpit, err := cockpitSessionCheck()
	if err != nil {
		return err
	}
	rows := chatRows(targets, tmux.Panes(), isCockpit, tmux.SetPaneChatSession)
	if *asJSON {
		return emitJSON("chats", rows)
	}
	printChatRows(rows)
	return nil
}

// chatWorkspaces resolves which registered workspaces to report on. An
// explicit --workspace that is not registered is an error (the caller named
// something that does not exist); without the flag, dead workspaces are
// skipped silently — a root that moved away has no chats and no brain dir.
func chatWorkspaces(list []workspace.Workspace, label string) ([]workspace.Workspace, error) {
	sort.Slice(list, func(i, j int) bool { return list[i].Label < list[j].Label })
	if label != "" {
		for _, ws := range list {
			if ws.Label == label {
				return []workspace.Workspace{ws}, nil
			}
		}
		return nil, fmt.Errorf("no registered workspace %q — `gv workspaces` lists them", label)
	}
	var alive []workspace.Workspace
	for _, ws := range list {
		if workspace.Alive(ws) {
			alive = append(alive, ws)
		}
	}
	return alive, nil
}

// livePane is one pane awaiting identity: everything the row needs except
// the session id, which is resolved (and stamped) below.
type livePane struct {
	ws   workspace.Workspace
	kind string
	n    int
	pane tmux.LivePane
}

// chatRows builds the whole report: live chats, cockpit orchestrator panes,
// then the archived transcripts nothing live claims.
//
// Order of operations matters and is the point of the ticket:
//
//  1. seed the claim set with every id ALREADY stamped on a pane, across
//     the whole server — an id another chat wears is never re-handed-out;
//  2. resolve the unstamped panes newest-first, so the newest chat takes
//     the newest transcript, and stamp each answer on its pane so the join
//     is decided exactly once (a pane user option survives claude's boot,
//     re-layouts, and re-attaches; a pane title would not);
//  3. whatever transcript is left over has no live pane — it is archived.
//
// stamp is injected (tmux.SetPaneChatSession in production) so the whole
// join is testable without a tmux server.
func chatRows(targets []workspace.Workspace, panes []tmux.LivePane, isCockpit tmux.CockpitCheck, stamp func(pane, id string) error) []chat.Row {
	claimed := map[string]bool{}
	for _, p := range panes {
		if p.ChatSession != "" {
			claimed[p.ChatSession] = true
		}
	}
	var pending []livePane
	for _, ws := range targets {
		orchDir := orchestratorDirAt(ws.Root)
		for _, c := range tmux.ChatSessionsIn(panes, ws.Label, isCockpit) {
			pending = append(pending, livePane{ws: ws, kind: chat.KindChat, n: c.N, pane: tmux.LivePane{
				Session: c.Session, Command: c.Command, Attached: c.Attached,
				Created: c.Created, Pane: c.Pane, Dir: c.Dir, ChatSession: c.SessionID,
			}})
		}
		cockpit := cockpitSessionForLabel(ws.Label)
		for _, p := range panes {
			if p.Session != cockpit || !chat.IsOrchestratorPane(p.Dir, orchDir) {
				continue
			}
			pending = append(pending, livePane{ws: ws, kind: chat.KindCockpit, n: p.Index, pane: p})
		}
	}
	// Newest pane first: with two unstamped chats in one project dir, the
	// newer one owns the newer transcript. session_created has one-second
	// resolution, so two chats spawned back-to-back tie — and there the
	// higher <n> is the younger, because NextChatSession hands numbers out
	// in order. (A reused slot breaks that, but a reused slot is minutes
	// old, never a tie.)
	sort.SliceStable(pending, func(i, j int) bool {
		a, b := pending[i], pending[j]
		if !a.pane.Created.Equal(b.pane.Created) {
			return a.pane.Created.After(b.pane.Created)
		}
		return a.n > b.n
	})

	scans := map[string][]transcript.Session{}
	scan := func(dir string) []transcript.Session {
		if s, ok := scans[dir]; ok {
			return s
		}
		s, err := transcript.ListSessions(dir)
		if err != nil {
			s = nil
		}
		scans[dir] = s
		return s
	}

	var rows []chat.Row
	for _, lp := range pending {
		id, label := lp.pane.ChatSession, ""
		sessions := scan(lp.pane.Dir)
		if id == "" {
			if s, ok := chat.Resolve(sessions, claimed); ok {
				id, label = s.ID, s.FirstPrompt
				claimed[id] = true
				// Best-effort: a tmux too old for the option, or a pane
				// that died between listing and stamping, must not fail
				// the whole report — the next `ls` re-resolves.
				if err := stamp(lp.pane.Pane, id); err != nil {
					fmt.Fprintf(os.Stderr, "warning: could not stamp %s with its session id: %v\n", lp.pane.Session, err)
				}
			}
		}
		if id != "" && label == "" {
			label = transcriptLabel(sessions, id)
		}
		rows = append(rows, chat.Live{
			Session: lp.pane.Session, Workspace: lp.ws.Label, N: lp.n, Kind: lp.kind,
			Command: lp.pane.Command, Attached: lp.pane.Attached, Created: lp.pane.Created,
			SessionID: id, Label: label,
		}.Row())
	}
	for _, ws := range targets {
		for _, dir := range orchestratorProjectDirs(ws) {
			for _, s := range scan(dir) {
				if s.ID == "" || claimed[s.ID] {
					continue
				}
				claimed[s.ID] = true
				rows = append(rows, chat.ArchivedRow(ws.Label, s))
			}
		}
	}
	chat.Sort(rows)
	return rows
}

// orchestratorProjectDirs is where a workspace's chats have run: its brain
// dir, plus one subdir per model profile — Claude Code keys `--continue` by
// cwd, so a profiled chat gets its own dir (grove-36 T4) and therefore its
// own project dir. Read off the filesystem rather than the config so a
// profile that has since been renamed away still surfaces its history.
func orchestratorProjectDirs(ws workspace.Workspace) []string {
	orchDir := orchestratorDirAt(ws.Root)
	dirs := []string{orchDir}
	entries, err := os.ReadDir(orchDir)
	if err != nil {
		return dirs
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dirs = append(dirs, filepath.Join(orchDir, e.Name()))
	}
	return dirs
}

// transcriptLabel is the list label of an already-stamped id: its
// transcript's FirstPrompt. An id whose .jsonl is gone keeps the id and
// loses only the label.
func transcriptLabel(sessions []transcript.Session, id string) string {
	for _, s := range sessions {
		if s.ID == id {
			return s.FirstPrompt
		}
	}
	return ""
}

// printChatRows is the human table. `?` for an unresolved id is the same
// "starting…" the UI shows — the chat exists, its transcript does not yet.
func printChatRows(rows []chat.Row) {
	if len(rows) == 0 {
		fmt.Println("no orchestrator chats — `gv orchestrator new --workspace <label>` starts one")
		return
	}
	fmt.Printf("%-14s %-24s %-9s %-10s %s\n", "WORKSPACE", "SESSION", "KIND", "SESSION", "LABEL")
	for _, r := range rows {
		id := "?"
		if r.SessionID != nil {
			id = (*r.SessionID)[:min(8, len(*r.SessionID))]
		}
		session := r.Session
		if session == "" {
			session = "—"
		}
		flags := ""
		if r.Busy {
			flags += " ●"
		}
		if r.Attached {
			flags += " ⌁"
		}
		if !r.Writable {
			flags += " (read-only)"
		}
		fmt.Printf("%-14s %-24s %-9s %-10s %s%s\n", r.Workspace, session, r.Kind, id, chatAge(r), flags)
		if r.Label != "" {
			fmt.Printf("%-14s   %s\n", "", r.Label)
		}
	}
}

// chatAge renders `created` compactly; a zero time (a pane on a tmux that
// reported none) prints nothing rather than year 1.
func chatAge(r chat.Row) string {
	if r.Created.IsZero() {
		return ""
	}
	return r.Created.Local().Format(time.RFC3339)
}
