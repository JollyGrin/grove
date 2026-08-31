package chat

import (
	"strings"
	"testing"
)

func row(session, kind, id string) Row {
	r := Row{Session: session, Kind: kind, Writable: Writable(kind)}
	if id != "" {
		r.SessionID = &id
	}
	return r
}

func TestMatch(t *testing.T) {
	rows := []Row{
		row("grove-chat-unbrewed-1", KindChat, "eeeb1111-aaaa"),
		row("grove-chat-unbrewed-2", KindChat, "eeeb2222-bbbb"),
		row("grove-unbrewed", KindCockpit, "ffff3333-cccc"),
		row("", KindArchived, "12345678-dddd"),
	}
	cases := []struct {
		name    string
		target  string
		want    int
		wantErr string
	}{
		{"tmux session name", "grove-chat-unbrewed-2", 1, ""},
		{"cockpit session name", "grove-unbrewed", 2, ""},
		{"full session id", "12345678-dddd", 3, ""},
		{"unique id prefix", "ffff", 2, ""},
		{"the 8 characters ls prints", "12345678", 3, ""},
		{"ambiguous prefix is refused, never picked", "eeeb", -1, "matches 2 chats"},
		{"a prefix too short to trust", "ee", -1, "no chat matching"},
		{"unknown target", "grove-chat-nope-9", -1, "no chat matching"},
		{"empty target", "  ", -1, "name a chat"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Match(rows, tc.target)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("Match(%q) = %d, want an error containing %q", tc.target, got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Match(%q) error = %v, want it to contain %q", tc.target, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Match(%q): %v", tc.target, err)
			}
			if got != tc.want {
				t.Errorf("Match(%q) = %d (%s), want %d", tc.target, got, chatNameOf(rows[got]), tc.want)
			}
		})
	}
}

// An exact tmux session name wins over an id that merely starts with it —
// the operator typed a whole name, and guessing past it is how a relay
// lands in a sibling's pane (grove-116/78).
func TestMatchPrefersAnExactSessionName(t *testing.T) {
	rows := []Row{
		row("", KindArchived, "grove-chat-x-1-lookalike"),
		row("grove-chat-x-1", KindChat, "aaaa"),
	}
	got, err := Match(rows, "grove-chat-x-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Errorf("matched row %d, want the live session (1)", got)
	}
}

func TestWriteRefusal(t *testing.T) {
	cases := []struct {
		name  string
		row   Row
		want  bool // refused?
		hints []string
	}{
		{"a live chat takes input", row("grove-chat-x-1", KindChat, "aaaa"), false, nil},
		{
			"the cockpit's own pane is refused with somewhere to go",
			row("grove-unbrewed", KindCockpit, "bbbb"),
			true,
			[]string{"writable: false", "tmux attach", "gv orchestrator new"},
		},
		{
			"an archived transcript points at the revive verb",
			row("", KindArchived, "cccc"),
			true,
			[]string{"writable: false", "gv orchestrator new --resume cccc"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := WriteRefusal(tc.row)
			if (got != "") != tc.want {
				t.Fatalf("WriteRefusal = %q, refused=%v want %v", got, got != "", tc.want)
			}
			for _, hint := range tc.hints {
				if !strings.Contains(got, hint) {
					t.Errorf("refusal %q is missing %q", got, hint)
				}
			}
		})
	}
}

// The gate is the `writable` FIELD, not a second reading of `kind` — a
// client is told to disable its input box off exactly this, so a row whose
// two disagree must not be writable by accident.
func TestWriteRefusalTracksWritable(t *testing.T) {
	for _, kind := range []string{KindChat, KindCockpit, KindArchived, "something-new"} {
		refused := WriteRefusal(Row{Kind: kind, Session: "s"}) != ""
		if refused == Writable(kind) {
			t.Errorf("kind %q: refused=%v but Writable=%v", kind, refused, Writable(kind))
		}
	}
}

func chatNameOf(r Row) string {
	if r.Session != "" {
		return r.Session
	}
	if r.SessionID != nil {
		return *r.SessionID
	}
	return "?"
}
