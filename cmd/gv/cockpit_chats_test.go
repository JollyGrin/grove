package main

// grove-402: the cockpit CHATS box's read, without a tmux server. The
// cost tests are the point — the cockpit RAM rule is an acceptance
// criterion: the cheap pass is ONE pane listing and nothing else.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/chat"
	"github.com/JollyGrin/grove/internal/tmux"
	"github.com/JollyGrin/grove/internal/workspace"
)

const (
	cockpitChatIDa = "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa"
	cockpitChatIDb = "bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb"
)

// chatCosts counts every impure call a pass makes.
type chatCosts struct {
	panes, registry, procs, captures, transcripts, configs int
}

// countingLookup serves canned panes/procs/captures and counts the calls.
func countingLookup(c *chatCosts, panes []tmux.LivePane, procs []chat.Proc, capture string) cockpitChatLookup {
	return cockpitChatLookup{
		panes: func() []tmux.LivePane { c.panes++; return panes },
		isCockpit: func() (tmux.CockpitCheck, error) {
			c.registry++
			return neverCockpit, nil
		},
		procs:   func() []chat.Proc { c.procs++; return procs },
		capture: func(string) (string, error) { c.captures++; return capture, nil },
		transcript: func(path string) (string, string, time.Time, bool) {
			c.transcripts++
			return readChatTranscript(path)
		},
		configDir: func(workspace.Workspace) string { c.configs++; return "" },
	}
}

// cockpitChatPanesFixture: the dashboard pane, one chat on screen, one
// remote chat on screen, two hidden chats, and another workspace's chat.
func cockpitChatPanesFixture(ws workspace.Workspace, orch string) []tmux.LivePane {
	born := time.Unix(1700000000, 0)
	return []tmux.LivePane{
		{Session: "grove-unbrewed", PID: 100, Command: "gv", Pane: "%1", Index: 1, Dir: ws.Root, Created: born},
		{Session: "grove-unbrewed", PID: 101, Command: "claude", Pane: "%2", Index: 2, Dir: orch, Model: "opus", Created: born},
		{Session: "grove-unbrewed", PID: 102, Command: "ssh", Pane: "%3", Index: 3, Dir: ws.Root, Remote: "groveremote", Created: born},
		{Session: "grove-chat-unbrewed-2", PID: 202, Command: "zsh", Pane: "%8", Index: 1, Dir: orch, ChatSession: cockpitChatIDb, Model: "glm-4.6", Created: born},
		{Session: "grove-chat-unbrewed-1", PID: 201, Command: "claude", Pane: "%7", Index: 1, Dir: orch, Created: born},
		{Session: "grove-chat-other-1", PID: 301, Command: "claude", Pane: "%9", Index: 1, Dir: "/elsewhere", Created: born},
		{Session: "grove-unbrewed", PID: 103, Command: "zsh", Pane: "%4", Index: 1, Dir: filepath.Join(ws.Root, "repo"), Created: born},
	}
}

// AC: the 1s path costs exactly ONE tmux invocation (the pane listing —
// tmux.Panes is pinned to one exec by TestPanesOneExec) and zero
// ps/transcript/capture work.
func TestCockpitChatsCheapPassCostsOnePaneListing(t *testing.T) {
	ws, orch := chatFixture(t)
	var c chatCosts
	rows := cockpitChats(&ws, false, countingLookup(&c, cockpitChatPanesFixture(ws, orch), nil, ""))
	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4: %+v", len(rows), rows)
	}
	if c.panes != 1 {
		t.Errorf("cheap pass listed panes %d times, want exactly 1", c.panes)
	}
	if c.procs != 0 || c.captures != 0 || c.transcripts != 0 || c.configs != 0 {
		t.Errorf("cheap pass did costly work: %+v", c)
	}
	for _, r := range rows {
		if r.Label != "" || r.Last != "" || r.Turn != "" || r.Waiting || !r.LastActive.IsZero() {
			t.Errorf("cheap row carries a costly field: %+v", r)
		}
	}
	// grove-403: every row names the tmux session it lives in — the chat
	// keys need it, and it comes off the same one listing.
	sessions := map[string]string{}
	for _, r := range rows {
		sessions[r.Pane] = r.Session
	}
	want := map[string]string{"%2": "grove-unbrewed", "%3": "grove-unbrewed", "%7": "grove-chat-unbrewed-1", "%8": "grove-chat-unbrewed-2"}
	if !reflect.DeepEqual(sessions, want) {
		t.Errorf("row sessions = %v, want %v", sessions, want)
	}
}

// With no hidden chat the registry is not even read: a cockpit showing
// only its own panes pays for the listing alone.
func TestCockpitChatsShownOnlySkipsRegistry(t *testing.T) {
	ws, orch := chatFixture(t)
	var c chatCosts
	panes := cockpitChatPanesFixture(ws, orch)[:3]
	rows := cockpitChats(&ws, false, countingLookup(&c, panes, nil, ""))
	if len(rows) != 2 || c.registry != 0 || c.panes != 1 {
		t.Errorf("rows=%d costs=%+v, want 2 rows, no registry read, one listing", len(rows), c)
	}
}

// The global cockpit owns no chats and must not even list panes.
func TestCockpitChatsGlobalCockpitCostsNothing(t *testing.T) {
	var c chatCosts
	if rows := cockpitChats(nil, true, countingLookup(&c, []tmux.LivePane{{Session: "grove"}}, nil, "")); rows != nil {
		t.Errorf("rows = %+v, want none", rows)
	}
	if c != (chatCosts{}) {
		t.Errorf("global cockpit paid %+v", c)
	}
}

