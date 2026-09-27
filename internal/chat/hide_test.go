package chat

import (
	"strings"
	"testing"
)

func hideRows() []Row {
	id := func(s string) *string { return &s }
	return []Row{
		{Session: "grove-chat-x-1", Workspace: "x", N: 1, Kind: KindChat, SessionID: id("aaaa1111-0000")},
		{Session: "grove-x", Workspace: "x", N: 2, Kind: KindCockpit, SessionID: id("bbbb2222-0000")},
		{Session: "grove-x", Workspace: "x", N: 3, Kind: KindCockpit, SessionID: id("bbbb3333-0000")},
		{Session: "grove-x", Workspace: "x", N: 4, Kind: KindCockpit},
		{Session: "grove-y", Workspace: "y", N: 2, Kind: KindCockpit, SessionID: id("cccc4444-0000")},
		{Workspace: "x", Kind: KindArchived, SessionID: id("dddd5555-0000")},
	}
}

func TestMatchHide(t *testing.T) {
	cases := []struct {
		name, target string
		want         int
		errHas       []string
	}{
		{"full session id", "bbbb3333-0000", 2, nil},
		{"unambiguous prefix", "bbbb2", 1, nil},
		{"a cockpit with one chat pane", "grove-y", 4, nil},
		{"a cockpit with several is an error, never a pick", "grove-x", -1,
			[]string{"grove-x holds 3 chat panes", "bbbb2222-0000 (pane 2)", "bbbb3333-0000 (pane 3)", "pane 4 (no session id yet"}},
		{"ambiguous prefix", "bbbb", -1, []string{"matches 2 chats"}},
		{"a hidden chat still resolves, for its refusal", "grove-chat-x-1", 0, nil},
		{"unknown", "zzzz9999", -1, []string{"no chat matching"}},
		{"empty", "  ", -1, []string{"name a chat"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := MatchHide(hideRows(), c.target)
			if got != c.want {
				t.Fatalf("MatchHide = %d, %v; want %d", got, err, c.want)
			}
			if (err != nil) != (len(c.errHas) > 0) {
				t.Fatalf("MatchHide err = %v", err)
			}
			for _, want := range c.errHas {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q lacks %q", err, want)
				}
			}
		})
	}
}

func TestHideShowRefusal(t *testing.T) {
	rows := hideRows()
	cases := []struct {
		name       string
		row        Row
		hide, show string // substring of the refusal, "" = allowed
	}{
		{"hidden chat", rows[0], "already hidden — bring it back with `gv chat show grove-chat-x-1`", ""},
		{"cockpit pane", rows[1], "", "already shown"},
		{"archived", rows[5], "gv orchestrator new --resume dddd5555-0000", "gv orchestrator new --resume dddd5555-0000"},
		{"kind chat on a session that is not a chat's", Row{Session: "grove-x", Kind: KindChat}, "already hidden", "not a grove-chat-<label>-<n> session"},
		{"unknown kind", Row{Session: "s", Kind: "future"}, "cannot be hidden", "cannot be shown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for verb, pair := range map[string][2]string{"hide": {HideRefusal(c.row), c.hide}, "show": {ShowRefusal(c.row), c.show}} {
				got, want := pair[0], pair[1]
				if want == "" && got != "" {
					t.Errorf("%s refused: %s", verb, got)
				}
				if want != "" && !strings.Contains(got, want) {
					t.Errorf("%s refusal = %q, want it to contain %q", verb, got, want)
				}
			}
		})
	}
}
