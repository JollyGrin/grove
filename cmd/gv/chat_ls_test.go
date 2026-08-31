package main

// grove-215: the join, end to end without a tmux server — panes in, rows
// out, stamps recorded. Two chats in ONE workspace share one project dir and
// must come back with DISTINCT, STABLE ids.
//
// grove-222 rewrote what "distinct and stable" is allowed to mean. The first
// three tests are the ones that ticket exists for: a pair that mtime order
// gets BACKWARDS (younger pane idle, older pane working), the correction of
// a stamp that is already wrong, and the refusal to pair rivals at all when
// nothing but recency could tell them apart.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/chat"
	"github.com/JollyGrin/grove/internal/tmux"
	"github.com/JollyGrin/grove/internal/transcript"
	"github.com/JollyGrin/grove/internal/workspace"
)

// writeTranscript lays down one Claude Code session .jsonl in the project
// dir of cwd, exactly as claude would: id = filename, cwd on every entry.
func writeTranscript(t *testing.T, cwd, id, prompt string, mod time.Time) {
	t.Helper()
	dir := transcript.ProjectDir(cwd)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(map[string]any{
		"type": "user", "cwd": cwd, "gitBranch": "main",
		"message": map[string]any{"role": "user", "content": prompt},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

// chatFixture stands up a scratch workspace + Claude config dir and returns
// the workspace and its brain dir.
func chatFixture(t *testing.T) (workspace.Workspace, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("GV_CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "claude"))
	orch := filepath.Join(root, ".grove", "orchestrator")
	if err := os.MkdirAll(orch, 0o755); err != nil {
		t.Fatal(err)
	}
	return workspace.Workspace{Root: root, Label: "unbrewed", Scope: "repo"}, orch
}

// never is the injected registry answer "no session here is a cockpit".
var neverCockpit = tmux.CockpitCheck(func(string) bool { return false })

// recordStamps collects what would have been written to @grove_chat_session.
func recordStamps() (map[string]string, func(pane, id string) error) {
	stamps := map[string]string{}
	return stamps, func(pane, id string) error { stamps[pane] = id; return nil }
}

// look bundles the injected halves for a report with NO process table — the
// pre-grove-222 world, where nothing but the panes themselves can be asked.
func look(panes []tmux.LivePane, isCockpit tmux.CockpitCheck, stamp func(pane, id string) error) chatLookup {
	return chatLookup{panes: panes, isCockpit: isCockpit, stamp: stamp}
}

// lookProcs is look plus a fake process table: the ground truth a live pane's
// agent carries in its argv.
func lookProcs(panes []tmux.LivePane, isCockpit tmux.CockpitCheck, stamp func(pane, id string) error, procs []chat.Proc) chatLookup {
	l := look(panes, isCockpit, stamp)
	l.procs = func() []chat.Proc { return procs }
	return l
}

// The grove-222 acceptance case, and the one that FAILS under mtime pairing:
// two chats in ONE project dir, the YOUNGER pane idle since its last write
// and the OLDER pane still working. Recency says the older pane's transcript
// is the newest — which is exactly why the old resolver, sorting panes
// newest-first, handed it to the younger one and inverted the pair.
//
// Here each pane's claude carries the id it was launched on, and that is
// what decides.
func TestChatRowsPairsByGroundTruthNotMtime(t *testing.T) {
	ws, orch := chatFixture(t)
	now := time.Now()
	// chat-1 is OLDER but still working: its transcript is the newest file.
	writeTranscript(t, orch, "1111-active", "triage the artgen backlog", now.Add(-1*time.Minute))
	// chat-2 is YOUNGER but went idle 15 minutes ago.
	writeTranscript(t, orch, "2222-idle", "write the release notes", now.Add(-15*time.Minute))

	panes := []tmux.LivePane{
		{Session: "grove-chat-unbrewed-1", Pane: "%7", PID: 100, Dir: orch, Command: "claude", Created: time.Unix(1700000100, 0)},
		{Session: "grove-chat-unbrewed-2", Pane: "%8", PID: 200, Dir: orch, Command: "claude", Created: time.Unix(1700000200, 0)},
	}
	procs := []chat.Proc{
		{PID: 100, PPID: 1, Args: "-bash"},
		{PID: 101, PPID: 100, Args: "claude --add-dir /x --session-id 1111-active"},
		{PID: 200, PPID: 1, Args: "-bash"},
		{PID: 201, PPID: 200, Args: "claude --add-dir /x --session-id 2222-idle"},
	}
	stamps, stamp := recordStamps()
	rows := chatRows([]workspace.Workspace{ws}, lookProcs(panes, neverCockpit, stamp, procs))

	var chats []chat.Row
	for _, r := range rows {
		if r.Kind == chat.KindChat {
			chats = append(chats, r)
		}
	}
	if len(chats) != 2 {
		t.Fatalf("got %d chat rows, want 2: %+v", len(chats), rows)
	}
	// Under mtime pairing chat 2 (the newer pane) took 1111-active. It must
	// now hold the conversation its own process is speaking into.
	if chats[0].SessionID == nil || *chats[0].SessionID != "1111-active" {
		t.Errorf("chat 1 (older pane, still working) = %v, want 1111-active", chats[0].SessionID)
	}
	if chats[1].SessionID == nil || *chats[1].SessionID != "2222-idle" {
		t.Errorf("chat 2 (younger pane, idle) = %v, want 2222-idle", chats[1].SessionID)
	}
	if chats[0].Label != "triage the artgen backlog" || chats[1].Label != "write the release notes" {
		t.Errorf("labels follow the pairing: %q / %q", chats[0].Label, chats[1].Label)
	}
	if stamps["%7"] != "1111-active" || stamps["%8"] != "2222-idle" {
		t.Errorf("panes not stamped with the ids their agents run on: %v", stamps)
	}

	// STABLE across calls, and re-stamping a pane that already agrees is not
	// work the report does twice.
	panes[0].ChatSession, panes[1].ChatSession = stamps["%7"], stamps["%8"]
	again, reStamp := recordStamps()
	rows2 := chatRows([]workspace.Workspace{ws}, lookProcs(panes, neverCockpit, reStamp, procs))
	for i, r := range rows2 {
		if r.Kind != chat.KindChat {
			continue
		}
		if *r.SessionID != *chats[i].SessionID {
			t.Errorf("row %d id moved between calls: %q then %q", i, *chats[i].SessionID, *r.SessionID)
		}
	}
	if len(again) != 0 {
		t.Errorf("a pane already wearing the right id must never be re-stamped: %v", again)
	}
}

// A stamp that disagrees with the running process is CORRECTED — the fix for
// the pairs grove-215 already inverted on the operator's machine, which are
// durable and would otherwise stay wrong forever.
func TestChatRowsCorrectsAWrongStamp(t *testing.T) {
	ws, orch := chatFixture(t)
	now := time.Now()
	writeTranscript(t, orch, "1111-active", "triage the artgen backlog", now.Add(-1*time.Minute))
	writeTranscript(t, orch, "2222-idle", "write the release notes", now.Add(-15*time.Minute))

	// Exactly the live inversion from the ticket: each pane wears the OTHER's id.
	panes := []tmux.LivePane{
		{Session: "grove-chat-unbrewed-1", Pane: "%713", PID: 100, Dir: orch, Command: "claude", ChatSession: "2222-idle"},
		{Session: "grove-chat-unbrewed-2", Pane: "%714", PID: 200, Dir: orch, Command: "claude", ChatSession: "1111-active"},
	}
	procs := []chat.Proc{
		{PID: 100, PPID: 1, Args: "-bash"},
		{PID: 101, PPID: 100, Args: "claude --session-id 1111-active"},
		{PID: 200, PPID: 1, Args: "-bash"},
		{PID: 201, PPID: 200, Args: "claude --session-id 2222-idle"},
	}
	stamps, stamp := recordStamps()
	rows := chatRows([]workspace.Workspace{ws}, lookProcs(panes, neverCockpit, stamp, procs))
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(rows), rows)
	}
	if *rows[0].SessionID != "1111-active" || *rows[1].SessionID != "2222-idle" {
		t.Fatalf("an inverted pair must be corrected, got %q / %q", *rows[0].SessionID, *rows[1].SessionID)
	}
	if stamps["%713"] != "1111-active" || stamps["%714"] != "2222-idle" {
		t.Errorf("the correction must be WRITTEN BACK to the panes: %v", stamps)
	}
	for _, r := range rows {
		if r.Kind == chat.KindArchived {
			t.Errorf("both transcripts are live-claimed; none may be archived: %+v", r)
		}
	}
}

// The id a correction FREES goes back on the shelf: a transcript nothing is
// running any more must reappear as archived, not vanish from the report
// because a pane was still wearing its id when the panes were listed.
func TestChatRowsCorrectionFreesTheStaleID(t *testing.T) {
	ws, orch := chatFixture(t)
	writeTranscript(t, orch, "1111-active", "what is really running", time.Now())
	writeTranscript(t, orch, "9999-stale", "a conversation that ended", time.Now().Add(-time.Hour))
	panes := []tmux.LivePane{
		{Session: "grove-chat-unbrewed-1", Pane: "%7", PID: 100, Dir: orch, Command: "claude", ChatSession: "9999-stale"},
	}
	procs := []chat.Proc{
		{PID: 100, PPID: 1, Args: "-bash"},
		{PID: 101, PPID: 100, Args: "claude --session-id 1111-active"},
	}
	_, stamp := recordStamps()
	rows := chatRows([]workspace.Workspace{ws}, lookProcs(panes, neverCockpit, stamp, procs))
	if len(rows) != 2 {
		t.Fatalf("want the live chat + the freed transcript, got %+v", rows)
	}
	if *rows[0].SessionID != "1111-active" || rows[0].Kind != chat.KindChat {
		t.Errorf("the live row must carry the running id: %+v", rows[0])
	}
	if rows[1].Kind != chat.KindArchived || *rows[1].SessionID != "9999-stale" {
		t.Errorf("the freed transcript must be listed as archived: %+v", rows[1])
	}
}

// No ground truth (a pane grove did not spawn, on a machine whose process
// table says nothing) and TWO rivals in one project dir: both report null.
// A missing id costs a client a button; a wrong one pastes into the wrong
// agent — so refusing is the answer, and mtime order is never consulted.
func TestChatRowsRefusesToGuessBetweenRivals(t *testing.T) {
	ws, orch := chatFixture(t)
	now := time.Now()
	writeTranscript(t, orch, "1111-older", "older", now.Add(-2*time.Hour))
	writeTranscript(t, orch, "2222-newer", "newer", now.Add(-1*time.Hour))
	panes := []tmux.LivePane{
		{Session: "grove-chat-unbrewed-1", Pane: "%7", PID: 100, Dir: orch, Command: "claude", Created: time.Unix(1700000100, 0)},
		{Session: "grove-chat-unbrewed-2", Pane: "%8", PID: 200, Dir: orch, Command: "claude", Created: time.Unix(1700000200, 0)},
	}
	stamps, stamp := recordStamps()
	rows := chatRows([]workspace.Workspace{ws}, look(panes, neverCockpit, stamp))

	var chats, archived []chat.Row
	for _, r := range rows {
		switch r.Kind {
		case chat.KindChat:
			chats = append(chats, r)
		case chat.KindArchived:
			archived = append(archived, r)
		}
	}
	if len(chats) != 2 {
		t.Fatalf("got %d chat rows, want 2: %+v", len(rows), rows)
	}
	for i, r := range chats {
		if r.SessionID != nil {
			t.Errorf("chat %d guessed an id (%q) between two rivals in one project dir", i+1, *r.SessionID)
		}
	}
	if len(stamps) != 0 {
		t.Errorf("nothing may be stamped from a guess: %v", stamps)
	}
	// Unclaimed transcripts are still visible — as archived, which is honest.
	if len(archived) != 2 {
		t.Errorf("both unclaimed transcripts must still be listed as archived, got %d", len(archived))
	}
}

// One unstamped pane and no ground truth IS answerable: it is the only live
// candidate in its project dir, so the newest unclaimed transcript is its
// conversation. This is what keeps the cockpit's own `--continue` pane — the
// one chat whose id grove cannot mint — identified.
func TestChatRowsResolvesTheSoleCandidate(t *testing.T) {
	ws, orch := chatFixture(t)
	writeTranscript(t, orch, "1111-mine", "the only conversation here", time.Now())
	writeTranscript(t, orch, "0000-old", "last tuesday", time.Now().Add(-72*time.Hour))
	panes := []tmux.LivePane{{Session: "grove-chat-unbrewed-1", Pane: "%7", PID: 100, Dir: orch, Command: "claude"}}
	stamps, stamp := recordStamps()
	rows := chatRows([]workspace.Workspace{ws}, look(panes, neverCockpit, stamp))
	if rows[0].SessionID == nil || *rows[0].SessionID != "1111-mine" {
		t.Fatalf("the sole candidate must resolve: %+v", rows[0])
	}
	if stamps["%7"] != "1111-mine" {
		t.Errorf("and be stamped: %v", stamps)
	}
}

// A pane in a project dir of its own has no rivals even when the workspace is
// busy: the profiled chat (grove-36 T4) resolves against ITS dir alone.
func TestChatRowsProfileDirIsItsOwnCompetition(t *testing.T) {
	ws, orch := chatFixture(t)
	profileDir := filepath.Join(orch, "openrouter-glm")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTranscript(t, orch, "1111-a", "brain dir chat a", time.Now())
	writeTranscript(t, orch, "2222-b", "brain dir chat b", time.Now().Add(-time.Hour))
	writeTranscript(t, profileDir, "3333-glm", "the glm experiment", time.Now().Add(-2*time.Hour))
	panes := []tmux.LivePane{
		{Session: "grove-chat-unbrewed-1", Pane: "%7", PID: 100, Dir: orch, Command: "claude"},
		{Session: "grove-chat-unbrewed-2", Pane: "%8", PID: 200, Dir: orch, Command: "claude"},
		{Session: "grove-chat-unbrewed-3", Pane: "%9", PID: 300, Dir: profileDir, Command: "claude"},
	}
	_, stamp := recordStamps()
	rows := chatRows([]workspace.Workspace{ws}, look(panes, neverCockpit, stamp))
	byN := map[int]chat.Row{}
	for _, r := range rows {
		if r.Kind == chat.KindChat {
			byN[r.N] = r
		}
	}
	if byN[1].SessionID != nil || byN[2].SessionID != nil {
		t.Errorf("the two rivals in the brain dir must both stay null: %+v", rows)
	}
	if byN[3].SessionID == nil || *byN[3].SessionID != "3333-glm" {
		t.Errorf("the profiled chat is alone in its own dir and must resolve: %+v", byN[3])
	}
}

// The cockpit's own orchestrator pane is reported as kind cockpit and is
// never writable — and the dashboard pane beside it is not a chat at all.
func TestChatRowsCockpitPane(t *testing.T) {
	ws, orch := chatFixture(t)
	writeTranscript(t, orch, "3333-cockpit", "the cockpit conversation", time.Now())

	panes := []tmux.LivePane{
		{Session: "grove-unbrewed", Pane: "%1", Index: 0, Dir: ws.Root, Command: "gv"},            // the dashboard
		{Session: "grove-unbrewed", Pane: "%2", Index: 1, Dir: orch, Command: "claude"},           // the orchestrator
		{Session: "grove-unbrewed", Pane: "%3", Index: 2, Dir: ws.Root, Command: "ssh"},           // a grove-199 remote chat
		{Session: "grove-unbrewed:worker", Pane: "%4", Index: 1, Dir: ws.Root, Command: "claude"}, // not this session
	}
	_, stamp := recordStamps()
	rows := chatRows([]workspace.Workspace{ws}, look(panes, neverCockpit, stamp))
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want exactly the orchestrator pane: %+v", len(rows), rows)
	}
	r := rows[0]
	if r.Kind != chat.KindCockpit || r.Writable {
		t.Errorf("the cockpit's own pane must be kind cockpit and read-only: %+v", r)
	}
	if r.Session != "grove-unbrewed" || r.N != 1 || !r.Busy {
		t.Errorf("cockpit row = %+v, want the cockpit session / pane index / busy", r)
	}
	if r.SessionID == nil || *r.SessionID != "3333-cockpit" {
		t.Errorf("the cockpit pane resolves its transcript too: %+v", r.SessionID)
	}
}

// A nil CockpitCheck must keep UNDER-reporting (ParseChatSessions' rule):
// `gv park --chats` kills what chat rows describe, so an uninjected guard
// yields no chat rows rather than every session.
func TestChatRowsNilCockpitCheckUnderReports(t *testing.T) {
	ws, orch := chatFixture(t)
	writeTranscript(t, orch, "4444", "a chat", time.Now())
	panes := []tmux.LivePane{{Session: "grove-chat-unbrewed-1", Pane: "%7", Dir: orch, Command: "claude"}}
	_, stamp := recordStamps()
	for _, r := range chatRows([]workspace.Workspace{ws}, look(panes, nil, stamp)) {
		if r.Kind == chat.KindChat {
			t.Fatalf("a nil CockpitCheck must never produce a chat row: %+v", r)
		}
	}
}

// Transcripts no live pane claims are archived, read-only, and include the
// per-profile project dirs a profiled chat writes to (grove-36 T4).
func TestChatRowsArchived(t *testing.T) {
	ws, orch := chatFixture(t)
	profileDir := filepath.Join(orch, "openrouter-glm")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTranscript(t, orch, "5555-live", "today", time.Now())
	writeTranscript(t, orch, "6666-gone", "last tuesday", time.Now().Add(-72*time.Hour))
	writeTranscript(t, profileDir, "7777-glm", "the glm experiment", time.Now().Add(-48*time.Hour))

	panes := []tmux.LivePane{{Session: "grove-chat-unbrewed-1", Pane: "%7", Dir: orch, Command: "claude"}}
	_, stamp := recordStamps()
	rows := chatRows([]workspace.Workspace{ws}, look(panes, neverCockpit, stamp))

	kinds := map[string]int{}
	for _, r := range rows {
		kinds[r.Kind]++
		if r.Kind == chat.KindArchived {
			if r.Writable || r.Busy || r.Session != "" {
				t.Errorf("an archived row must be read-only and paneless: %+v", r)
			}
			if r.Workspace != "unbrewed" {
				t.Errorf("every row carries its workspace: %+v", r)
			}
		}
	}
	if kinds[chat.KindChat] != 1 || kinds[chat.KindArchived] != 2 {
		t.Fatalf("kinds = %v, want 1 live chat + 2 archived (one of them the profile dir's)", kinds)
	}
	// The live chat claimed the newest transcript, so it is NOT archived too.
	for _, r := range rows {
		if r.Kind == chat.KindArchived && r.SessionID != nil && *r.SessionID == "5555-live" {
			t.Error("a transcript a live pane owns must not also be listed as archived")
		}
	}
}

// No --workspace: rows from EVERY registered workspace, each tagged.
func TestChatRowsEveryWorkspace(t *testing.T) {
	t.Setenv("GV_CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "claude"))
	var wss []workspace.Workspace
	var panes []tmux.LivePane
	for i, label := range []string{"unbrewed", "grove-repo"} {
		root := t.TempDir()
		orch := filepath.Join(root, ".grove", "orchestrator")
		if err := os.MkdirAll(orch, 0o755); err != nil {
			t.Fatal(err)
		}
		writeTranscript(t, orch, label+"-id", "chatting about "+label, time.Now())
		wss = append(wss, workspace.Workspace{Root: root, Label: label, Scope: "repo"})
		panes = append(panes, tmux.LivePane{
			Session: "grove-chat-" + label + "-1", Pane: "%" + string(rune('1'+i)),
			Dir: orch, Command: "claude",
		})
	}
	_, stamp := recordStamps()
	rows := chatRows(wss, look(panes, neverCockpit, stamp))
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.Workspace] = true
	}
	if !seen["unbrewed"] || !seen["grove-repo"] || len(rows) != 2 {
		t.Fatalf("want one row per registered workspace, got %+v", rows)
	}
}

// chatWorkspaces: an explicit label that is not registered is an error, not
// an empty report — the caller named something that does not exist.
func TestChatWorkspacesSelection(t *testing.T) {
	list := []workspace.Workspace{{Label: "zed", Root: "/z"}, {Label: "abc", Root: "/a"}}
	got, err := chatWorkspaces(list, "zed")
	if err != nil || len(got) != 1 || got[0].Label != "zed" {
		t.Fatalf("chatWorkspaces(zed) = %+v, %v", got, err)
	}
	if _, err := chatWorkspaces(list, "nope"); err == nil {
		t.Error("an unregistered label must be an error")
	}
}