func TestCockpitChatsClassifiesRows(t *testing.T) {
	ws, orch := chatFixture(t)
	var c chatCosts
	rows := cockpitChats(&ws, false, countingLookup(&c, cockpitChatPanesFixture(ws, orch), nil, ""))
	by := map[string]int{}
	for i, r := range rows {
		by[r.Pane] = i
	}
	for _, pane := range []string{"%1", "%4", "%9"} {
		if _, ok := by[pane]; ok {
			t.Errorf("%s is not a chat of this workspace and must not be listed", pane)
		}
	}
	shown, remote, idle, busy := rows[by["%2"]], rows[by["%3"]], rows[by["%8"]], rows[by["%7"]]
	if shown.Hidden || shown.N != 2 || shown.Model != "opus" || !shown.Busy || shown.Host != "" {
		t.Errorf("shown = %+v", shown)
	}
	if remote.Hidden || remote.Host != "groveremote" || remote.N != 3 {
		t.Errorf("remote = %+v", remote)
	}
	if !idle.Hidden || idle.N != 2 || idle.Busy || idle.Model != "glm-4.6" {
		t.Errorf("hidden idle = %+v", idle)
	}
	if !busy.Hidden || busy.N != 1 || !busy.Busy {
		t.Errorf("hidden busy = %+v", busy)
	}
}

// The costly pass: one ps, one transcript read per identified local chat,
// one capture per pane with an agent on it — and the waiting capture
// overrides on a chat ON SCREEN too.
func TestCockpitChatsDeepPass(t *testing.T) {
	ws, orch := chatFixture(t)
	now := time.Now().Truncate(time.Second)
	writeTranscript(t, orch, cockpitChatIDa, "triage the artgen backlog", now.Add(-3*time.Minute))
	writeTranscript(t, orch, cockpitChatIDb, "overnight mandate", now.Add(-40*time.Minute))
	picker, err := os.ReadFile(filepath.Join("..", "..", "internal", "chatweb", "testdata", "cc2.1.282-single.txt"))
	if err != nil {
		t.Fatal(err)
	}
	procs := []chat.Proc{
		{PID: 101, PPID: 1, Args: "zsh"},
		{PID: 1011, PPID: 101, Args: "claude --session-id " + cockpitChatIDa},
	}
	var c chatCosts
	rows := cockpitChats(&ws, true, countingLookup(&c, cockpitChatPanesFixture(ws, orch), procs, string(picker)))
	if c.panes != 1 || c.procs != 1 {
		t.Errorf("deep pass: %d pane listings, %d ps — want 1 and 1", c.panes, c.procs)
	}
	// %2 by ps, %8 by its stamp; %7 has neither, %3 is remote.
	if c.transcripts != 2 {
		t.Errorf("deep pass read %d transcripts, want 2", c.transcripts)
	}
	// %2 and %7 run claude, %3 is a remote attach; %8 sits at a shell.
	if c.captures != 3 {
		t.Errorf("deep pass captured %d panes, want 3", c.captures)
	}
	by := map[string]int{}
	for i, r := range rows {
		by[r.Pane] = i
	}
	shown := rows[by["%2"]]
	if shown.Label != "triage the artgen backlog" || shown.Last != "triage the artgen backlog" || !shown.LastActive.Equal(now.Add(-3*time.Minute)) {
		t.Errorf("shown transcript fields = %+v", shown)
	}
	if !shown.Waiting {
		t.Errorf("a picker on a chat on screen must read waiting: %+v", shown)
	}
	stopped := rows[by["%8"]]
	if stopped.Turn != "stopped" || stopped.Waiting || stopped.Label != "overnight mandate" {
		t.Errorf("hidden chat at a shell = %+v", stopped)
	}
	if unknown := rows[by["%7"]]; unknown.Label != "" || !unknown.LastActive.IsZero() {
		t.Errorf("an unidentified pane must carry no transcript fields: %+v", unknown)
	}
}

// AC: a pure read — the lookup has no stamp writer to call, and an
// unreadable registry lists no hidden chat rather than guessing.
func TestCockpitChatsUnreadableRegistryListsNoHidden(t *testing.T) {
	ws, orch := chatFixture(t)
	var c chatCosts
	look := countingLookup(&c, cockpitChatPanesFixture(ws, orch), nil, "")
	look.isCockpit = func() (tmux.CockpitCheck, error) { return nil, os.ErrPermission }
	for _, r := range cockpitChats(&ws, false, look) {
		if r.Hidden {
			t.Errorf("hidden row listed off an unreadable registry: %+v", r)
		}
	}
}

func TestLastSaid(t *testing.T) {
	user := `{"type":"user","message":{"role":"user","content":"anything need me?"}}`
	reply := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hm"},{"type":"text","text":"Two workers moving.\n\nFile as one issue or two?\n"}]}}`
	tool := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}`
	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{"empty", nil, ""},
		{"user only", []string{user}, "anything need me?"},
		{"the reply's last line", []string{user, reply}, "File as one issue or two?"},
		{"tool calls and chrome are skipped", []string{user, reply, tool, `{"type":"mode"}`, `{"half a li`}, "File as one issue or two?"},
	}
	for _, tc := range cases {
		if got := lastSaid(tc.lines); got != tc.want {
			t.Errorf("%s: lastSaid = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The tail read is bounded: a transcript longer than the window still
// answers from its end, and the line the seek cut in half is dropped.
func TestLastChatLineReadsOnlyTheTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	filler := `{"type":"user","message":{"role":"user","content":"` + strings.Repeat("x", 1000) + `"}}` + "\n"
	body := strings.Repeat(filler, 100) + `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done — PR opened"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if int64(len(body)) <= chatTailBytes {
		t.Fatal("fixture must exceed the tail window")
	}
	if got := lastChatLine(path, int64(len(body))); got != "done — PR opened" {
		t.Errorf("lastChatLine = %q", got)
	}
}
