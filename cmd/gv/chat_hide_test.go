package main

import (
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/chat"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/tmux"
	"github.com/JollyGrin/grove/internal/workspace"
)

func TestHideArg(t *testing.T) {
	cases := []struct {
		name         string
		args         []string
		env          string
		pane, target string
		errHas       string
	}{
		{"no argument is this pane", nil, "%12", "%12", "", ""},
		{"no argument outside tmux is a usage error", nil, "", "", "", "usage: gv chat hide"},
		{"…that names the missing variable", nil, "  ", "", "", "$TMUX_PANE is not set"},
		{"a pane id", []string{"%7"}, "%12", "%7", "", ""},
		{"a session id beats the ambient pane", []string{"bbbb2222"}, "%12", "", "bbbb2222", ""},
		{"empty argument", []string{" "}, "%12", "", "", "usage: gv chat hide"},
		{"too many", []string{"a", "b"}, "%12", "", "", "usage: gv chat hide"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pane, target, err := hideArg(c.args, c.env)
			if pane != c.pane || target != c.target {
				t.Fatalf("hideArg = %q, %q; want %q, %q", pane, target, c.pane, c.target)
			}
			if c.errHas == "" && err != nil {
				t.Fatalf("hideArg: %v", err)
			}
			if c.errHas != "" && (err == nil || !strings.Contains(err.Error(), c.errHas)) {
				t.Fatalf("hideArg err = %v, want %q", err, c.errHas)
			}
		})
	}
}

// hideFixture is a workspace whose cockpit holds the dashboard, two
// orchestrator panes and a remote attachment (cwd = the workspace root),
// plus one chat that is already hidden.
func hideFixture(t *testing.T) ([]chatRecord, workspace.Workspace) {
	t.Helper()
	ws, orch := chatFixture(t)
	cockpit := cockpitSessionForLabel(ws.Label)
	panes := []tmux.LivePane{
		{Session: cockpit, Pane: "%0", Index: 1, Dir: ws.Root, Command: "gv"},
		{Session: cockpit, Pane: "%3", Index: 2, Dir: orch, Command: "claude", ChatSession: "bbbb2222-0000"},
		{Session: cockpit, Pane: "%4", Index: 3, Dir: orch, Command: "claude", ChatSession: "bbbb3333-0000"},
		{Session: cockpit, Pane: "%5", Index: 4, Dir: ws.Root, Command: "ssh"},
		{Session: "grove-chat-" + ws.Label + "-1", Pane: "%6", Index: 1, Dir: orch, Command: "claude", ChatSession: "aaaa1111-0000"},
	}
	isCockpit := func(s string) bool { return s == cockpit }
	_, stamp := recordStamps()
	return chatRecords([]workspace.Workspace{ws}, look(panes, isCockpit, stamp)), ws
}

func TestHideRecord(t *testing.T) {
	recs, ws := hideFixture(t)
	cockpit := cockpitSessionForLabel(ws.Label)
	cases := []struct {
		name, pane, target string
		wantPane           string
		errHas             string
	}{
		{"by pane id", "%4", "", "%4", ""},
		{"by session id", "", "bbbb2222-0000", "%3", ""},
		{"by prefix", "", "bbbb3", "%4", ""},
		{"the dashboard is not a chat", "%0", "", "", "not an orchestrator chat pane"},
		{"a remote attachment is not an orchestrator pane", "%5", "", "", "not an orchestrator chat pane"},
		{"a cockpit name with two chats is an error, never a pick", "", cockpit, "", "holds 2 chat panes"},
		{"a hidden chat resolves (its refusal is HideRefusal's)", "", "grove-chat-" + ws.Label + "-1", "%6", ""},
		{"unknown", "", "zzzz9999", "", "no chat matching"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec, err := hideRecord(recs, c.pane, c.target)
			if c.errHas != "" {
				if err == nil || !strings.Contains(err.Error(), c.errHas) {
					t.Fatalf("hideRecord err = %v, want %q", err, c.errHas)
				}
				return
			}
			if err != nil || rec.Pane != c.wantPane {
				t.Fatalf("hideRecord = %q, %v; want pane %s", rec.Pane, err, c.wantPane)
			}
		})
	}
}

// The invariant of the whole chat-hide train: until the operator hides a
// chat, a cockpit's chats are cockpit panes — and the report's contract is
// unchanged by a hide, which only moves a pane between two kinds it already
// had.
func TestHideChangesKindNotContract(t *testing.T) {
	recs, ws := hideFixture(t)
	kinds := map[string]chat.Row{}
	for _, r := range recs {
		kinds[r.Pane] = r.Row
	}
	for _, pane := range []string{"%3", "%4"} {
		if r := kinds[pane]; r.Kind != chat.KindCockpit || r.Writable {
			t.Errorf("%s = kind %s writable %v; a chat nobody hid is a read-only cockpit pane", pane, r.Kind, r.Writable)
		}
	}
	if r := kinds["%6"]; r.Kind != chat.KindChat || !r.Writable || r.Session != "grove-chat-"+ws.Label+"-1" {
		t.Errorf("hidden chat = %+v; want kind chat, writable, on its own session", r)
	}
}

func TestChatMoveEvent(t *testing.T) {
	id := "bbbb2222-0000"
	ev := chatMoveEvent(state.EvChatHidden, "grove-chat-x-1", "%3", chat.Row{Workspace: "x", SessionID: &id})
	if ev.Type != "chat_hidden" || ev.Ticket != "" {
		t.Fatalf("event = %+v", ev)
	}
	want := map[string]string{"session": "grove-chat-x-1", "pane": "%3", "workspace": "x", "session_id": id}
	if len(ev.Data) != len(want) {
		t.Fatalf("data = %v, want %v", ev.Data, want)
	}
	for k, v := range want {
		if ev.Data[k] != v {
			t.Errorf("data[%s] = %q, want %q", k, ev.Data[k], v)
		}
	}
	ev = chatMoveEvent(state.EvChatShown, "grove-chat-x-1", "%3", chat.Row{Workspace: "x"})
	if _, ok := ev.Data["session_id"]; ok || ev.Type != "chat_shown" {
		t.Fatalf("an unidentified pane's event = %+v; session_id must be absent, not empty", ev)
	}
}
