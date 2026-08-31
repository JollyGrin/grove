package main

// grove-215: the join, end to end without a tmux server — panes in, rows
// out, stamps recorded. The acceptance case is the first test: two chats in
// ONE workspace share one project dir, and must come back with DISTINCT,
// STABLE ids.

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

func TestChatRowsResolvesTwoChatsInOneWorkspace(t *testing.T) {
	ws, orch := chatFixture(t)
	older := time.Now().Add(-2 * time.Hour)
	newer := time.Now().Add(-1 * time.Hour)
	writeTranscript(t, orch, "1111-older", "triage the artgen backlog", older)
	writeTranscript(t, orch, "2222-newer", "write the release notes", newer)

	panes := []tmux.LivePane{
		{Session: "grove-chat-unbrewed-1", Pane: "%7", Dir: orch, Command: "claude", Created: time.Unix(1700000100, 0)},
		{Session: "grove-chat-unbrewed-2", Pane: "%8", Dir: orch, Command: "claude", Created: time.Unix(1700000200, 0)},
	}
	stamps, stamp := recordStamps()
	rows := chatRows([]workspace.Workspace{ws}, panes, neverCockpit, stamp)

	var chats []chat.Row
	for _, r := range rows {
		if r.Kind == chat.KindChat {
			chats = append(chats, r)
		}
	}
	if len(chats) != 2 {
		t.Fatalf("got %d chat rows, want 2: %+v", len(chats), rows)
	}
	if chats[0].SessionID == nil || chats[1].SessionID == nil {
		t.Fatalf("both chats must resolve an id: %+v", chats)
	}
	if *chats[0].SessionID == *chats[1].SessionID {
		t.Fatalf("two chats in one project dir took the SAME transcript (%q) — the whole point of the join", *chats[0].SessionID)
	}
	// Newest pane takes the newest transcript.
	if *chats[1].SessionID != "2222-newer" || chats[1].Label != "write the release notes" {
		t.Errorf("chat 2 (newer pane) = %q/%q, want 2222-newer", *chats[1].SessionID, chats[1].Label)
	}
	if *chats[0].SessionID != "1111-older" || chats[0].Label != "triage the artgen backlog" {
		t.Errorf("chat 1 = %q/%q, want 1111-older", *chats[0].SessionID, chats[0].Label)
	}
	if !chats[0].Writable || !chats[1].Writable {
		t.Error("a live chat is the one writable kind")
	}
	// The join is decided ONCE: both panes were stamped.
	if stamps["%7"] != "1111-older" || stamps["%8"] != "2222-newer" {
		t.Errorf("panes not stamped with their resolved ids: %v", stamps)
	}

	// STABLE across calls: the second run reads the stamp back off the pane
	// (as tmux would report it) and re-derives nothing.
	panes[0].ChatSession, panes[1].ChatSession = stamps["%7"], stamps["%8"]
	again, reStamp := recordStamps()
	rows2 := chatRows([]workspace.Workspace{ws}, panes, neverCockpit, reStamp)
	for i, r := range rows2 {
		if r.Kind != chat.KindChat {
			continue
		}
		if *r.SessionID != *chats[i].SessionID {
			t.Errorf("row %d id moved between calls: %q then %q", i, *chats[i].SessionID, *r.SessionID)
		}
		if r.Label == "" {
			t.Errorf("row %d lost its label on the stamped path", i)
		}
	}
	if len(again) != 0 {
		t.Errorf("an already-stamped pane must never be re-stamped: %v", again)
	}
}

// session_created has one-second resolution, so two chats spawned
// back-to-back tie: the higher <n> is the younger and takes the newer
// transcript. Without this tie-break the pairing flips with input order.
func TestChatRowsTiedCreationTimes(t *testing.T) {
	ws, orch := chatFixture(t)
	now := time.Now()
	writeTranscript(t, orch, "1111-older", "older", now.Add(-2*time.Hour))
	writeTranscript(t, orch, "2222-newer", "newer", now.Add(-1*time.Hour))
	tie := time.Unix(1700000100, 0)
	panes := []tmux.LivePane{
		{Session: "grove-chat-unbrewed-1", Pane: "%7", Dir: orch, Command: "claude", Created: tie},
		{Session: "grove-chat-unbrewed-2", Pane: "%8", Dir: orch, Command: "claude", Created: tie},
	}
	_, stamp := recordStamps()
	rows := chatRows([]workspace.Workspace{ws}, panes, neverCockpit, stamp)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(rows), rows)
	}
	if *rows[1].SessionID != "2222-newer" || *rows[0].SessionID != "1111-older" {
		t.Errorf("tied panes paired wrong: chat 1 = %q, chat 2 = %q", *rows[0].SessionID, *rows[1].SessionID)
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
	rows := chatRows([]workspace.Workspace{ws}, panes, neverCockpit, stamp)
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
	for _, r := range chatRows([]workspace.Workspace{ws}, panes, nil, stamp) {
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
	rows := chatRows([]workspace.Workspace{ws}, panes, neverCockpit, stamp)

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
	rows := chatRows(wss, panes, neverCockpit, stamp)
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
