package tmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// --- grove-404: the hidden-remote record (pure) ---

func TestHiddenRemotesRoundTrip(t *testing.T) {
	list := []HiddenRemote{
		{Host: "groveremote", Session: "grove-chat-x-1", Profile: "zai-plan"},
		{Host: "pc", Session: "grove-chat-x-2"},
		// Nothing a value holds may forge a boundary — of the record, or of
		// the tab-separated list-panes line it travels in.
		{Host: "odd|host,name", Session: "s\tess\nion", Profile: "a b#{pane_id}%"},
	}
	value := EncodeHiddenRemotes(list)
	if strings.ContainsAny(value, "\t\n #") {
		t.Fatalf("encoded record %q would break the list-panes line that carries it", value)
	}
	if got := ParseHiddenRemotes(value); !reflect.DeepEqual(got, list) {
		t.Fatalf("round trip = %+v, want %+v", got, list)
	}
	if got := EncodeHiddenRemotes(nil); got != "" {
		t.Errorf("no records encode as %q, want the empty value (the option is unset)", got)
	}
	if got := ParseHiddenRemotes(""); got != nil {
		t.Errorf("the empty value parses as %+v", got)
	}
	if got := ParseHiddenRemotes("  \n"); got != nil {
		t.Errorf("a blank value parses as %+v", got)
	}
	if got := EncodeHiddenRemotes([]HiddenRemote{{Host: "pc"}, {Session: "s"}}); got != "" {
		t.Errorf("a record without host or session was encoded: %q", got)
	}
}

func TestParseHiddenRemotesIgnoresMalformed(t *testing.T) {
	good := HiddenRemote{Host: "pc", Session: "grove-chat-x-1", Profile: "glm"}
	cases := []struct{ name, value string }{
		{"too few fields", "pc|grove-chat-x-9"},
		{"too many fields", "pc|grove-chat-x-9|glm|extra"},
		{"no host", "|grove-chat-x-9|glm"},
		{"no session", "pc||glm"},
		{"bad escape", "pc|grove-chat-x-9|%zz"},
		{"empty record", ""},
		{"the ticket's tab form is not this format", "pc\tgrove-chat-x-9\tglm"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, value := range []string{
				c.value + "," + EncodeHiddenRemotes([]HiddenRemote{good}),
				EncodeHiddenRemotes([]HiddenRemote{good}) + "," + c.value,
			} {
				if got := ParseHiddenRemotes(value); !reflect.DeepEqual(got, []HiddenRemote{good}) {
					t.Errorf("ParseHiddenRemotes(%q) = %+v, want only the well-formed record", value, got)
				}
			}
		})
	}
}

func TestAddRemoveHiddenRemote(t *testing.T) {
	a := HiddenRemote{Host: "pc", Session: "grove-chat-x-1"}
	b := HiddenRemote{Host: "pc", Session: "grove-chat-x-2", Profile: "glm"}
	// The same session NAME on another host is another chat.
	c := HiddenRemote{Host: "groveremote", Session: "grove-chat-x-1"}

	list := AddHiddenRemote(nil, a)
	list = AddHiddenRemote(list, b)
	list = AddHiddenRemote(list, c)
	if !reflect.DeepEqual(list, []HiddenRemote{a, b, c}) {
		t.Fatalf("add = %+v", list)
	}
	// A duplicate never makes a second row; it updates the one in place.
	dup := a
	dup.Profile = "zai-plan"
	list = AddHiddenRemote(list, dup)
	if !reflect.DeepEqual(list, []HiddenRemote{dup, b, c}) {
		t.Fatalf("duplicate add = %+v", list)
	}
	if got := AddHiddenRemote(list, HiddenRemote{Host: "pc"}); len(got) != 3 {
		t.Fatalf("a record without a session was added: %+v", got)
	}
	// A duplicate in a stored value keeps its first.
	if got := ParseHiddenRemotes("pc|s|one,pc|s|two"); !reflect.DeepEqual(got, []HiddenRemote{{Host: "pc", Session: "s", Profile: "one"}}) {
		t.Fatalf("stored duplicate = %+v", got)
	}

	before := append([]HiddenRemote(nil), list...)
	got, ok := RemoveHiddenRemote(list, "pc", "grove-chat-x-2")
	if !ok || !reflect.DeepEqual(got, []HiddenRemote{dup, c}) {
		t.Fatalf("remove = %+v ok=%v", got, ok)
	}
	if !reflect.DeepEqual(list, before) {
		t.Fatalf("remove rewrote its input: %+v", list)
	}
	if got, ok := RemoveHiddenRemote(list, "elsewhere", "grove-chat-x-2"); ok || len(got) != 3 {
		t.Fatalf("removing an unknown record = %+v ok=%v", got, ok)
	}
	if got, ok := RemoveHiddenRemote(nil, "pc", "s"); ok || got != nil {
		t.Fatalf("remove from nothing = %+v ok=%v", got, ok)
	}
}

