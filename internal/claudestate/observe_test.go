package claudestate

import (
	"testing"

	"github.com/JollyGrin/grove/internal/detect"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/tmux"
)

// One read for the whole beat; capture only for the worker with no
// matching session file.
func TestObserveCapturesOnlyWithoutSessionFile(t *testing.T) {
	snap := tmux.ParseSessionSnapshot("@1\t1\ta\n@2\t0\tb\n@3\t0\tc\n",
		"@1\t%1\t0\t24\tclaude\n@2\t%2\t0\t24\tclaude\n@3\t%3\t0\t24\tclaude\n")
	mk := func(tk, win, sid string) *state.Task {
		return &state.Task{Ticket: tk, TmuxSession: "s", TmuxWindow: win, SessionID: sid}
	}
	tasks := []*state.Task{mk("A", "a", "sa"), mk("B", "b", "sb"), mk("C", "c", "sc")}
	reads, captures := 0, 0
	infos, sigs := Observe(tasks,
		func(string) *tmux.SessionSnapshot { return snap },
		func(*tmux.SessionSnapshot, string) detect.LiveInfo {
			captures++
			return detect.LiveInfo{Exists: true, HasClaude: true, Status: detect.StatusIdle}
		},
		func(want map[string]bool) map[string]state.ClaudeStatus {
			reads++
			return map[string]state.ClaudeStatus{
				"%1": {Status: "busy", SessionID: "sa"},
				"%2": {Status: "busy", SessionID: "other"}, // stale file from another session
			}
		})
	if reads != 1 || captures != 2 {
		t.Fatalf("reads=%d captures=%d, want 1 and 2 (B's file is another session's, C has none)", reads, captures)
	}
	if sigs["A"].Claude == nil || infos["A"].Status != detect.StatusBusy {
		t.Fatalf("A = %+v %+v", sigs["A"], infos["A"])
	}
	if sigs["B"].Claude != nil || sigs["C"].Claude != nil {
		t.Fatal("B/C must not carry a session-file signal")
	}
}
