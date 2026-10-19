package tmux

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// cockpit grove-alpha: dash %0, chat %1 (139x45), rail %2; plus whatever
// hidden windows the case adds.
func slotCockpit(chats ...SizePane) []SizePane {
	c := func(id string, idx, w, h int) SizePane {
		return SizePane{Session: "grove-alpha", WindowID: "@0", WindowName: "cockpit", WindowW: 200, WindowH: 50, Pane: id, Index: idx, PaneW: w, PaneH: h}
	}
	panes := []SizePane{c("%0", 1, 60, 50)}
	panes = append(panes, chats...)
	rail := c("%2", 9, 139, 4)
	rail.Rail = true
	return append(panes, rail)
}

func shownChat(w, h int) SizePane {
	return SizePane{Session: "grove-alpha", WindowID: "@0", WindowName: "cockpit", WindowW: 200, WindowH: 50, Pane: "%1", Index: 2, PaneW: w, PaneH: h}
}

func hidden(session, win string, w, h int) SizePane {
	return SizePane{Session: session, WindowID: win, WindowName: "chat", WindowW: w, WindowH: h, Pane: "%" + win[1:] + "0", Index: 1, PaneW: w, PaneH: h}
}

func pin(win string) [][]string {
	return [][]string{
		{"set-option", "-w", "-t", win, "window-size", "manual"},
		{"resize-window", "-t", win, "-x", "139", "-y", "45"},
	}
}

func TestPlanHiddenSizes(t *testing.T) {
	registry := CockpitCheck(func(s string) bool { return s == "grove-alpha" || s == "grove-chat-alpha-7" })
	remote := hidden("grove-chat-alpha-3", "@3", 80, 24)
	remote.Remote = true
	tests := []struct {
		name  string
		panes []SizePane
		want  [][]string
	}{
		{"slot equal: nothing", append(slotCockpit(shownChat(139, 45)), hidden("grove-chat-alpha-1", "@1", 139, 45)), nil},
		{"slot differs: pin + resize", append(slotCockpit(shownChat(139, 45)), hidden("grove-chat-alpha-1", "@1", 80, 24)), pin("@1")},
		{"only height differs", append(slotCockpit(shownChat(139, 45)), hidden("grove-chat-alpha-1", "@1", 139, 50)), pin("@1")},
		{"no chat shown: no slot", append(slotCockpit(), hidden("grove-chat-alpha-1", "@1", 80, 24)), nil},
		{"two local chats mid-move: no slot", append(slotCockpit(shownChat(139, 22), SizePane{Session: "grove-alpha", WindowName: "cockpit", Pane: "%5", Index: 3, PaneW: 139, PaneH: 22}), hidden("grove-chat-alpha-1", "@1", 80, 24)), nil},
		{"remote shown beside the chat: slot is the local one", append(slotCockpit(shownChat(139, 45), SizePane{Session: "grove-alpha", WindowName: "cockpit", Pane: "%6", Index: 3, PaneW: 139, PaneH: 10, Remote: true}), hidden("grove-chat-alpha-1", "@1", 80, 24)), pin("@1")},
		{"remote chat skipped", append(slotCockpit(shownChat(139, 45)), remote), nil},
		{"hand-hidden and minimized alike", append(slotCockpit(shownChat(139, 45)), hidden("grove-chat-alpha-1", "@1", 80, 24), hidden("grove-chat-alpha-2", "@2", 100, 30)), append(pin("@1"), pin("@2")...)},
		{"other workspaces' chats untouched", append(slotCockpit(shownChat(139, 45)),
			hidden("grove-chat-beta-1", "@4", 80, 24),    // another label
			hidden("grove-chat-alpha-b-1", "@5", 80, 24), // label alpha-b, not chat n of alpha
			hidden("grove-chat-alpha-7", "@6", 80, 24),   // the registry says: a cockpit
			hidden("grove-alpha", "@7", 80, 24),          // the cockpit's other windows
			hidden("grove-chat-alpha-1", "@1", 80, 24)), pin("@1")},
		{"one window per chat, however many panes", append(slotCockpit(shownChat(139, 45)), hidden("grove-chat-alpha-1", "@1", 80, 24), hidden("grove-chat-alpha-1", "@1", 80, 24)), pin("@1")},
		{"another cockpit's slot is not ours", append(slotCockpit(), SizePane{Session: "grove-beta", WindowName: "cockpit", Pane: "%8", Index: 2, PaneW: 139, PaneH: 45}, hidden("grove-chat-alpha-1", "@1", 80, 24)), nil},
	}
	for _, tc := range tests {
		got := PlanHiddenSizes(tc.panes, "grove-alpha", "alpha", registry)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got  %v\n want %v", tc.name, got, tc.want)
		}
	}
	// A nil cockpit check reads every session as a cockpit: nothing sized.
	if got := PlanHiddenSizes(append(slotCockpit(shownChat(139, 45)), hidden("grove-chat-alpha-1", "@1", 80, 24)), "grove-alpha", "alpha", nil); got != nil {
		t.Errorf("nil check: got %v", got)
	}
}