// The record is the LAST list-panes field: every earlier field keeps its
// index, and a session with nothing hidden answers with the line it always
// did.
func TestParsePanesHiddenRemote(t *testing.T) {
	if !strings.HasSuffix(paneListFormat, "\t#{@grove_remote}\t#{"+hiddenRemoteOption+"}") {
		t.Fatalf("paneListFormat must end with the hidden-remote field, appended after @grove_remote: %q", paneListFormat)
	}
	rec := EncodeHiddenRemotes([]HiddenRemote{{Host: "pc", Session: "grove-chat-g-1", Profile: "glm"}})
	out := strings.Join([]string{
		strings.Join([]string{"grove-g", "300", "gv", "1", "1700000100", "%8", "1", "/ws", "", "", "", rec}, "\t"),
		strings.Join([]string{"grove-g", "301", "ssh", "1", "1700000100", "%9", "2", "/ws", "", "", "groveremote", rec}, "\t"),
		strings.Join([]string{"grove-h", "302", "claude", "1", "1700000100", "%10", "1", "/ws/o", "eeee", "opus"}, "\t"),
	}, "\n")
	panes := ParsePanes(out)
	if len(panes) != 3 {
		t.Fatalf("got %d panes, want 3", len(panes))
	}
	for _, p := range panes[:2] {
		if got := ParseHiddenRemotes(p.HiddenRemote); len(got) != 1 || got[0].Session != "grove-chat-g-1" {
			t.Errorf("pane %s of the cockpit session carries %q", p.Pane, p.HiddenRemote)
		}
	}
	if panes[0].Remote != "" || panes[1].Remote != "groveremote" {
		t.Errorf("the record shifted @grove_remote: %+v", panes[:2])
	}
	if panes[2].HiddenRemote != "" || panes[2].Model != "opus" {
		t.Errorf("a session with nothing hidden = %+v", panes[2])
	}
}

// AC: the 1s path is still exactly ONE tmux invocation with chats hidden —
// the record rides the listing, it is never asked for.
func TestPanesOneExecWithHiddenRemotes(t *testing.T) {
	old := execTmux
	defer func() { execTmux = old }()
	var calls [][]string
	rec := EncodeHiddenRemotes([]HiddenRemote{{Host: "pc", Session: "grove-chat-g-1"}, {Host: "pc", Session: "grove-chat-g-2", Profile: "glm"}})
	execTmux = func(args ...string) (string, error) {
		calls = append(calls, args)
		return "grove-g\t301\tgv\t1\t1700000100\t%9\t1\t/ws\t\t\t\t" + rec, nil
	}
	panes := Panes()
	if len(panes) != 1 || len(ParseHiddenRemotes(panes[0].HiddenRemote)) != 2 {
		t.Fatalf("panes = %+v", panes)
	}
	if len(calls) != 1 || calls[0][0] != "list-panes" || calls[0][1] != "-a" {
		t.Fatalf("Panes ran %v, want exactly one list-panes -a", calls)
	}
}

