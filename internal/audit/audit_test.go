package audit

import (
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/state"
)

func TestClassify(t *testing.T) {
	week := 7 * 24 * time.Hour
	cases := []struct {
		name string
		f    Facts
		want Class
	}{
		{"working with live window", Facts{WorktreeExists: true, WindowAlive: true, PRKnown: true, PRState: "OPEN", Agent: state.AgentWorking}, Healthy},
		{"merged beats disconnected", Facts{WorktreeExists: true, WindowAlive: false, PRKnown: true, PRState: "MERGED", Agent: state.AgentIdle}, Merged},
		{"merged with live window", Facts{WorktreeExists: true, WindowAlive: true, PRKnown: true, PRState: "MERGED", Agent: state.AgentIdle}, Merged},
		{"window gone", Facts{WorktreeExists: true, WindowAlive: false, PRKnown: true, PRState: "OPEN", Agent: state.AgentDead}, Disconnected},
		{"pr error + window gone is disconnected, never abandoned", Facts{WorktreeExists: true, WindowAlive: false, PRKnown: false, Agent: state.AgentDead, Age: 30 * 24 * time.Hour}, Disconnected},
		{"closed pr is abandoned", Facts{WorktreeExists: true, WindowAlive: false, PRKnown: true, PRState: "CLOSED", Agent: state.AgentDead}, Abandoned},
		{"closed pr with live window still abandoned", Facts{WorktreeExists: true, WindowAlive: true, PRKnown: true, PRState: "CLOSED", Agent: state.AgentIdle}, Abandoned},
		{"no pr + dead + stale is abandoned", Facts{WorktreeExists: true, WindowAlive: false, PRKnown: true, PRState: "", Agent: state.AgentDead, Age: 10 * 24 * time.Hour}, Abandoned},
		{"no pr + dead + fresh is disconnected", Facts{WorktreeExists: true, WindowAlive: false, PRKnown: true, PRState: "", Agent: state.AgentDead, Age: 2 * 24 * time.Hour}, Disconnected},
		{"parked + no pr + idle + stale is NOT abandoned, just disconnected", Facts{WorktreeExists: true, WindowAlive: false, PRKnown: true, PRState: "", Agent: state.AgentIdle, Age: 30 * 24 * time.Hour, Parked: true}, Disconnected},
		{"parked with a closed pr is still abandoned (parking doesn't rescue a closed PR)", Facts{WorktreeExists: true, WindowAlive: false, PRKnown: true, PRState: "CLOSED", Agent: state.AgentIdle, Parked: true}, Abandoned},
		{"no pr + working + stale is healthy (agent alive)", Facts{WorktreeExists: true, WindowAlive: true, PRKnown: true, PRState: "", Agent: state.AgentWorking, Age: 10 * 24 * time.Hour}, Healthy},
		{"missing worktree is drifted regardless of window", Facts{WorktreeExists: false, WindowAlive: true, PRKnown: true, PRState: "OPEN", Agent: state.AgentWorking}, Drifted},
		{"missing worktree with merged pr is still merged (done now handles it)", Facts{WorktreeExists: false, WindowAlive: false, PRKnown: true, PRState: "MERGED", Agent: state.AgentDead}, Merged},
		// gv pause (grove-90): a paused task with a missing window is Paused —
		// never Disconnected, never Abandoned, however stale or dead it looks.
		{"paused + window gone is paused, not disconnected", Facts{WorktreeExists: true, WindowAlive: false, PRKnown: true, PRState: "OPEN", Agent: state.AgentIdle, Paused: true}, Paused},
		{"paused + no pr + dead + stale is paused, not abandoned", Facts{WorktreeExists: true, WindowAlive: false, PRKnown: true, PRState: "", Agent: state.AgentDead, Age: 30 * 24 * time.Hour, Paused: true}, Paused},
		{"paused + closed pr is paused (the bookmark outranks the closed PR)", Facts{WorktreeExists: true, WindowAlive: false, PRKnown: true, PRState: "CLOSED", Agent: state.AgentIdle, Paused: true}, Paused},
		{"paused + merged pr is merged (ship beats bookmark)", Facts{WorktreeExists: true, WindowAlive: false, PRKnown: true, PRState: "MERGED", Agent: state.AgentIdle, Paused: true}, Merged},
		{"paused + missing worktree is drifted (adopt re-creates it)", Facts{WorktreeExists: false, WindowAlive: false, PRKnown: true, PRState: "", Agent: state.AgentIdle, Paused: true}, Drifted},
	}
	for _, c := range cases {
		if got := Classify(c.f, week); got != c.want {
			t.Errorf("%s: Classify = %s, want %s", c.name, got, c.want)
		}
	}
}

func TestSuggestion(t *testing.T) {
	cases := map[Class]string{
		Healthy:      "",
		Merged:       "gv done",
		Paused:       "gv adopt",
		Disconnected: "gv adopt",
		Abandoned:    "gv untrack --rm",
		Drifted:      "gv adopt (or gv untrack)",
	}
	for class, want := range cases {
		if got := Suggestion(class); got != want {
			t.Errorf("Suggestion(%s) = %q, want %q", class, got, want)
		}
	}
}
