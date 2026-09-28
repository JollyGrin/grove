package main

// grove-404: hidden REMOTE chats in the cockpit's read, and the verbs'
// naming. The tmux half — hide, show, the record itself — is tested on an
// isolated socket in internal/tmux.

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/JollyGrin/grove/internal/chat"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/tmux"
	"github.com/JollyGrin/grove/internal/tui"
)

// sshTripwire puts a fake ssh first on PATH that records every run.
func sshTripwire(t *testing.T) (ran func() bool) {
	t.Helper()
	bin := t.TempDir()
	marker := filepath.Join(bin, "ssh-ran")
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte("#!/bin/sh\necho ran >> '"+marker+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() bool { _, err := os.Stat(marker); return err == nil }
}

// withHiddenRemotes stamps the record on every pane of the cockpit session,
// which is how the one listing reports a session option.
func withHiddenRemotes(panes []tmux.LivePane, cockpit string, recs ...tmux.HiddenRemote) []tmux.LivePane {
	out := append([]tmux.LivePane(nil), panes...)
	for i := range out {
		if out[i].Session == cockpit {
			out[i].HiddenRemote = tmux.EncodeHiddenRemotes(recs)
		}
	}
	return out
}

// AC: with remote chats hidden the 1s path is still ONE pane listing, and
// neither pass dials the host — not the cheap one, not the costly one.
func TestCockpitChatsHiddenRemoteCostsNothingMore(t *testing.T) {
	ws, orch := chatFixture(t)
	ran := sshTripwire(t)
	base := cockpitChatPanesFixture(ws, orch)
	panes := withHiddenRemotes(base, "grove-unbrewed",
		tmux.HiddenRemote{Host: "groveremote", Session: "grove-chat-unbrewed-4", Profile: "glm-4.6"},
		tmux.HiddenRemote{Host: "pc", Session: "grove-chat-unbrewed-1"})

	var cheap, plain chatCosts
	rows := cockpitChats(&ws, false, countingLookup(&cheap, panes, nil, ""))
	cockpitChats(&ws, false, countingLookup(&plain, base, nil, ""))
	if len(rows) != 6 {
		t.Fatalf("got %d rows, want the 4 panes + 2 hidden remote chats: %+v", len(rows), rows)
	}
	if cheap.panes != 1 || cheap != plain {
		t.Errorf("cheap pass with hidden remote chats cost %+v, without %+v — want one listing and no difference", cheap, plain)
	}
	want := []tui.ChatRow{
		{Session: "grove-chat-unbrewed-4", Hidden: true, N: 4, Host: "groveremote", Model: "glm-4.6"},
		{Session: "grove-chat-unbrewed-1", Hidden: true, N: 1, Host: "pc"},
	}
	if got := rows[4:]; !reflect.DeepEqual(got, want) {
		t.Errorf("hidden remote rows = %+v, want %+v", got, want)
	}

	var deep, deepPlain chatCosts
	deepRows := cockpitChats(&ws, true, countingLookup(&deep, panes, nil, ""))
	cockpitChats(&ws, true, countingLookup(&deepPlain, base, nil, ""))
	if deep != deepPlain {
		t.Errorf("costly pass with hidden remote chats cost %+v, without %+v — a hidden remote row is its record, nothing is read for it", deep, deepPlain)
	}
	if got := deepRows[4:]; !reflect.DeepEqual(got, want) {
		t.Errorf("costly pass changed the hidden remote rows: %+v", got)
	}
	if ran() {
		t.Fatal("the CHATS box ran ssh")
	}
}

// A cockpit whose every chat is a hidden remote one still has a box: the
// dashboard pane carries the record.
func TestCockpitChatsOnlyHiddenRemote(t *testing.T) {
	ws, _ := chatFixture(t)
	panes := withHiddenRemotes([]tmux.LivePane{
		{Session: "grove-unbrewed", PID: 100, Command: "gv", Pane: "%1", Index: 1, Dir: ws.Root},
		{Session: "grove-other", PID: 200, Command: "gv", Pane: "%5", Index: 1, Dir: "/elsewhere"},
	}, "grove-unbrewed", tmux.HiddenRemote{Host: "pc", Session: "grove-chat-unbrewed-2"})
	var c chatCosts
	rows := cockpitChats(&ws, false, countingLookup(&c, panes, nil, ""))
	if len(rows) != 1 || rows[0].Host != "pc" || !rows[0].Hidden || rows[0].Pane != "" || rows[0].N != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	if c.registry != 0 || c.panes != 1 {
		t.Errorf("costs = %+v", c)
	}
	// Another workspace's record is not this one's.
	panes = withHiddenRemotes(panes[:2], "grove-other", tmux.HiddenRemote{Host: "pc", Session: "grove-chat-other-1"})
	panes[0].HiddenRemote = ""
	if rows := cockpitChats(&ws, false, countingLookup(&c, panes, nil, "")); rows != nil {
		t.Errorf("another cockpit's hidden chat was listed: %+v", rows)
	}
	// A malformed record is ignored, not guessed at.
	panes[0].HiddenRemote = "pc|grove-chat-unbrewed-2"
	if rows := cockpitChats(&ws, false, countingLookup(&c, panes, nil, "")); rows != nil {
		t.Errorf("a malformed record made a row: %+v", rows)
	}
}

// Invariant (every car): nothing auto-hides. Panes with no record yield
// exactly the rows they did before this car — the remote one on screen.
func TestCockpitChatsNothingHiddenByDefault(t *testing.T) {
	ws, orch := chatFixture(t)
	var c chatCosts
	for _, r := range cockpitChats(&ws, true, countingLookup(&c, cockpitChatPanesFixture(ws, orch), nil, "")) {
		if r.Host != "" && r.Hidden {
			t.Errorf("a remote chat is hidden with no record of a hide: %+v", r)
		}
		if r.Pane == "" {
			t.Errorf("a row with no pane and no record: %+v", r)
		}
	}
}

func TestChatSessionNumber(t *testing.T) {
	for name, want := range map[string]int{
		"grove-chat-unbrewed-3": 3, "grove-chat-chat-app-12": 12, "grove-chat-x-": 0, "nodash": 0, "grove-chat-x-y": 0, "": 0,
	} {
		if got := chatSessionNumber(name); got != want {
			t.Errorf("chatSessionNumber(%q) = %d, want %d", name, got, want)
		}
	}
}

func TestParseRemoteChatTarget(t *testing.T) {
	cases := []struct {
		target, host, session string
		ok                    bool
	}{
		{"@groveremote/grove-chat-x-1", "groveremote", "grove-chat-x-1", true},
		{remoteChatTarget("pc", "grove-chat-chat-app-2"), "pc", "grove-chat-chat-app-2", true},
		// A local chat is named by its session alone — and stays local.
		{"grove-chat-x-1", "", "", false},
		{"@groveremote", "", "", false},
		{"@/grove-chat-x-1", "", "", false},
		{"@groveremote/", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		host, session, ok := parseRemoteChatTarget(c.target)
		if host != c.host || session != c.session || ok != c.ok {
			t.Errorf("parseRemoteChatTarget(%q) = (%q, %q, %v), want (%q, %q, %v)", c.target, host, session, ok, c.host, c.session, c.ok)
		}
	}
}

// The events are chat_hidden / chat_shown as they were, plus `host`.
func TestRemoteChatMoveEvent(t *testing.T) {
	ev := remoteChatMoveEvent(state.EvChatHidden, "groveremote", "grove-chat-x-1", "%9", "x")
	want := map[string]string{"session": "grove-chat-x-1", "pane": "%9", "workspace": "x", "host": "groveremote"}
	if ev.Type != "chat_hidden" || ev.Ticket != "" || !reflect.DeepEqual(ev.Data, want) {
		t.Errorf("event = %+v", ev)
	}
	if ev := remoteChatMoveEvent(state.EvChatShown, "pc", "s", "%1", "x"); ev.Type != "chat_shown" || ev.Data["host"] != "pc" {
		t.Errorf("event = %+v", ev)
	}
	// A LOCAL move never grows the key.
	if _, ok := chatMoveEvent(state.EvChatHidden, "grove-chat-x-1", "%9", chat.Row{Workspace: "x"}).Data["host"]; ok {
		t.Error("a local chat_hidden carries a host")
	}
}