func TestShowableRemote(t *testing.T) {
	reg := registry("grove-x")
	rec := HiddenRemote{Host: "pc", Session: "grove-chat-x-1"}
	cases := []struct {
		name, cockpit, window string
		recorded              bool
		isCockpit             CockpitCheck
		want                  string
	}{
		{"recorded, cockpit running", "grove-x", "@1", true, reg, ""},
		{"cockpit not running", "grove-x", "", true, reg, "is not running"},
		{"never hidden from this cockpit", "grove-x", "@1", false, reg, "no hidden remote chat grove-chat-x-1 on @pc"},
		{"not a cockpit", "grove-ghost", "@1", true, reg, "not a registered workspace's cockpit"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := showableRemote(c.cockpit, c.window, rec, c.recorded, c.isCockpit)
			if c.want == "" {
				if err != nil {
					t.Fatalf("showableRemote = %v, want showable", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("showableRemote = %v, want a refusal containing %q", err, c.want)
			}
		})
	}
}

// --- isolated socket ---

// fakeHost stands in for the remote host: a second session on the scratch
// server holding the "agent". Returns its name and the agent's pid.
func fakeHost(t *testing.T) (session, pid string) {
	t.Helper()
	session = "grove-chat-x-1"
	mustRun(t, "new-session", "-d", "-s", session, "-n", "chat", "sleep 600")
	return session, mustRun(t, "display-message", "-p", "-t", Exact(session)+":chat", "-F", "#{pane_pid}")
}

// fakeAttach is the attach command with no ssh in it: what the pane runs in
// place of `ssh -t <host> tmux attach -t '=<session>'`. It leaves a marker
// so the test can see that THIS command ran in the pane.
func fakeAttach(t *testing.T, session string) (cmd, marker string) {
	t.Helper()
	marker = filepath.Join(t.TempDir(), "attached")
	return "echo " + session + " > '" + marker + "'; exec sleep 600", marker
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func paneOption(t *testing.T, pane, option string) string {
	t.Helper()
	return mustRun(t, "show-options", "-pqv", "-t", pane, option)
}

// assertRemoteTags: the four tags of a remote chat pane, plus the stamp.
func assertRemoteTags(t *testing.T, pane, window, host, profile, session string) {
	t.Helper()
	if got := paneOption(t, pane, "@grove_remote"); got != host {
		t.Errorf("@grove_remote = %q, want %q", got, host)
	}
	if got := paneOption(t, pane, "@grove_profile"); got != profile {
		t.Errorf("@grove_profile = %q, want %q", got, profile)
	}
	if got := paneOption(t, pane, "pane-border-style"); got != remotePaneBorderStyle {
		t.Errorf("pane-border-style = %q, want %q", got, remotePaneBorderStyle)
	}
	if got := mustRun(t, "show-options", "-wqv", "-t", window, "pane-border-status"); got != "top" {
		t.Errorf("the cockpit window's pane-border-status = %q, want top", got)
	}
	if got := mustRun(t, "show-options", "-wqv", "-t", window, "pane-border-format"); got != paneBorderFormat {
		t.Errorf("the cockpit window's pane-border-format = %q", got)
	}
	if got := paneOption(t, pane, remoteSessionOption); got != session {
		t.Errorf("%s = %q, want %q", remoteSessionOption, got, session)
	}
}

// AC: spawn → stamped; hide → the local pane is gone, the record is there,
// the "remote" session is alive with the same pid; show → a new pane running
// the attach command, all four tags applied, the record gone.
func TestHideShowRemotePane(t *testing.T) {
	dash, chatA, chatB := fakeCockpit(t)
	reg := registry("grove-x")
	win := Exact("grove-x") + ":cockpit"
	winID := paneFmt(t, dash, "#{window_id}")
	// vertical = main-vertical, so a re-tile is visible (see
	// TestHideShowHonorsLayout).
	if err := SetCockpitLayout("grove-x", "vertical"); err != nil {
		t.Fatal(err)
	}
	host, hostPID := fakeHost(t)
	worker := mustRun(t, "new-window", "-d", "-P", "-F", "#{pane_id}", "-t", Exact("grove-x"), "-n", "repo · grove-1", "sleep 600")
	workerLayout := paneFmt(t, worker, "#{window_layout}")
	dir := t.TempDir()

	// Spawn, as spawnRemoteChat does: SpawnPane, then the shared tail.
	attach, marker := fakeAttach(t, host)
	pane, err := SpawnPane("grove-x", dir, attach)
	if err != nil {
		t.Fatalf("SpawnPane: %v", err)
	}
	if w := TagRemotePane(pane, win, "pc", "glm", host); len(w) != 0 {
		t.Fatalf("TagRemotePane warned: %v", w)
	}
	assertRemoteTags(t, pane, win, "pc", "glm", host)
	waitFor(t, "the attach command to run", func() bool { _, err := os.Stat(marker); return err == nil })
	// Invariant: the spawn opens a pane beside the others and hides nothing.
	if got := windowPanes(t, win); !reflect.DeepEqual(got, []string{dash, chatA, chatB, pane}) {
		t.Fatalf("cockpit panes after the spawn = %v", got)
	}
	if got := HiddenRemotes("grove-x"); got != nil {
		t.Fatalf("a spawn recorded a hidden chat: %+v", got)
	}

	// Hide.
	var announced []HiddenRemote
	rec, err := HideRemotePane(pane, reg, func(r HiddenRemote) error {
		// The event lands before anything is closed.
		if got := windowPanes(t, win); len(got) != 4 {
			t.Errorf("announce ran after the pane was closed: %v", got)
		}
		announced = append(announced, r)
		return nil
	})
	if err != nil {
		t.Fatalf("HideRemotePane: %v", err)
	}
	want := HiddenRemote{Host: "pc", Session: host, Profile: "glm"}
	if rec != want || !reflect.DeepEqual(announced, []HiddenRemote{want}) {
		t.Fatalf("hid %+v, announced %+v, want %+v", rec, announced, want)
	}
	if got := windowPanes(t, win); !reflect.DeepEqual(got, []string{dash, chatA, chatB}) {
		t.Fatalf("cockpit panes after the hide = %v, want the attach pane gone and the rest untouched", got)
	}
	if got := HiddenRemotes("grove-x"); !reflect.DeepEqual(got, []HiddenRemote{want}) {
		t.Fatalf("record after the hide = %+v", got)
	}
	// The record reaches the CHATS box through the one listing it already runs.
	seen := false
	for _, p := range Panes() {
		if p.Session == "grove-x" {
			seen = true
			if got := ParseHiddenRemotes(p.HiddenRemote); !reflect.DeepEqual(got, []HiddenRemote{want}) {
				t.Errorf("pane %s lists the record as %+v", p.Pane, got)
			}
		} else if p.HiddenRemote != "" {
			t.Errorf("session %s carries another session's record: %q", p.Session, p.HiddenRemote)
		}
	}
	if !seen {
		t.Fatal("Panes() listed no cockpit pane")
	}
	// The "host" never heard of it.
	if !SessionExists(host) {
		t.Fatal("hide killed the remote session")
	}
	if got := mustRun(t, "display-message", "-p", "-t", Exact(host)+":chat", "-F", "#{pane_pid}"); got != hostPID {
		t.Fatalf("the remote agent's pid changed: %s → %s", hostPID, got)
	}
	if !mainVerticalShape(t, win) {
		t.Fatal("the cockpit window was not re-tiled after the hide")
	}
	if got := paneFmt(t, worker, "#{window_layout}"); got != workerLayout {
		t.Fatalf("worker window layout changed: %s → %s", workerLayout, got)
	}

	// Show — with the operator looking at the worker window, so a re-tile
	// aimed at the active window would hit the wrong one.
	mustRun(t, "select-window", "-t", worker)
	attach, marker = fakeAttach(t, host)
	shown, warnings, err := ShowRemotePane("grove-x", "pc", host, attach, dir, false, reg)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("ShowRemotePane = %q, %v, %v", shown, warnings, err)
	}
	if shown == pane {
		t.Fatalf("show reused the closed pane's id %s", pane)
	}
	if got := windowPanes(t, win); !reflect.DeepEqual(got, []string{dash, chatA, chatB, shown}) {
		t.Fatalf("cockpit panes after the show = %v", got)
	}
	waitFor(t, "the attach command to run in the shown pane", func() bool { _, err := os.Stat(marker); return err == nil })
	waitFor(t, "the shown pane to be running the attach command", func() bool {
		return paneFmt(t, shown, "#{pane_current_command}") == "sleep"
	})
	assertRemoteTags(t, shown, winID, "pc", "glm", host)
	if got := HiddenRemotes("grove-x"); got != nil {
		t.Fatalf("record after the show = %+v, want none", got)
	}
	if _, err := run("show-options", "-v", "-t", ExactActive("grove-x"), hiddenRemoteOption); err == nil {
		t.Error("the option is still set with nothing hidden — every listing would keep carrying it")
	}
	if got := mustRun(t, "display-message", "-p", "-t", Exact(host)+":chat", "-F", "#{pane_pid}"); got != hostPID {
		t.Fatalf("the remote agent's pid changed across the show: %s → %s", hostPID, got)
	}
	if !mainVerticalShape(t, win) {
		t.Fatal("the cockpit window was not re-tiled after the show")
	}
	if got := paneFmt(t, worker, "#{window_layout}"); got != workerLayout {
		t.Fatalf("the show re-tiled the worker window: %s → %s", workerLayout, got)
	}
	// focus=false: the keyboard stayed where it was in the cockpit window.
	if got := activePane(t, win); got == shown {
		t.Error("an unfocused show took the keyboard")
	}

	// Hide again, show focused.
	if _, err := HideRemotePane(shown, reg, nil); err != nil {
		t.Fatalf("second hide: %v", err)
	}
	attach, _ = fakeAttach(t, host)
	focused, _, err := ShowRemotePane("grove-x", "pc", host, attach, dir, true, reg)
	if err != nil {
		t.Fatalf("focused show: %v", err)
	}
	if got := activePane(t, win); got != focused {
		t.Errorf("active pane after a focused show = %s, want %s", got, focused)
	}
}

// Two chats hidden, one shown: the other's record survives, in order.
func TestHideRemotePaneKeepsOtherRecords(t *testing.T) {
	fakeCockpit(t)
	reg := registry("grove-x")
	win := Exact("grove-x") + ":cockpit"
	dir := t.TempDir()
	var panes []string
	for _, s := range []string{"grove-chat-x-1", "grove-chat-x-2"} {
		pane, err := SpawnPane("grove-x", dir, "exec sleep 600")
		if err != nil {
			t.Fatal(err)
		}
		TagRemotePane(pane, win, "pc", "", s)
		panes = append(panes, pane)
	}
	for _, p := range panes {
		if _, err := HideRemotePane(p, reg, nil); err != nil {
			t.Fatal(err)
		}
	}
	a, b := HiddenRemote{Host: "pc", Session: "grove-chat-x-1"}, HiddenRemote{Host: "pc", Session: "grove-chat-x-2"}
	if got := HiddenRemotes("grove-x"); !reflect.DeepEqual(got, []HiddenRemote{a, b}) {
		t.Fatalf("records = %+v", got)
	}
	if _, _, err := ShowRemotePane("grove-x", "pc", a.Session, "exec sleep 600", dir, false, reg); err != nil {
		t.Fatal(err)
	}
	if got := HiddenRemotes("grove-x"); !reflect.DeepEqual(got, []HiddenRemote{b}) {
		t.Fatalf("records after showing one = %+v", got)
	}
	if err := ForgetHiddenRemote("grove-x", "pc", b.Session); err != nil {
		t.Fatal(err)
	}
	if err := ForgetHiddenRemote("grove-x", "pc", b.Session); err != nil {
		t.Fatalf("forgetting twice: %v", err)
	}
	if got := HiddenRemotes("grove-x"); got != nil {
		t.Fatalf("records after forgetting the last = %+v", got)
	}
}

// AC: a remote pane spawned before the stamp existed is refused, and
// nothing moves — by either hide, local or remote.
func TestHideRemotePaneRefusals(t *testing.T) {
	dash, chatA, _ := fakeCockpit(t)
	reg := registry("grove-x")
	win := Exact("grove-x") + ":cockpit"
	legacy := mustRun(t, "split-window", "-d", "-h", "-t", win, "-P", "-F", "#{pane_id}", "sleep 600")
	// Exactly what grove-199 put on a pane, before grove-404.
	if err := SetPaneRemote(legacy, "groveremote"); err != nil {
		t.Fatal(err)
	}
	if err := SetPaneProfile(legacy, "glm"); err != nil {
		t.Fatal(err)
	}
	if err := SetPaneRemoteBorder(legacy); err != nil {
		t.Fatal(err)
	}
	stamped := mustRun(t, "split-window", "-d", "-h", "-t", win, "-P", "-F", "#{pane_id}", "sleep 600")
	TagRemotePane(stamped, win, "groveremote", "", "grove-chat-x-1")
	cases := []struct {
		name, pane string
		isCockpit  CockpitCheck
		want       string
	}{
		{"legacy remote pane", legacy, reg, "close it and re-attach with"},
		{"legacy remote pane, nil check", legacy, nil, "never guessed"},
		{"local chat pane", chatA, reg, "is a local chat, not a remote attachment"},
		{"dashboard", dash, reg, "the dashboard"},
		{"unregistered cockpit", stamped, registry(), "not a registered workspace's cockpit"},
		{"no such pane", "%999", reg, "no such pane"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := serverState(t)
			called := false
			_, err := HideRemotePane(c.pane, c.isCockpit, func(HiddenRemote) error { called = true; return nil })
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("HideRemotePane = %v, want a refusal containing %q", err, c.want)
			}
			if called {
				t.Fatal("announce ran for a refused hide")
			}
			if after := serverState(t); after != before {
				t.Fatalf("a refused hide mutated the server:\n%s\n→\n%s", before, after)
			}
			if got := HiddenRemotes("grove-x"); got != nil {
				t.Fatalf("a refused hide left a record: %+v", got)
			}
		})
	}

	t.Run("the local hide never moves a remote pane", func(t *testing.T) {
		before := serverState(t)
		for _, pane := range []string{legacy, stamped} {
			if _, err := HideChatPane(pane, "x", reg, nil); err == nil {
				t.Fatalf("HideChatPane moved remote pane %s into a chat session", pane)
			}
		}
		if after := serverState(t); after != before {
			t.Fatalf("server changed:\n%s\n→\n%s", before, after)
		}
	})

	t.Run("announce failure closes nothing", func(t *testing.T) {
		before := serverState(t)
		_, err := HideRemotePane(stamped, reg, func(HiddenRemote) error { return errString("log is read-only") })
		if err == nil || !strings.Contains(err.Error(), "log is read-only") {
			t.Fatalf("HideRemotePane = %v, want the announce error", err)
		}
		if after := serverState(t); after != before || HiddenRemotes("grove-x") != nil {
			t.Fatalf("a failed announce left the server changed:\n%s\n→\n%s", before, after)
		}
	})

	t.Run("show refuses what was never hidden", func(t *testing.T) {
		before := serverState(t)
		_, _, err := ShowRemotePane("grove-x", "groveremote", "grove-chat-x-1", "exec sleep 600", t.TempDir(), true, reg)
		if err == nil || !strings.Contains(err.Error(), "no hidden remote chat") {
			t.Fatalf("ShowRemotePane = %v, want a refusal", err)
		}
		if after := serverState(t); after != before {
			t.Fatalf("a refused show mutated the server:\n%s\n→\n%s", before, after)
		}
	})

	// A legacy pane can still be focused: focus needs no stamp.
	if err := FocusChatPane(legacy, reg); err != nil {
		t.Errorf("FocusChatPane on a legacy remote pane: %v", err)
	}
	if err := FocusChatPane(dash, reg); err == nil {
		t.Error("FocusChatPane moved the keyboard onto the dashboard guard's blind side")
	}
}

// No ssh binary is ever looked for by anything in this file's paths: with
// PATH holding only tmux, hide and show still work.
func TestHideShowRemoteNeverRunsSSH(t *testing.T) {
	fakeCockpit(t)
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not installed")
	}
	bin := t.TempDir()
	marker := filepath.Join(bin, "ssh-ran")
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte("#!/bin/sh\necho ran >> '"+marker+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(tmuxPath, filepath.Join(bin, "tmux")); err != nil {
		t.Fatal(err)
	}
	reg := registry("grove-x")
	dir := t.TempDir()
	pane, err := SpawnPane("grove-x", dir, "exec sleep 600")
	if err != nil {
		t.Fatal(err)
	}
	TagRemotePane(pane, Exact("grove-x")+":cockpit", "pc", "", "grove-chat-x-1")
	t.Setenv("PATH", bin)
	if _, err := HideRemotePane(pane, reg, nil); err != nil {
		t.Fatal(err)
	}
	Panes()
	if _, _, err := ShowRemotePane("grove-x", "pc", "grove-chat-x-1", "exec sleep 600", dir, false, reg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("hide/show ran ssh")
	}
}
