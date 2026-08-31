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
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JollyGrin/grove/internal/chat"
	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/tmux"
	"github.com/JollyGrin/grove/internal/transcript"
	"github.com/JollyGrin/grove/internal/workspace"
)

// cmdChat dispatches the `gv chat` verbs. An unknown subcommand fails
// loudly rather than defaulting to anything: half these verbs are
// write-shaped (they paste into a live agent), and a typo must never fall
// through to one of them.
func cmdChat(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: gv chat ls|tail|send|keys|restamp|serve …\n  gv chat ls [--workspace <label>] [--json]\n  gv chat tail <session> [--follow] [--since <n>]\n  gv chat send <session> \"<text>\"\n  gv chat keys <session> <chars>\n  gv chat restamp <session> [<session-id>]\n  gv chat serve [--port 3000] [--bind 127.0.0.1]")
	}
	switch args[0] {
	case "ls":
		return cmdChatLs(args[1:])
	case "tail":
		return cmdChatTail(args[1:])
	case "send":
		return cmdChatSend(args[1:])
	case "keys":
		return cmdChatKeys(args[1:])
	case "restamp":
		return cmdChatRestamp(args[1:])
	case "serve":
		return cmdChatServe(args[1:])
	default:
		return fmt.Errorf("unknown `gv chat` subcommand %q (have: ls, tail, send, keys, restamp, serve)", args[0])
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
	rows := chatRows(targets, liveChatLookup(isCockpit))
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
	ws        workspace.Workspace
	configDir string // ws's Claude config dir ("" = ambient default)
	kind      string
	n         int
	pane      tmux.LivePane
}

// chatRecord is one report row PLUS the two impure handles the other `gv
// chat` verbs need and the contract row deliberately does not carry: the
// pane to paste into, and the cwd whose encoded project dir holds the
// transcript. `ls` throws both away; `tail`/`send`/`keys` ARE that join.
type chatRecord struct {
	Row  chat.Row
	Pane string // %id — the immutable paste target, "" for an archived row
	Dir  string // the chat's cwd, which is its transcript project-dir key
	PID  int    // the pane's process — the root of the walk that finds its agent
	// ConfigDir is the Claude config dir Dir's project dir lives under —
	// this chat's WORKSPACE answer, resolved once here and carried rather
	// than re-derived (grove-227). "" is the ambient default, which is every
	// workspace but one.
	ConfigDir string
}

// workspaceClaudeConfigDir resolves a workspace's claude_config_dir: the
// dir whose projects/ holds the transcripts of agents launched by THIS
// workspace's orchestrator command. Empty for every workspace that does not
// set the key (the whole fleet bar thegrid, whose orchestrator is ccwork),
// and empty when the config cannot be loaded at all — the reader degrades to
// today's ambient path, never to an error.
func workspaceClaudeConfigDir(ws workspace.Workspace) string {
	cfg, err := config.LoadAt(ws.Root)
	if err != nil {
		return ""
	}
	return cfg.ClaudeConfigDir
}

// configDirResolver memoizes workspaceClaudeConfigDir per workspace root:
// one report touches each workspace's config once, however many panes and
// project dirs it holds.
func configDirResolver() func(workspace.Workspace) string {
	seen := map[string]string{}
	return func(ws workspace.Workspace) string {
		if d, ok := seen[ws.Root]; ok {
			return d
		}
		d := workspaceClaudeConfigDir(ws)
		seen[ws.Root] = d
		return d
	}
}

// chatLookup is the impure half of the report, injected in one bundle so
// the whole join is testable without a tmux server or a process table: the
// server's panes, the registry's cockpit test, the stamp writer, and the
// process table the grove-222 ground-truth pass reads.
type chatLookup struct {
	panes     []tmux.LivePane
	isCockpit tmux.CockpitCheck
	stamp     func(pane, id string) error
	// procs is scanned LAZILY and at most once per report: `ps` is one exec,
	// and a report with no live pane needs none at all.
	procs func() []chat.Proc
}

