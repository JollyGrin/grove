package tmux

import (
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestDecideSwap(t *testing.T) {
	ok := SwapInput{Layout: "focus", Shown: "%1", Slot: Slot{W: 139, H: 45}, TargetPanes: 1, TargetW: 139, TargetH: 45}
	with := func(f func(*SwapInput)) SwapInput { in := ok; f(&in); return in }
	tests := []struct {
		name string
		in   SwapInput
		want SwapDecision
	}{
		{"slot-sized local target", ok, SwapDecision{Fast: true}},
		{"horizontal", with(func(in *SwapInput) { in.Layout = "horizontal" }), SwapDecision{Reason: SwapNotFocus}},
		{"layout unset", with(func(in *SwapInput) { in.Layout = "" }), SwapDecision{Reason: SwapNotFocus}},
		{"remote", with(func(in *SwapInput) { in.Remote = true }), SwapDecision{Reason: SwapRemote}},
		{"nothing shown", with(func(in *SwapInput) { in.Shown, in.Slot = "", Slot{} }), SwapDecision{Reason: SwapNothingShown}},
		{"two panes", with(func(in *SwapInput) { in.TargetPanes = 2 }), SwapDecision{Reason: SwapPanes}},
		{"no target pane", with(func(in *SwapInput) { in.TargetPanes = 0 }), SwapDecision{Reason: SwapPanes}},
		{"width differs", with(func(in *SwapInput) { in.TargetW = 80 }), SwapDecision{Reason: SwapSize}},
		{"height differs", with(func(in *SwapInput) { in.TargetH = 24 }), SwapDecision{Reason: SwapSize}},
		{"never synced (80x24)", with(func(in *SwapInput) { in.TargetW, in.TargetH = 80, 24 }), SwapDecision{Reason: SwapSize}},
		// Precedence: the cheapest, most structural reason wins.
		{"horizontal beats size", with(func(in *SwapInput) { in.Layout, in.TargetW = "horizontal", 80 }), SwapDecision{Reason: SwapNotFocus}},
		{"remote beats panes", with(func(in *SwapInput) { in.Remote, in.TargetPanes = true, 2 }), SwapDecision{Reason: SwapRemote}},
	}
	for _, tc := range tests {
		if got := DecideSwap(tc.in); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestSwapInput(t *testing.T) {
	shown := shownChat(139, 45)
	shown.Home = "grove-chat-alpha-3"
	panes := append(slotCockpit(shown), hidden("grove-chat-alpha-1", "@5", 139, 45), hidden("grove-chat-alpha-2", "@6", 80, 24))
	in, sp, tp := swapInput(panes, "grove-alpha", "focus", "grove-chat-alpha-1")
	want := SwapInput{Layout: "focus", Shown: "%1", Slot: Slot{W: 139, H: 45}, TargetPanes: 1, TargetW: 139, TargetH: 45}
	if in != want || sp.Pane != "%1" || sp.Home != "grove-chat-alpha-3" || tp.Pane != "%50" {
		t.Errorf("got %+v shown %s target %s", in, sp.Pane, tp.Pane)
	}
	if in, _, _ := swapInput(panes, "grove-alpha", "focus", "grove-chat-alpha-2"); DecideSwap(in).Reason != SwapSize {
		t.Errorf("80x24 target: %+v", in)
	}
	// A split hidden session: two panes.
	split := hidden("grove-chat-alpha-1", "@5", 139, 45)
	split.Pane = "%99"
	if in, _, _ := swapInput(append(panes, split), "grove-alpha", "focus", "grove-chat-alpha-1"); in.TargetPanes != 2 {
		t.Errorf("split target: %+v", in)
	}
	// Nothing on screen but the dash and the rail.
	if in, _, _ := swapInput(append(slotCockpit(), hidden("grove-chat-alpha-1", "@5", 139, 45)), "grove-alpha", "focus", "grove-chat-alpha-1"); in.Shown != "" || DecideSwap(in).Reason != SwapNothingShown {
		t.Errorf("no chat shown: %+v", in)
	}
	// A remote attachment in the window is not the slot; a remote target is flagged.
	remote := hidden("grove-chat-alpha-4", "@7", 139, 45)
	remote.Remote = true
	if in, _, _ := swapInput(append(panes, remote), "grove-alpha", "focus", "grove-chat-alpha-4"); !in.Remote {
		t.Errorf("remote target: %+v", in)
	}
}

func TestPickHome(t *testing.T) {
	sessions := []string{"grove-alpha", "grove-chat-alpha-1", "grove-chat-alpha-2", "grove-chat-beta-3"}
	tests := []struct {
		name, stamp, self string
		homes             []string
		want              string
	}{
		{"stamp free", "grove-chat-alpha-5", "", nil, "grove-chat-alpha-5"},
		{"stamp held by the session being renamed", "grove-chat-alpha-2", "grove-chat-alpha-2", nil, "grove-chat-alpha-2"},
		{"stamp taken by another session", "grove-chat-alpha-1", "grove-chat-alpha-2", nil, "grove-chat-alpha-3"},
		{"no stamp: lowest free", "", "", nil, "grove-chat-alpha-3"},
		{"no stamp: another chat's home is reserved", "", "", []string{"grove-chat-alpha-3"}, "grove-chat-alpha-4"},
		{"another workspace's name is no stamp", "grove-chat-beta-9", "", nil, "grove-chat-alpha-3"},
		{"garbage stamp", "nope", "", nil, "grove-chat-alpha-3"},
	}
	for _, tc := range tests {
		if got := PickHome(tc.stamp, "alpha", tc.self, sessions, tc.homes); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestSwapFocusHid(t *testing.T) {
	tests := []struct {
		name    string
		list    []string
		out, in string
		want    []string
	}{
		{"replaced in place", []string{"a", "b", "c"}, "b", "x", []string{"a", "x", "c"}},
		{"same name (rename to itself)", []string{"a", "b"}, "b", "b", []string{"a", "b"}},
		{"hand-hidden target: appended", []string{"a"}, "h", "x", []string{"a", "x"}},
		{"empty", nil, "h", "x", []string{"x"}},
		{"in already listed: never twice", []string{"x", "b"}, "b", "x", []string{"x"}},
	}
	for _, tc := range tests {
		if got := SwapFocusHid(tc.list, tc.out, tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPlanSwap(t *testing.T) {
	shown := SizePane{Pane: "%1", Home: "grove-chat-alpha-3"}
	tgt := SizePane{Pane: "%5", Home: "grove-chat-alpha-1"}
	got := strings.Join(planSwap("grove-alpha", shown, tgt, "grove-chat-alpha-1", "grove-chat-alpha-3", []string{"grove-chat-alpha-1", "grove-chat-alpha-2"}, true), " ")
	want := "swap-pane -d -s %5 -t %1 ; rename-session -t =grove-chat-alpha-1 grove-chat-alpha-3 ; " +
		"set-option -t =grove-alpha: @grove_focus_hid grove-chat-alpha-3 grove-chat-alpha-2 ; select-pane -t %5"
	if got != want {
		t.Errorf("stamped:\n got %s\nwant %s", got, want)
	}
	// Neither stamped (chats from before homes): the target takes the
	// session it leaves, the shown chat its new name; no select-pane.
	got = strings.Join(planSwap("grove-alpha", SizePane{Pane: "%1"}, SizePane{Pane: "%5"}, "grove-chat-alpha-1", "grove-chat-alpha-2", nil, false), " ")
	want = "set-option -p -t %5 @grove_chat_home grove-chat-alpha-1 ; swap-pane -d -s %5 -t %1 ; " +
		"rename-session -t =grove-chat-alpha-1 grove-chat-alpha-2 ; set-option -p -t %1 @grove_chat_home grove-chat-alpha-2 ; " +
		"set-option -t =grove-alpha: @grove_focus_hid grove-chat-alpha-2"
	if got != want {
		t.Errorf("unstamped:\n got %s\nwant %s", got, want)
	}
}

// winchChat is a chat stand-in that prints a line whenever it is sent
// SIGWINCH — a resize is visible in its capture, as a repaint is in Claude's.
const winchChat = `sh -c 'trap "echo WINCH" WINCH; echo ready; while :; do sleep 0.1; done'`

// Live, on a scratch server: a focus cockpit with three chats, two
// minimized at the slot's size. Six swaps: each is one swap-pane, the
// swapped-in pane's screen is byte-identical before and after (no SIGWINCH),
// the layout never changes, and every chat keeps its session name.
func TestFocusSwapLive(t *testing.T) {
	// "top": the operator's own `set -g pane-border-status top` makes every
	// lone hidden pane a row shorter than its window (grove-526).
	for _, border := range []string{"off", "top"} {
		t.Run("pane-border-status="+border, func(t *testing.T) { focusSwapLive(t, border) })
	}
}

func focusSwapLive(t *testing.T, border string) {
	scratchServer(t)
	dir := t.TempDir()
	const s = "grove-x"
	reg := registry(s)
	mustRun(t, "new-session", "-d", "-s", "boot", "-c", dir)
	mustRun(t, "set-option", "-g", "base-index", "1")
	mustRun(t, "set-option", "-g", "pane-base-index", "1")
	mustRun(t, "set-option", "-g", "automatic-rename", "off")
	mustRun(t, "set-option", "-g", "pane-border-status", border)
	mustRun(t, "new-session", "-d", "-s", s, "-n", "cockpit", "-x", "200", "-y", "50", "-c", dir, "sleep 600")
	win := Exact(s) + ":cockpit"
	dash, err := FirstPaneID(win)
	if err != nil {
		t.Fatal(err)
	}
	var chats []string
	for i := 0; i < 3; i++ {
		chats = append(chats, mustRun(t, "split-window", "-h", "-t", win, "-c", dir, "-P", "-F", "#{pane_id}", winchChat))
	}
	old := FocusWorkspace
	t.Cleanup(func() { FocusWorkspace = old })
	FocusWorkspace = func(string) (string, CockpitCheck, bool) { return "x", reg, true }
	if err := SetCockpitLayout(s, "focus"); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "select-pane", "-t", chats[0])
	if err := MinimizeChats(s, "x", reg, nil); err != nil {
		t.Fatal(err)
	}
	if err := RetileFocus(s); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond) // the off-screen resizes' WINCH lines land

	pids := map[string]string{}
	for _, c := range chats {
		pids[c] = paneFmt(t, c, "#{pane_pid}")
	}
	homes := map[string]string{} // pane → the session name it must keep
	checkNames := func(step int) {
		t.Helper()
		var hiddenSessions []string
		for _, c := range chats {
			sess := paneFmt(t, c, "#{session_name}")
			home := paneFmt(t, c, "#{@grove_chat_home}")
			if pid := paneFmt(t, c, "#{pane_pid}"); pid != pids[c] {
				t.Fatalf("step %d: %s pid %s, want %s", step, c, pid, pids[c])
			}
			if sess == s {
				continue
			}
			hiddenSessions = append(hiddenSessions, sess)
			if home != sess {
				t.Fatalf("step %d: %s hidden in %s but its home is %q", step, c, sess, home)
			}
			if want, ok := homes[c]; ok && want != sess {
				t.Fatalf("step %d: %s moved from session %s to %s", step, c, want, sess)
			}
			homes[c] = sess
		}
		hid := FocusHid(s)
		sort.Strings(hid)
		sort.Strings(hiddenSessions)
		if !reflect.DeepEqual(hid, hiddenSessions) {
			t.Fatalf("step %d: @grove_focus_hid %v, hidden %v", step, hid, hiddenSessions)
		}
	}
	checkNames(0)

	shown := chats[0]
	for step := 1; step <= 6; step++ {
		tgt := chats[step%3]
		if step == 4 { // reverse once so every pair trades in both directions
			tgt = chats[(step+1)%3]
		}
		if tgt == shown {
			tgt = chats[(step+2)%3]
		}
		target := paneFmt(t, tgt, "#{session_name}")
		before := mustRun(t, "capture-pane", "-p", "-t", tgt)
		geometry := func() string { // window_layout names pane ids; the cells are what a re-tile changes
			return mustRun(t, "list-panes", "-t", win, "-F", "#{pane_left},#{pane_top},#{pane_width}x#{pane_height}")
		}
		layout := geometry()
		focus := step%2 == 1
		if !focus {
			mustRun(t, "select-pane", "-t", dash)
		}
		start := time.Now()
		res, err := FocusSwap(s, "x", "focus", target, focus)
		took := time.Since(start)
		if err != nil || !res.Decision.Fast {
			t.Fatalf("step %d: FocusSwap(%s) = %+v, %v", step, target, res, err)
		}
		t.Logf("step %d: fast swap %s ↔ %s in %v", step, shown, tgt, took)
		if res.Shown != tgt || res.Hidden != shown || res.From != target {
			t.Fatalf("step %d: result %+v", step, res)
		}
		if got := paneFmt(t, shown, "#{session_name}"); got != res.HiddenAs {
			t.Fatalf("step %d: %s is in %s, result says %s", step, shown, got, res.HiddenAs)
		}
		if got := windowPanes(t, win); len(got) != 2 || got[1] != tgt {
			t.Fatalf("step %d: cockpit panes %v, want dash + %s", step, got, tgt)
		}
		if got := geometry(); got != layout {
			t.Fatalf("step %d: layout changed %s → %s", step, layout, got)
		}
		wantActive := dash
		if focus {
			wantActive = tgt
		}
		if got := activePane(t, win); got != wantActive {
			t.Fatalf("step %d (focus=%v): active %s, want %s", step, focus, got, wantActive)
		}
		time.Sleep(300 * time.Millisecond) // a WINCH would have printed by now
		if after := mustRun(t, "capture-pane", "-p", "-t", tgt); after != before {
			t.Fatalf("step %d: %s repainted:\n%s\n→\n%s", step, tgt, before, after)
		}
		checkNames(step)
		shown = tgt
	}
}

// Every fallback reason leaves the server untouched, so the caller's
// break/join path runs on exactly what it always did.
func TestFocusSwapFallbacksTouchNothing(t *testing.T) {
	_, chatA, chatB := fakeCockpit(t)
	const s = "grove-x"
	reg := registry(s)
	old := FocusWorkspace
	t.Cleanup(func() { FocusWorkspace = old })
	FocusWorkspace = func(string) (string, CockpitCheck, bool) { return "x", reg, true }
	if err := SetCockpitLayout(s, "focus"); err != nil {
		t.Fatal(err)
	}
	session, err := HideChatPane(chatB, "x", reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := paneFmt(t, chatB, "#{@grove_chat_home}"); got != session {
		t.Fatalf("hide stamped home %q, want %s", got, session)
	}
	state := func() string {
		return serverState(t) + "\n" + mustRun(t, "list-panes", "-a", "-F", "#{pane_id} #{window_width}x#{window_height} #{@grove_chat_home}") + "\n" + strings.Join(FocusHid(s), " ")
	}
	expect := func(name, layout, reason string) {
		t.Helper()
		before := state()
		res, err := FocusSwap(s, "x", layout, session, true)
		if err != nil || res.Decision.Fast || res.Decision.Reason != reason {
			t.Fatalf("%s: %+v, %v; want fallback %q", name, res, err, reason)
		}
		if after := state(); after != before {
			t.Fatalf("%s: a fallback decision moved something:\n%s\n→\n%s", name, before, after)
		}
	}
	expect("horizontal", "horizontal", SwapNotFocus)
	// Hidden while the slot was measured: now pin it to some other size.
	mustRun(t, "resize-window", "-t", Exact(session)+":chat", "-x", "80", "-y", "24")
	expect("size mismatch", "focus", SwapSize)
	if err := SyncHiddenSizes(s, "x", reg); err != nil {
		t.Fatal(err)
	}
	extra := mustRun(t, "split-window", "-t", Exact(session)+":chat", "-P", "-F", "#{pane_id}", "sleep 600")
	expect("two panes", "focus", SwapPanes)
	mustRun(t, "kill-pane", "-t", extra)
	if err := SyncHiddenSizes(s, "x", reg); err != nil {
		t.Fatal(err)
	}
	if _, err := HideChatPane(chatA, "x", reg, nil); err != nil {
		t.Fatal(err)
	}
	expect("nothing shown", "focus", SwapNothingShown)
	// The fallback still produces today's result, and the target keeps its
	// home through a show and the next hide.
	if _, err := ShowChatPane(session, s, reg, nil); err != nil {
		t.Fatal(err)
	}
	again, err := HideChatPane(chatB, "x", reg, nil)
	if err != nil || again != session {
		t.Fatalf("re-hide = %s, %v; want its home %s", again, err, session)
	}
}

// A chat hidden before homes existed takes the session it leaves as its
// home on its next show, and hides back under that name — even when a
// lower number is free.
func TestShowStampsLegacyHome(t *testing.T) {
	_, chatA, _ := fakeCockpit(t)
	reg := registry("grove-x")
	mustRun(t, "new-session", "-d", "-s", "grove-chat-x-4", "-n", "chat", "sleep 600")
	legacy := mustRun(t, "list-panes", "-t", Exact("grove-chat-x-4")+":chat", "-F", "#{pane_id}")
	if _, err := ShowChatPane("grove-chat-x-4", "grove-x", reg, nil); err != nil {
		t.Fatal(err)
	}
	if got := paneFmt(t, legacy, "#{@grove_chat_home}"); got != "grove-chat-x-4" {
		t.Fatalf("legacy home = %q", got)
	}
	// chatA hides first and must not take the shown chat's reserved name.
	a, err := HideChatPane(chatA, "x", reg, nil)
	if err != nil || a != "grove-chat-x-1" {
		t.Fatalf("hide A = %s, %v", a, err)
	}
	if got, err := HideChatPane(legacy, "x", reg, nil); err != nil || got != "grove-chat-x-4" {
		t.Fatalf("hide legacy = %s, %v; want grove-chat-x-4", got, err)
	}
}