func TestPlanHiddenSizeReset(t *testing.T) {
	registry := CockpitCheck(func(s string) bool { return s == "grove-alpha" })
	panes := append(slotCockpit(shownChat(139, 45)), hidden("grove-chat-alpha-1", "@1", 139, 45), hidden("grove-chat-beta-1", "@4", 139, 45), hidden("grove-chat-alpha-2", "@2", 80, 24))
	want := [][]string{
		{"set-option", "-wu", "-t", "@1", "window-size"},
		{"set-option", "-wu", "-t", "@2", "window-size"},
	}
	if got := PlanHiddenSizeReset(panes, "alpha", registry); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestParseSizePanes(t *testing.T) {
	out := "grove-alpha\t@0\tcockpit\t200\t50\t%1\t2\t139\t45\t\t\t.\n" +
		"grove-alpha\t@0\tcockpit\t200\t50\t%2\t3\t139\t4\t\t1\t.\n" +
		"grove-chat-alpha-1\t@3\tchat\t80\t24\t%3\t0\t80\t24\thostA\t\t."
	want := []SizePane{
		{Session: "grove-alpha", WindowID: "@0", WindowName: "cockpit", WindowW: 200, WindowH: 50, Pane: "%1", Index: 2, PaneW: 139, PaneH: 45},
		{Session: "grove-alpha", WindowID: "@0", WindowName: "cockpit", WindowW: 200, WindowH: 50, Pane: "%2", Index: 3, PaneW: 139, PaneH: 4, Rail: true},
		{Session: "grove-chat-alpha-1", WindowID: "@3", WindowName: "chat", WindowW: 80, WindowH: 24, Pane: "%3", PaneW: 80, PaneH: 24, Remote: true},
	}
	if got := parseSizePanes(out); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestSyncSizesCommand(t *testing.T) {
	got := SyncSizesCommand("grove-alpha")
	if !strings.HasPrefix(got, `run-shell -b "'`) || !strings.Contains(got, ` chat sync-sizes --session 'grove-alpha' >/dev/null 2>&1 || true"`) {
		t.Errorf("hook command = %s", got)
	}
	if exe, err := os.Executable(); err == nil && !strings.Contains(got, exe) {
		t.Errorf("hook command %s does not run the absolute binary %s", got, exe)
	}
	if q := tmuxQuote(`a"b\c$d#e`); q != `"a\"b\\c\$d##e"` {
		t.Errorf("tmuxQuote = %s", q)
	}
}

// window-size is set per window or not at all: `set -g window-size …` kills
// the tmux server on the next new window (LEARNINGS 2026-10-19). Fails on
// any Go or shell source that sets window-size without -w — a source-level
// tripwire, so the rule cannot regress in a path no test exercises.
func TestNoGlobalWindowSize(t *testing.T) {
	root := filepath.Join("..", "..")
	setter := regexp.MustCompile(`(set-option|"set"|\bset\b)`)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "docs":
				return filepath.SkipDir
			}
			return nil
		}
		if ext := filepath.Ext(path); ext != ".go" && ext != ".sh" && ext != ".conf" {
			return nil
		}
		if strings.HasSuffix(path, "slot_test.go") {
			return nil // this file names the forbidden shape on purpose
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			if !strings.Contains(line, "window-size") || !setter.MatchString(line) {
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(line), "//") || strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			// The hostile e2e conf plays an OPERATOR's own tmux.conf, which
			// may set it globally; grove itself never does.
			if strings.Contains(line, "grove-e2e: user-conf fixture") {
				continue
			}
			if !regexp.MustCompile(`"-w"|"-wu"|\s-w\b|\s-wu\b`).MatchString(line) || strings.Contains(line, `"-g",`) {
				t.Errorf("%s:%d sets window-size without -w (never global — it kills the tmux server): %s", path, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Live, on a scratch server: RetileFocus sizes a hidden chat window to the
// slot it produced, pinned per window; the reset unpins it again; the
// rail's resize hook carries the sync command.
func TestRetileFocusSyncsHiddenSizes(t *testing.T) {
	scratchServer(t)
	dir := t.TempDir()
	const s = "grove-alpha"
	if _, err := run("new-session", "-d", "-s", s, "-n", "cockpit", "-x", "200", "-y", "50", "-c", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := run("split-window", "-t", ExactActive(s), "-c", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := run("new-session", "-d", "-s", "grove-chat-alpha-1", "-n", "chat", "-c", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := run("new-session", "-d", "-s", "grove-chat-beta-1", "-n", "chat", "-c", dir); err != nil {
		t.Fatal(err)
	}
	old := FocusWorkspace
	t.Cleanup(func() { FocusWorkspace = old })
	FocusWorkspace = func(string) (string, CockpitCheck, bool) {
		return "alpha", func(x string) bool { return x == s }, true
	}
	if _, err := EnsureRail(s, dir, "sleep 600"); err != nil {
		t.Fatal(err)
	}
	if err := RetileFocus(s); err != nil {
		t.Fatal(err)
	}
	panes, err := sizePanes()
	if err != nil {
		t.Fatal(err)
	}
	slot, ok := FocusSlot(panes, s)
	if !ok {
		t.Fatalf("no slot in %+v", panes)
	}
	size := func(session string) string {
		out, _ := run("display-message", "-p", "-t", Exact(session)+":chat", "#{window_width}x#{window_height}")
		return out
	}
	if got, want := size("grove-chat-alpha-1"), strconv.Itoa(slot.W)+"x"+strconv.Itoa(slot.H); got != want {
		t.Errorf("hidden alpha window %s, want slot %s", got, want)
	}
	if got := size("grove-chat-beta-1"); got != "80x24" {
		t.Errorf("another workspace's chat was resized to %s", got)
	}
	if out, _ := run("show-options", "-w", "-t", Exact("grove-chat-alpha-1")+":chat", "window-size"); !strings.Contains(out, "manual") {
		t.Errorf("hidden window not pinned per window: %q", out)
	}
	if out, _ := run("show-options", "-g", "window-size"); strings.Contains(out, "manual") {
		t.Errorf("window-size went global: %q", out)
	}
	if out, _ := run("show-hooks", "-w", "-t", cockpitWindowTarget(s)); !strings.Contains(out, "chat sync-sizes --session 'grove-alpha'") {
		t.Errorf("resize hook lacks the sync: %q", out)
	}
	if err := ResetHiddenSizes("alpha", func(x string) bool { return x == s }); err != nil {
		t.Fatal(err)
	}
	if out, _ := run("show-options", "-w", "-t", Exact("grove-chat-alpha-1")+":chat", "window-size"); strings.TrimSpace(out) != "" {
		t.Errorf("window-size still set after reset: %q", out)
	}
}
