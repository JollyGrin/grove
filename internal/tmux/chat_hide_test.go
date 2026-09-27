package tmux

import (
	"fmt"
	"strings"
	"testing"
)

// registry is a CockpitCheck over a fixed set of cockpit session names.
func registry(cockpits ...string) CockpitCheck {
	set := map[string]bool{}
	for _, c := range cockpits {
		set[c] = true
	}
	return func(session string) bool { return set[session] }
}

func TestHidablePane(t *testing.T) {
	reg := registry("grove-x", "grove-chat-app")
	chatPane := PaneFacts{Session: "grove-x", Window: "cockpit", WindowID: "@1", Index: 2, First: 1}
	cases := []struct {
		name      string
		mutate    func(*PaneFacts)
		isCockpit CockpitCheck
		want      string // substring of the refusal, "" = hidable
	}{
		{"chat pane", func(*PaneFacts) {}, reg, ""},
		{"chat pane, nil check", func(*PaneFacts) {}, nil, ""},
		{"dashboard under pane-base-index 1", func(p *PaneFacts) { p.Index = 1 }, reg, "the dashboard"},
		{"dashboard under pane-base-index 0", func(p *PaneFacts) { p.Index, p.First = 0, 0 }, reg, "the dashboard"},
		{"dashboard, nil check", func(p *PaneFacts) { p.Index = 1 }, nil, "the dashboard"},
		{"index 0 is a chat when the dashboard died", func(p *PaneFacts) { p.Index, p.First = 3, 2 }, reg, ""},
		{"worker window", func(p *PaneFacts) { p.Window = "repo · grove-1" }, reg, "worker windows are never hidden"},
		{"worker window, nil check", func(p *PaneFacts) { p.Window = "repo · grove-1" }, nil, "worker windows are never hidden"},
		{"foreign session", func(p *PaneFacts) { p.Session = "work" }, reg, "not a grove cockpit"},
		{"grove-named but unregistered", func(p *PaneFacts) { p.Session = "grove-ghost" }, reg, "not a registered workspace's cockpit"},
		{"already hidden", func(p *PaneFacts) { p.Session, p.Window = "grove-chat-x-1", "chat" }, reg, "already hidden"},
		{"already hidden names the show verb", func(p *PaneFacts) { p.Session, p.Window = "grove-chat-x-1", "chat" }, reg, "gv chat show grove-chat-x-1"},
		{"chat-shaped name, nil check: window rule protects", func(p *PaneFacts) { p.Session, p.Window = "grove-chat-x-1", "chat" }, nil, "not the cockpit window"},
		{"cockpit of a label that looks like a chat", func(p *PaneFacts) { p.Session = "grove-chat-app" }, reg, ""},
		{"remote pane", func(p *PaneFacts) { p.Remote = "groveremote" }, reg, "remote chat panes: not yet (chat-hide car: remote)"},
		{"remote beats every other reading", func(p *PaneFacts) { p.Remote, p.Index = "groveremote", 1 }, reg, "remote chat panes: not yet"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := chatPane
			c.mutate(&p)
			err := hidablePane(p, c.isCockpit)
			if c.want == "" {
				if err != nil {
					t.Fatalf("hidablePane = %v, want hidable", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("hidablePane = %v, want a refusal containing %q", err, c.want)
			}
		})
	}
}

func TestShowableChat(t *testing.T) {
	reg := registry("grove-x", "grove-chat-app")
	hidden := ChatFacts{Session: "grove-chat-x-1", Exists: true, Panes: []string{"%4"}, Cockpit: "grove-x", CockpitWindow: "@1"}
	cases := []struct {
		name      string
		mutate    func(*ChatFacts)
		isCockpit CockpitCheck
		want      string
	}{
		{"hidden chat", func(*ChatFacts) {}, reg, ""},
		{"nil check shows nothing", func(*ChatFacts) {}, nil, "already shown"},
		{"a cockpit session is already shown", func(c *ChatFacts) { c.Session = "grove-x" }, reg, "already shown"},
		{"a cockpit whose label looks like a chat", func(c *ChatFacts) { c.Session = "grove-chat-app" }, reg, "already shown"},
		{"foreign session", func(c *ChatFacts) { c.Session = "work" }, reg, "not a hidden chat"},
		{"session gone", func(c *ChatFacts) { c.Exists, c.Panes = false, nil }, reg, "no live chat session"},
		{"operator split it", func(c *ChatFacts) { c.Panes = []string{"%4", "%9"} }, reg, "holds 2 panes"},
		{"remote attachment", func(c *ChatFacts) { c.Remote = "groveremote" }, reg, "remote chat panes: not yet (chat-hide car: remote)"},
		{"cockpit not running", func(c *ChatFacts) { c.CockpitWindow = "" }, reg, "open the cockpit with `gv`, or attach with tmux attach -t '=grove-chat-x-1'"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := hidden
			c.mutate(&f)
			err := showableChat(f, c.isCockpit)
			if c.want == "" {
				if err != nil {
					t.Fatalf("showableChat = %v, want showable", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("showableChat = %v, want a refusal containing %q", err, c.want)
			}
		})
	}
}

func TestParsePaneFacts(t *testing.T) {
	cases := []struct {
		name, out string
		want      PaneFacts
		bad       bool
	}{
		{"local pane", "grove-x\tcockpit\t\t2\t@1", PaneFacts{Session: "grove-x", Window: "cockpit", Index: 2, WindowID: "@1"}, false},
		{"remote pane", "grove-x\tcockpit\tgroveremote\t3\t@1\n", PaneFacts{Session: "grove-x", Window: "cockpit", Remote: "groveremote", Index: 3, WindowID: "@1"}, false},
		{"short line", "grove-x\tcockpit\t2", PaneFacts{}, true},
		{"index not a number", "grove-x\tcockpit\t\tx\t@1", PaneFacts{}, true},
		{"empty", "", PaneFacts{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parsePaneFacts(c.out)
			if (err != nil) != c.bad {
				t.Fatalf("parsePaneFacts err = %v, want error: %v", err, c.bad)
			}
			if got != c.want {
				t.Fatalf("parsePaneFacts = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestLastPaneID(t *testing.T) {
	cases := []struct {
		name, out, want string
		ok              bool
	}{
		{"pane-base-index 0", "%0 0\n%3 1\n%7 2", "%7", true},
		{"pane-base-index 1", "%0 1\n%3 2", "%3", true},
		{"unordered", "%7 3\n%0 1\n%3 2", "%7", true},
		{"single pane", "%0 1", "%0", true},
		{"garbage", "nope\n", "", false},
		{"empty", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := lastPaneID(c.out)
			if got != c.want || ok != c.ok {
				t.Fatalf("lastPaneID = %q, %v; want %q, %v", got, ok, c.want, c.ok)
			}
		})
	}
}

// --- integration: a fake cockpit on an isolated server ---

// fakeCockpit boots the scratch server under the hostile numbering pair
// (base-index 1 + pane-base-index 1, grove-168) and builds grove-x: a
// dashboard pane plus two chat panes, each running a long-lived process.
// Returns the dashboard and the two chat panes' %ids.
func fakeCockpit(t *testing.T) (dash, chatA, chatB string) {
	t.Helper()
	scratchServer(t)
	dir := t.TempDir()
	mustRun(t, "new-session", "-d", "-s", "boot", "-c", dir)
	mustRun(t, "set-option", "-g", "base-index", "1")
	mustRun(t, "set-option", "-g", "pane-base-index", "1")
	// Window names must hold still: serverState compares them, and a shell
	// settling in renames its window a moment after it is created.
	mustRun(t, "set-option", "-g", "automatic-rename", "off")
	mustRun(t, "new-session", "-d", "-s", "grove-x", "-n", "cockpit", "-x", "200", "-y", "50", "-c", dir, "sleep 600")
	win := Exact("grove-x") + ":cockpit"
	dash, err := FirstPaneID(win)
	if err != nil {
		t.Fatalf("FirstPaneID: %v", err)
	}
	chatA = mustRun(t, "split-window", "-h", "-t", win, "-c", dir, "-P", "-F", "#{pane_id}", "sleep 600")
	chatB = mustRun(t, "split-window", "-h", "-t", win, "-c", dir, "-P", "-F", "#{pane_id}", "sleep 600")
	if err := SetPaneChatSession(chatA, "11111111-aaaa"); err != nil {
		t.Fatal(err)
	}
	if err := SetPaneModel(chatA, "opus"); err != nil {
		t.Fatal(err)
	}
	if err := SetPaneProfile(chatA, "zai-plan"); err != nil {
		t.Fatal(err)
	}
	return dash, chatA, chatB
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	out, err := run(args...)
	if err != nil {
		t.Fatalf("tmux %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(out)
}

// paneFmt reads one format off one pane.
func paneFmt(t *testing.T, pane, format string) string {
	t.Helper()
	return mustRun(t, "display-message", "-p", "-t", pane, "-F", format)
}

// windowPanes lists the pane ids of a window, in index order.
func windowPanes(t *testing.T, target string) []string {
	t.Helper()
	return strings.Fields(mustRun(t, "list-panes", "-t", target, "-F", "#{pane_id}"))
}

// serverState is every session/window/pane on the scratch server — what a
// refusal must leave byte-identical.
func serverState(t *testing.T) string {
	t.Helper()
	return mustRun(t, "list-panes", "-a", "-F", "#{session_name} #{window_name} #{pane_id} #{pane_index} #{pane_pid}")
}

func TestHideShowChatPane(t *testing.T) {
	_, chatA, chatB := fakeCockpit(t)
	reg := registry("grove-x")
	pid := paneFmt(t, chatA, "#{pane_pid}")

	var announced string
	session, err := HideChatPane(chatA, "x", reg, func(s string) error {
		announced = s
		// The event lands BEFORE the move: the pane is still in the cockpit.
		if got := paneFmt(t, chatA, "#{session_name}"); got != "grove-x" {
			t.Errorf("announce ran after the pane moved (pane in %q)", got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("HideChatPane: %v", err)
	}
	if session != "grove-chat-x-1" || announced != session {
		t.Fatalf("session = %q, announced %q; want grove-chat-x-1", session, announced)
	}
	if got := windowPanes(t, Exact("grove-x")+":cockpit"); len(got) != 2 || got[1] != chatB {
		t.Fatalf("cockpit panes after hide = %v, want the dashboard + %s", got, chatB)
	}
	if got := mustRun(t, "list-windows", "-t", Exact(session), "-F", "#{window_name}"); got != "chat" {
		t.Fatalf("chat session windows = %q, want exactly one named chat (no placeholder)", got)
	}
	if got := mustRun(t, "show-options", "-w", "-v", "-t", Exact(session)+":chat", "automatic-rename"); got != "off" {
		t.Fatalf("automatic-rename on the chat window = %q, want off", got)
	}
	hidden, err := ChatSessionPane(session)
	if err != nil || hidden != chatA {
		t.Fatalf("ChatSessionPane = %q, %v; want the same pane id %s", hidden, err, chatA)
	}
	if got := paneFmt(t, chatA, "#{pane_pid}"); got != pid {
		t.Fatalf("pane pid after hide = %s, want %s (the process must keep running)", got, pid)
	}
	for format, want := range map[string]string{
		"#{@grove_chat_session}": "11111111-aaaa",
		"#{@grove_model}":        "opus",
		"#{@grove_profile}":      "zai-plan",
	} {
		if got := paneFmt(t, chatA, format); got != want {
			t.Fatalf("%s after hide = %q, want %q", format, got, want)
		}
	}
	// The hidden pane is an ordinary detached chat to every existing reader.
	chats := ChatSessions("x", reg)
	if len(chats) != 1 || chats[0].Session != session || chats[0].Pane != chatA || chats[0].SessionID != "11111111-aaaa" {
		t.Fatalf("ChatSessions = %+v, want the hidden pane as chat 1", chats)
	}

	// Hiding it again is a refusal, not a second session.
	before := serverState(t)
	if _, err := HideChatPane(chatA, "x", reg, nil); err == nil || !strings.Contains(err.Error(), "already hidden") {
		t.Fatalf("second hide = %v, want already hidden", err)
	}
	if after := serverState(t); after != before {
		t.Fatalf("a refused hide mutated the server:\n%s\n→\n%s", before, after)
	}

	var shownPane string
	pane, err := ShowChatPane(session, "grove-x", reg, func(p string) error { shownPane = p; return nil })
	if err != nil {
		t.Fatalf("ShowChatPane: %v", err)
	}
	if pane != chatA || shownPane != chatA {
		t.Fatalf("shown pane = %q (announced %q), want %s", pane, shownPane, chatA)
	}
	if got := windowPanes(t, Exact("grove-x")+":cockpit"); len(got) != 3 || got[2] != chatA {
		t.Fatalf("cockpit panes after show = %v, want %s joined last", got, chatA)
	}
	if SessionExists(session) {
		t.Fatalf("%s still exists after show — the emptied session must disappear", session)
	}
	if got := paneFmt(t, chatA, "#{pane_pid}"); got != pid {
		t.Fatalf("pane pid after show = %s, want %s", got, pid)
	}
	if got := paneFmt(t, chatA, "#{@grove_chat_session}"); got != "11111111-aaaa" {
		t.Fatalf("stamp after show = %q", got)
	}

	// Showing it again: the session is gone.
	if _, err := ShowChatPane(session, "grove-x", reg, nil); err == nil || !strings.Contains(err.Error(), "no live chat session") {
		t.Fatalf("second show = %v, want a refusal", err)
	}
}

// paneTops lists a window's pane top edges: equal in a row of columns
// (even-horizontal), different where panes are stacked.
func paneTops(t *testing.T, target string) []string {
	t.Helper()
	return strings.Fields(mustRun(t, "list-panes", "-t", target, "-F", "#{pane_top}"))
}

func TestHideShowHonorsLayout(t *testing.T) {
	_, chatA, _ := fakeCockpit(t)
	reg := registry("grove-x")
	win := Exact("grove-x") + ":cockpit"
	// vertical = main-vertical: the dashboard left, chats STACKED right — so
	// chat panes have different tops, which the default (a row) never has.
	if err := SetCockpitLayout("grove-x", "vertical"); err != nil {
		t.Fatal(err)
	}
	// A worker window is active while the hide and show run: the re-tile
	// must land on the cockpit window, never the one being looked at.
	worker := mustRun(t, "new-window", "-P", "-F", "#{window_id}", "-t", Exact("grove-x"), "-n", "repo · grove-1", "sleep 600")
	mustRun(t, "split-window", "-h", "-t", worker, "sleep 600")
	workerLayout := paneFmt(t, worker, "#{window_layout}")

	session, err := HideChatPane(chatA, "x", reg, nil)
	if err != nil {
		t.Fatalf("HideChatPane: %v", err)
	}
	if got := paneFmt(t, win, "#{window_layout}"); !mainVerticalShape(t, win) {
		t.Fatalf("cockpit layout after hide = %s, want main-vertical", got)
	}
	if _, err := ShowChatPane(session, "grove-x", reg, nil); err != nil {
		t.Fatalf("ShowChatPane: %v", err)
	}
	tops := paneTops(t, win)
	if len(tops) != 3 || tops[1] == tops[2] || !mainVerticalShape(t, win) {
		t.Fatalf("pane tops after show = %v, want the two chats stacked (main-vertical)", tops)
	}
	if got := paneFmt(t, worker, "#{window_layout}"); got != workerLayout {
		t.Fatalf("worker window layout changed: %s → %s", workerLayout, got)
	}
	if got := paneFmt(t, win, "#{window_name}"); got != "cockpit" {
		t.Fatalf("cockpit window renamed to %q", got)
	}
}

// mainVerticalShape: the first pane sits at the left edge holding the
// dashboard's 55% share (an even row of two would give it half), and every
// other pane sits to its right.
func mainVerticalShape(t *testing.T, target string) bool {
	t.Helper()
	out := mustRun(t, "list-panes", "-t", target, "-F", "#{pane_left} #{pane_width} #{window_width}")
	lines := strings.Split(out, "\n")
	var left, width, total int
	if _, err := fmt.Sscan(lines[0], &left, &width, &total); err != nil || left != 0 {
		return false
	}
	if share := width * 100 / total; share < 53 || share > 56 {
		return false
	}
	for _, l := range lines[1:] {
		if f := strings.Fields(l); len(f) != 3 || f[0] == "0" {
			return false
		}
	}
	return true
}

func TestHideChatPaneRefusals(t *testing.T) {
	dash, chatA, _ := fakeCockpit(t)
	reg := registry("grove-x")
	worker := mustRun(t, "new-window", "-d", "-P", "-F", "#{pane_id}", "-t", Exact("grove-x"), "-n", "repo · grove-1", "sleep 600")
	workerChat := mustRun(t, "split-window", "-d", "-h", "-t", worker, "-P", "-F", "#{pane_id}", "sleep 600")
	foreign := mustRun(t, "new-session", "-d", "-s", "work", "-P", "-F", "#{pane_id}", "sleep 600")
	remote := mustRun(t, "split-window", "-d", "-h", "-t", Exact("grove-x")+":cockpit", "-P", "-F", "#{pane_id}", "sleep 600")
	if err := SetPaneRemote(remote, "groveremote"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, pane string
		isCockpit  CockpitCheck
		want       string
	}{
		{"dashboard", dash, reg, "the dashboard"},
		{"dashboard, nil check", dash, nil, "the dashboard"},
		{"worker window's first pane", worker, reg, "worker windows are never hidden"},
		{"worker window's second pane", workerChat, reg, "worker windows are never hidden"},
		{"foreign session", foreign, reg, "not a grove cockpit"},
		{"unregistered cockpit", chatA, registry(), "not a registered workspace's cockpit"},
		{"remote pane", remote, reg, "remote chat panes: not yet (chat-hide car: remote)"},
		{"no such pane", "%999", reg, "no such pane"},
		{"no pane id", "", reg, "no pane id"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := serverState(t)
			called := false
			_, err := HideChatPane(c.pane, "x", c.isCockpit, func(string) error { called = true; return nil })
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("HideChatPane = %v, want a refusal containing %q", err, c.want)
			}
			if called {
				t.Fatal("announce ran for a refused hide")
			}
			if after := serverState(t); after != before {
				t.Fatalf("a refused hide mutated the server:\n%s\n→\n%s", before, after)
			}
		})
	}

	t.Run("announce failure moves nothing", func(t *testing.T) {
		before := serverState(t)
		_, err := HideChatPane(chatA, "x", reg, func(string) error { return errString("log is read-only") })
		if err == nil || !strings.Contains(err.Error(), "log is read-only") {
			t.Fatalf("HideChatPane = %v, want the announce error", err)
		}
		if after := serverState(t); after != before {
			t.Fatalf("a failed announce left the server changed:\n%s\n→\n%s", before, after)
		}
	})
}

type errString string

func (e errString) Error() string { return string(e) }

func TestShowChatPaneRefusals(t *testing.T) {
	_, chatA, chatB := fakeCockpit(t)
	reg := registry("grove-x", "grove-y")
	session, err := HideChatPane(chatA, "x", reg, nil)
	if err != nil {
		t.Fatalf("HideChatPane: %v", err)
	}
	split, err := HideChatPane(chatB, "x", reg, nil)
	if err != nil {
		t.Fatalf("HideChatPane: %v", err)
	}
	mustRun(t, "split-window", "-d", "-t", Exact(split)+":chat", "sleep 600")

	cases := []struct {
		name, session, cockpit string
		isCockpit              CockpitCheck
		want                   string
	}{
		{"cockpit session not running", session, "grove-y", reg, "open the cockpit with `gv`, or attach with tmux attach -t '=" + session + "'"},
		{"already shown", "grove-x", "grove-x", reg, "already shown"},
		{"operator split the chat", split, "grove-x", reg, "holds 2 panes"},
		{"no such chat", "grove-chat-x-9", "grove-x", reg, "no live chat session"},
		{"nil check", session, "grove-x", nil, "not a hidden chat"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := serverState(t)
			called := false
			_, err := ShowChatPane(c.session, c.cockpit, c.isCockpit, func(string) error { called = true; return nil })
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("ShowChatPane = %v, want a refusal containing %q", err, c.want)
			}
			if called {
				t.Fatal("announce ran for a refused show")
			}
			if after := serverState(t); after != before {
				t.Fatalf("a refused show mutated the server:\n%s\n→\n%s", before, after)
			}
		})
	}
}

// A chat that was never in the cockpit (spawned detached, the phone's path)
// shows exactly like a hidden one.
func TestShowChatPaneNeverInCockpit(t *testing.T) {
	fakeCockpit(t)
	reg := registry("grove-x")
	if err := CreateChatSession("grove-chat-x-1", t.TempDir(), "sleep 600"); err != nil {
		t.Fatalf("CreateChatSession: %v", err)
	}
	want, err := ChatSessionPane("grove-chat-x-1")
	if err != nil {
		t.Fatal(err)
	}
	pane, err := ShowChatPane("grove-chat-x-1", "grove-x", reg, nil)
	if err != nil {
		t.Fatalf("ShowChatPane: %v", err)
	}
	if pane != want {
		t.Fatalf("shown pane = %s, want %s", pane, want)
	}
	if got := windowPanes(t, Exact("grove-x")+":cockpit"); len(got) != 4 || got[3] != want {
		t.Fatalf("cockpit panes = %v, want %s joined last", got, want)
	}
	if SessionExists("grove-chat-x-1") {
		t.Fatal("the emptied chat session still exists")
	}
}

// activePane is the active pane of a window.
func activePane(t *testing.T, target string) string {
	t.Helper()
	for _, line := range strings.Split(mustRun(t, "list-panes", "-t", target, "-F", "#{pane_active} #{pane_id}"), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[0] == "1" {
			return f[1]
		}
	}
	t.Fatalf("no active pane in %s", target)
	return ""
}

// grove-403: the cockpit's `h` shows a chat without giving it the keyboard;
// `enter` focuses it. ShowChatPane itself still focuses (the CLI verb).
func TestShowChatPaneFocus(t *testing.T) {
	dash, chatA, chatB := fakeCockpit(t)
	reg := registry("grove-x")
	win := Exact("grove-x") + ":cockpit"
	mustRun(t, "select-pane", "-t", dash)

	session, err := HideChatPane(chatA, "x", reg, nil)
	if err != nil {
		t.Fatalf("HideChatPane: %v", err)
	}
	if got := activePane(t, win); got != dash {
		t.Fatalf("active pane after hide = %s, want the dashboard %s", got, dash)
	}
	pane, err := ShowChatPaneUnfocused(session, "grove-x", reg, nil)
	if err != nil {
		t.Fatalf("ShowChatPaneUnfocused: %v", err)
	}
	if pane != chatA {
		t.Fatalf("shown pane = %s, want %s", pane, chatA)
	}
	if got := windowPanes(t, win); len(got) != 3 || got[2] != chatA {
		t.Fatalf("cockpit panes after show = %v, want %s joined last", got, chatA)
	}
	if got := activePane(t, win); got != dash {
		t.Fatalf("active pane after an unfocused show = %s, want the dashboard %s", got, dash)
	}

	if err := FocusChatPane(chatA, reg); err != nil {
		t.Fatalf("FocusChatPane: %v", err)
	}
	if got := activePane(t, win); got != chatA {
		t.Fatalf("active pane after focus = %s, want %s", got, chatA)
	}
	// The dashboard is not a chat: focus refuses it and moves nothing.
	if err := FocusChatPane(dash, reg); err == nil || !strings.Contains(err.Error(), "the dashboard") {
		t.Fatalf("FocusChatPane(dashboard) = %v, want a refusal", err)
	}
	if got := activePane(t, win); got != chatA {
		t.Fatalf("a refused focus moved the active pane to %s", got)
	}

	// The CLI's show keeps taking focus.
	mustRun(t, "select-pane", "-t", dash)
	session, err = HideChatPane(chatB, "x", reg, nil)
	if err != nil {
		t.Fatalf("HideChatPane: %v", err)
	}
	if _, err := ShowChatPane(session, "grove-x", reg, nil); err != nil {
		t.Fatalf("ShowChatPane: %v", err)
	}
	if got := activePane(t, win); got != chatB {
		t.Fatalf("active pane after ShowChatPane = %s, want the shown chat %s", got, chatB)
	}
}
