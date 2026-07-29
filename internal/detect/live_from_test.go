// grove-149: DetectLiveFrom answers from a per-tick SessionSnapshot, so its
// only tmux exec is the one capture-pane per LIVE task. These tests count
// captures through the seam and pin that the snapshot path classifies and
// resolves (glyphs, prefix siblings) exactly like the stateless DetectLive.
package detect

import (
	"testing"

	"github.com/JollyGrin/grove/internal/tmux"
)

const (
	idlePane = "Claude Code v1.0.0\n❯ type a message\n/help for commands"
	busyPane = "✢ Reticulating…\nesc to interrupt"
)

// fromFixture is the grove-116 board shape: a glyphed grove-1 worker with
// claude in pane 1, and its prefix-extending sibling grove-10 running claude
// as the only pane (lost split, version-string process title).
func fromFixture() *tmux.SessionSnapshot {
	return tmux.ParseSessionSnapshot(
		"@1\t1\tcockpit\n@2\t0\trepo · grove-1 ✳\n@3\t0\trepo · grove-10",
		"@1\t%0\t0\t50\tgv\n@2\t%1\t0\t48\tzsh\n@2\t%2\t1\t48\tclaude\n@3\t%3\t0\t42\t2.1.197",
	)
}

// swapCapture installs a canned capture and returns a pointer to the count
// of captures performed plus a record of targeted pane ids.
func swapCapture(t *testing.T, content string) (*int, *[]string) {
	t.Helper()
	old := captureBottomKnown
	t.Cleanup(func() { captureBottomKnown = old })
	count, targets := 0, []string{}
	captureBottomKnown = func(target string, height, lines int) (string, error) {
		count++
		targets = append(targets, target)
		if height <= 0 {
			t.Errorf("capture with height %d — snapshot height not plumbed through", height)
		}
		return content, nil
	}
	return &count, &targets
}

func TestDetectLiveFromClassifies(t *testing.T) {
	snap := fromFixture()

	count, targets := swapCapture(t, idlePane)
	info := DetectLiveFrom(snap, "repo · grove-1")
	if !info.Exists || !info.HasClaude || info.Status != StatusIdle {
		t.Errorf("idle worker: got %+v, want exists/idle/claude", info)
	}
	if *count != 1 || (*targets)[0] != "%2" {
		t.Errorf("glyphed worker captured %d times at %v, want once at its own claude pane %%2 (never the sibling)", *count, *targets)
	}

	count, targets = swapCapture(t, busyPane)
	info = DetectLiveFrom(snap, "repo · grove-10")
	if !info.Exists || info.Status != StatusBusy {
		t.Errorf("busy worker: got %+v, want exists/busy", info)
	}
	if *count != 1 || (*targets)[0] != "%3" {
		t.Errorf("sibling captured %d times at %v, want once at %%3", *count, *targets)
	}
}

func TestDetectLiveFromGoneWindowNeverCaptures(t *testing.T) {
	// grove-116 shape: grove-1's window is dead, only the sibling lives.
	snap := tmux.ParseSessionSnapshot("@3\t0\trepo · grove-10", "@3\t%3\t0\t42\tclaude")

	count, _ := swapCapture(t, idlePane)
	info := DetectLiveFrom(snap, "repo · grove-1")
	if info.Exists {
		t.Errorf("dead window read as live: %+v — resolved via prefix sibling", info)
	}
	if *count != 0 {
		t.Errorf("dead window captured %d times, want 0", *count)
	}

	// A nil snapshot (session gone) behaves the same.
	info = DetectLiveFrom(nil, "repo · grove-1")
	if info.Exists || *count != 0 {
		t.Errorf("nil snapshot: got %+v after %d captures, want gone/0", info, *count)
	}
}

func TestDetectLiveFromEmptyCapture(t *testing.T) {
	old := captureBottomKnown
	defer func() { captureBottomKnown = old }()
	captureBottomKnown = func(string, int, int) (string, error) { return "", nil }

	info := DetectLiveFrom(fromFixture(), "repo · grove-1")
	if !info.Exists || info.HasClaude || info.Status != StatusUnknown {
		t.Errorf("empty capture: got %+v, want exists-only (same as DetectLive)", info)
	}
}
