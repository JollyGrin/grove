package tui

// grove-149: one refresh reads tmux O(1) times, not O(tasks). The dash's 1s
// tick used to run 3-4 window listings + a list-panes + a display-message
// PER TASK per second; on hosts where process spawn is expensive (EDR, WSL1)
// that pegged the CPU. These tests count what liveStates actually fetches:
// exactly one SessionSnapshot per distinct session per refresh (which is two
// tmux execs, pinned in internal/tmux's TestSnapshotSessionTwoExecs) and one
// detect per active task (one capture per live task, pinned in
// internal/detect's live_from tests).

import (
	"errors"
	"testing"

	"github.com/JollyGrin/grove/internal/detect"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/tmux"
)

// boardSnapshot is the grove-116 board shape: cockpit active, a glyphed
// grove-1 worker, and its prefix-extending sibling grove-10.
func boardSnapshot() *tmux.SessionSnapshot {
	return tmux.ParseSessionSnapshot(
		"@1\t1\tcockpit\n@2\t0\trepo · grove-1 ✳\n@3\t0\trepo · grove-10",
		"@2\t%1\t0\t48\tclaude\n@3\t%3\t0\t42\tclaude",
	)
}

func TestLiveStatesOneSnapshotPerSessionOneDetectPerTask(t *testing.T) {
	snap := boardSnapshot()
	fetches := 0
	snapFor := snapshotSessions(func(session string) (*tmux.SessionSnapshot, error) {
		fetches++
		if session != "grove-test" {
			t.Errorf("fetched session %q, want grove-test", session)
		}
		return snap, nil
	})
	var detected []string
	detectFrom := func(s *tmux.SessionSnapshot, base string) detect.LiveInfo {
		detected = append(detected, base)
		if s != snap {
			t.Errorf("detect for %q got a different snapshot than the fetch", base)
		}
		if !s.WindowExists(base) {
			return detect.LiveInfo{}
		}
		return detect.LiveInfo{Exists: true, HasClaude: true, Status: detect.StatusBusy}
	}

	tasks := []*state.Task{
		{Ticket: "grove-1", TmuxSession: "grove-test", TmuxWindow: "repo · grove-1"},
		{Ticket: "grove-10", TmuxSession: "grove-test", TmuxWindow: "repo · grove-10"},
		{Ticket: "grove-99", TmuxSession: "grove-test", TmuxWindow: "repo · grove-99"},
	}
	live, focused, _ := liveStates(tasks, "grove-test", snapFor, detectFrom)

	// The batched contract: 3 tasks + the active-window read = ONE snapshot.
	if fetches != 1 {
		t.Errorf("refresh fetched %d snapshots, want exactly 1 for the shared session", fetches)
	}
	if len(detected) != 3 {
		t.Errorf("detect ran %d times, want once per active task (3): %v", len(detected), detected)
	}
	want := map[string]string{"grove-1": "busy", "grove-10": "busy", "grove-99": "gone"}
	for ticket, status := range want {
		if live[ticket] != status {
			t.Errorf("live[%s] = %q, want %q", ticket, live[ticket], status)
		}
	}
	// Cockpit window is active — no worker focused.
	if focused != "" {
		t.Errorf("focused = %q, want none while the cockpit window is active", focused)
	}
}

// The grove-63 focus read rides the same snapshot: a glyphed active window
// must match its task's stored base name, and a prefix sibling must not
// steal the focus (grove-47 + grove-116 semantics in the batched path).
func TestLiveStatesFocusedGlyphTolerant(t *testing.T) {
	snap := tmux.ParseSessionSnapshot(
		"@1\t0\tcockpit\n@2\t1\trepo · grove-1 ✳\n@3\t0\trepo · grove-10",
		"@2\t%1\t0\t48\tclaude\n@3\t%3\t0\t42\tclaude",
	)
	snapFor := snapshotSessions(func(string) (*tmux.SessionSnapshot, error) { return snap, nil })
	detectFrom := func(s *tmux.SessionSnapshot, base string) detect.LiveInfo {
		return detect.LiveInfo{Exists: s.WindowExists(base), Status: detect.StatusIdle}
	}
	tasks := []*state.Task{
		{Ticket: "grove-10", TmuxSession: "grove-test", TmuxWindow: "repo · grove-10"},
		{Ticket: "grove-1", TmuxSession: "grove-test", TmuxWindow: "repo · grove-1"},
	}
	_, focused, _ := liveStates(tasks, "grove-test", snapFor, detectFrom)
	if focused != "grove-1" {
		t.Errorf("focused = %q, want grove-1 (glyphed active window, sibling must not match)", focused)
	}
}

// A dead session (SnapshotSession errors) memoizes nil and reads as gone —
// and still costs exactly one fetch attempt per refresh, not one per task.
func TestLiveStatesDeadSessionMemoized(t *testing.T) {
	fetches := 0
	snapFor := snapshotSessions(func(string) (*tmux.SessionSnapshot, error) {
		fetches++
		return nil, errors.New("no server running")
	})
	detectFrom := func(s *tmux.SessionSnapshot, base string) detect.LiveInfo {
		if s != nil {
			t.Errorf("dead session should hand detect a nil snapshot")
		}
		return detect.LiveInfo{}
	}
	tasks := []*state.Task{
		{Ticket: "grove-1", TmuxSession: "grove-test", TmuxWindow: "repo · grove-1"},
		{Ticket: "grove-10", TmuxSession: "grove-test", TmuxWindow: "repo · grove-10"},
	}
	live, focused, _ := liveStates(tasks, "grove-test", snapFor, detectFrom)
	if fetches != 1 {
		t.Errorf("dead session fetched %d times, want 1 (nil memoized)", fetches)
	}
	if live["grove-1"] != "gone" || live["grove-10"] != "gone" || focused != "" {
		t.Errorf("dead session: live=%v focused=%q, want all gone / no focus", live, focused)
	}
}