// liveChatLookup is the production bundle: this machine's tmux server and
// its process table.
func liveChatLookup(isCockpit tmux.CockpitCheck) chatLookup {
	return chatLookup{
		panes:     tmux.Panes(),
		isCockpit: isCockpit,
		stamp:     tmux.SetPaneChatSession,
		procs:     scanProcs,
	}
}

// scanProcs reads the machine's process table in the two columns the chat
// join needs plus argv. A `ps` that fails is an empty table, not an error —
// the report degrades to "no ground truth", never to a wrong pairing.
func scanProcs() []chat.Proc {
	out, err := exec.Command("ps", "-Ao", "pid,ppid,args").Output()
	if err != nil {
		return nil
	}
	return chat.ParseProcs(string(out))
}

// chatRows is the `ls` projection: records without their handles.
func chatRows(targets []workspace.Workspace, look chatLookup) []chat.Row {
	recs := chatRecords(targets, look)
	rows := make([]chat.Row, 0, len(recs))
	for _, r := range recs {
		rows = append(rows, r.Row)
	}
	return rows
}

// chatRecords builds the whole report: live chats, cockpit orchestrator
// panes, then the archived transcripts nothing live claims.
//
// Order of operations matters and is the point of grove-222:
//
//  1. GROUND TRUTH FIRST. Every live pane is asked what its agent was
//     actually launched on (chat.PaneSessionID, off the process table). That
//     answer beats whatever the pane is wearing, so a pane mis-stamped by
//     the old mtime resolver corrects itself on the next `ls` instead of
//     staying wrong forever;
//  2. seed the claim set with every id now spoken for, across the whole
//     server — an id another chat wears is never re-handed-out;
//  3. only then resolve the panes that still have no id, and only where
//     exactly ONE unstamped pane competes for a project dir (chat.Resolve).
//     Rivals stay null: ordering them by transcript mtime is the bug;
//  4. whatever transcript is left over has no live pane — it is archived.
//
// Everything impure arrives in look, so the whole join is table-tested
// without a tmux server or a real `ps`.
func chatRecords(targets []workspace.Workspace, look chatLookup) []chatRecord {
	configDirOf := configDirResolver()
	var pending []livePane
	for _, ws := range targets {
		orchDir := orchestratorDirAt(ws.Root)
		cfgDir := configDirOf(ws)
		for _, c := range tmux.ChatSessionsIn(look.panes, ws.Label, look.isCockpit) {
			pending = append(pending, livePane{ws: ws, configDir: cfgDir, kind: chat.KindChat, n: c.N, pane: tmux.LivePane{
				Session: c.Session, PID: c.PID, Command: c.Command, Attached: c.Attached,
				Created: c.Created, Pane: c.Pane, Dir: c.Dir, ChatSession: c.SessionID,
			}})
		}
		cockpit := cockpitSessionForLabel(ws.Label)
		for _, p := range look.panes {
			if p.Session != cockpit || !chat.IsOrchestratorPane(p.Dir, orchDir) {
				continue
			}
			pending = append(pending, livePane{ws: ws, configDir: cfgDir, kind: chat.KindCockpit, n: p.Index, pane: p})
		}
	}
	// Deterministic order so two runs over the same server produce the same
	// report. Nothing depends on it any more — the panes no longer RACE for
	// transcripts — which is exactly why the old newest-pane-first sort (and
	// the false assumption in its comment) is gone.
	sort.SliceStable(pending, func(i, j int) bool {
		a, b := pending[i], pending[j]
		if a.ws.Label != b.ws.Label {
			return a.ws.Label < b.ws.Label
		}
		if a.kind != b.kind {
			return a.kind < b.kind
		}
		return a.n < b.n
	})

	procs := procScanner(look.procs, len(pending) > 0)
	for i := range pending {
		lp := &pending[i]
		id := chat.PaneSessionID(procs(), lp.pane.PID)
		if id == "" || id == lp.pane.ChatSession {
			continue
		}
		// The running process disagrees with the stamp (or the pane wears
		// none): believe the process, and write the correction back so every
		// later reader — `tail`, `send`, the phone — sees it too.
		lp.pane.ChatSession = id
		stampChatPane(look.stamp, lp.pane.Pane, lp.pane.Session, id)
	}

	// The claim set: every id spoken for right now. Panes this report is
	// building rows for contribute their CORRECTED id — never the stale one
	// they were wearing, which would otherwise keep a transcript that nobody
	// is running out of the archived list forever.
	reported := map[string]bool{}
	claimed := map[string]bool{}
	for _, lp := range pending {
		reported[lp.pane.Pane] = true
		if lp.pane.ChatSession != "" {
			claimed[lp.pane.ChatSession] = true
		}
	}
	for _, p := range look.panes {
		if p.ChatSession != "" && !reported[p.Pane] {
			claimed[p.ChatSession] = true
		}
	}
	// How many still-unidentified panes compete for each project dir: one is
	// answerable, more than one is a guess (chat.Resolve refuses).
	rivals := map[string]int{}
	for _, lp := range pending {
		if lp.pane.ChatSession == "" {
			rivals[lp.pane.Dir]++
		}
	}

	// Keyed on the PAIR: the same cwd read under two config dirs is two
	// different transcript sets, and a workspace's dir is only ever its own.
	scans := map[[2]string][]transcript.Session{}
	scan := func(configDir, dir string) []transcript.Session {
		key := [2]string{configDir, dir}
		if s, ok := scans[key]; ok {
			return s
		}
		s, err := transcript.ListSessionsIn(configDir, dir)
		if err != nil {
			s = nil
		}
		scans[key] = s
		return s
	}

	var recs []chatRecord
	for _, lp := range pending {
		id, label := lp.pane.ChatSession, ""
		sessions := scan(lp.configDir, lp.pane.Dir)
		if id == "" {
			if s, ok := chat.Resolve(sessions, claimed, rivals[lp.pane.Dir]); ok {
				id, label = s.ID, s.FirstPrompt
				claimed[id] = true
				stampChatPane(look.stamp, lp.pane.Pane, lp.pane.Session, id)
			}
		}
		if id != "" && label == "" {
			label = transcriptLabel(sessions, id)
		}
		recs = append(recs, chatRecord{Row: chat.Live{
			Session: lp.pane.Session, Workspace: lp.ws.Label, N: lp.n, Kind: lp.kind,
			Command: lp.pane.Command, Attached: lp.pane.Attached, Created: lp.pane.Created,
			SessionID: id, Label: label,
		}.Row(), Pane: lp.pane.Pane, Dir: lp.pane.Dir, PID: lp.pane.PID, ConfigDir: lp.configDir})
	}
	for _, ws := range targets {
		cfgDir := configDirOf(ws)
		for _, dir := range orchestratorProjectDirs(ws) {
			for _, s := range scan(cfgDir, dir) {
				if s.ID == "" || claimed[s.ID] {
					continue
				}
				claimed[s.ID] = true
				recs = append(recs, chatRecord{Row: chat.ArchivedRow(ws.Label, s), Dir: dir, ConfigDir: cfgDir})
			}
		}
	}
	sort.SliceStable(recs, func(i, j int) bool { return chat.Less(recs[i].Row, recs[j].Row) })
	return recs
}

// procScanner memoizes the process-table read so one report shells out to
// `ps` at most once — and not at all when there is no live pane to identify
// or no scanner injected.
func procScanner(scan func() []chat.Proc, wanted bool) func() []chat.Proc {
	var procs []chat.Proc
	done := false
	return func() []chat.Proc {
		if done || !wanted || scan == nil {
			return procs
		}
		done = true
		procs = scan()
		return procs
	}
}

// stampChatPane writes a resolved id onto its pane. Best-effort: a tmux too
// old for the option, or a pane that died between listing and stamping, must
// not fail the whole report — the next `ls` re-derives.
func stampChatPane(stamp func(pane, id string) error, pane, session, id string) {
	if stamp == nil || pane == "" {
		return
	}
	if err := stamp(pane, id); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not stamp %s with its session id: %v\n", session, err)
	}
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
