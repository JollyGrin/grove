package feature

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/state"
)

func landTasks() map[string]*state.Task {
	return map[string]*state.Task{
		// merged: lands.
		"grove-1": {Ticket: "grove-1", Feature: "trains"},
		// PR open: skipped.
		"grove-2": {Ticket: "grove-2", Feature: "trains"},
		// no PR at all, but the worker says it's done: skipped "no PR".
		"grove-3": {Ticket: "grove-3", Feature: "trains", Sentinel: "done"},
		// no PR, still mid-turn: skipped "working".
		"grove-4": {Ticket: "grove-4", Feature: "trains", Agent: state.AgentWorking},
		// another feature entirely: absent from both lists.
		"grove-5": {Ticket: "grove-5", Feature: "keys"},
		// already landed (done): absent.
		"grove-6": {Ticket: "grove-6", Feature: "trains", Done: true},
	}
}

func landPR(t *state.Task) (*github.PR, error) {
	switch t.Ticket {
	case "grove-1":
		return &github.PR{Number: 101, State: "MERGED"}, nil
	case "grove-2":
		return &github.PR{Number: 102, State: "OPEN"}, nil
	default:
		return nil, nil
	}
}

func TestBuildLandPlanClassifiesEachCar(t *testing.T) {
	plan, err := BuildLandPlan(LandInput{Tasks: landTasks(), Slug: "trains", PR: landPR})
	if err != nil {
		t.Fatalf("BuildLandPlan: %v", err)
	}
	if len(plan.Land) != 1 || plan.Land[0].Ticket != "grove-1" || plan.Land[0].PR != 101 {
		t.Errorf("land = %+v, want just grove-1 (PR 101)", plan.Land)
	}
	want := map[string]string{"grove-2": SkipPROpen, "grove-3": SkipNoPR, "grove-4": SkipWorking}
	if len(plan.Skipped) != len(want) {
		t.Fatalf("skipped = %+v, want %d rows", plan.Skipped, len(want))
	}
	for _, s := range plan.Skipped {
		if want[s.Ticket] != s.Reason {
			t.Errorf("skip %s reason = %q, want %q", s.Ticket, s.Reason, want[s.Ticket])
		}
	}
	// Another feature's task, and an already-landed one, never appear.
	for _, s := range plan.Skipped {
		if s.Ticket == "grove-5" || s.Ticket == "grove-6" {
			t.Errorf("ticket %s should be absent, found in skipped", s.Ticket)
		}
	}
	for _, l := range plan.Land {
		if l.Ticket == "grove-5" || l.Ticket == "grove-6" {
			t.Errorf("ticket %s should be absent, found in land", l.Ticket)
		}
	}
}

func TestBuildLandPlanLookupErrorLeavesTicketOut(t *testing.T) {
	tasks := map[string]*state.Task{"grove-1": {Ticket: "grove-1", Feature: "trains"}}
	boom := errors.New("gh: timed out")
	plan, err := BuildLandPlan(LandInput{Tasks: tasks, Slug: "trains", PR: func(*state.Task) (*github.PR, error) {
		return nil, boom
	}})
	if err == nil || !errors.Is(err, boom) {
		t.Errorf("err = %v, want it to wrap %v", err, boom)
	}
	if len(plan.Land) != 0 || len(plan.Skipped) != 0 {
		t.Errorf("plan = %+v, want the errored ticket left out of both lists", plan)
	}
}

func TestLandContinuesPastOneFailure(t *testing.T) {
	plan := LandPlan{Land: []LandRow{
		{Ticket: "grove-1", Number: 1},
		{Ticket: "grove-2", Number: 2},
		{Ticket: "grove-3", Number: 3},
	}}
	var finished []string
	res := Land(plan, func(ticket string) error {
		finished = append(finished, ticket)
		if ticket == "grove-2" {
			return fmt.Errorf("worktree remove: dirty")
		}
		return nil
	})
	if len(finished) != 3 {
		t.Fatalf("finish called %d times, want 3 (one failure must not stop the rest)", len(finished))
	}
	if want := []int{1, 3}; !intsEqual(res.Landed, want) {
		t.Errorf("landed = %v, want %v", res.Landed, want)
	}
	if len(res.Failed) != 1 || res.Failed[0].Ticket != "grove-2" {
		t.Errorf("failed = %+v, want exactly grove-2", res.Failed)
	}
}

func TestLandEmptyPlanCallsFinisherZeroTimes(t *testing.T) {
	calls := 0
	res := Land(LandPlan{}, func(string) error { calls++; return nil })
	if calls != 0 {
		t.Errorf("finisher called %d times on an empty plan, want 0 (a --json dry run must never execute)", calls)
	}
	if len(res.Landed) != 0 || len(res.Failed) != 0 {
		t.Errorf("res = %+v, want empty", res)
	}
}

func intsEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// stubGH puts a fake `gh` on PATH that logs every invocation's argv to
// logPath, one line per call, then answers a fixed `pr list` payload —
// enough for github.PRForBranch to resolve a merged PR without a real
// network call.
func stubGH(t *testing.T, logPath string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub script is a POSIX shell script")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"echo \"$*\" >> " + logPath + "\n" +
		"case \"$*\" in\n" +
		"  *'pr list'*) echo '[{\"number\":101,\"url\":\"https://x/101\",\"state\":\"MERGED\",\"mergedAt\":\"2026-09-27T00:00:00Z\",\"isDraft\":false,\"mergeable\":\"MERGEABLE\",\"mergeStateStatus\":\"CLEAN\",\"statusCheckRollup\":[],\"comments\":[]}]' ;;\n" +
		"  *) echo '[]' ;;\n" +
		"esac\n"
	stub := filepath.Join(dir, "gh")
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub gh: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// TestLandNeverCallsAMutatingGH is the "provider fake records zero
// mutation calls" check (Decision 8): BuildLandPlan's own PR lookup, run
// against a real `github.PRForBranch` pointed at a stub `gh`, and a fake
// Finisher standing in for finishTask, together must never shell out to
// anything but a read (`pr list`) — never `issue close`, `issue comment`,
// or `pr merge`. Grove reads; humans and the orchestrator finish the job.
func TestLandNeverCallsAMutatingGH(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "gh.log")
	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	stubGH(t, logPath)

	repoDir := t.TempDir()
	tasks := map[string]*state.Task{
		"grove-1": {Ticket: "grove-1", Feature: "trains", Branch: "grove-1-car"},
	}
	plan, err := BuildLandPlan(LandInput{Tasks: tasks, Slug: "trains", PR: func(t *state.Task) (*github.PR, error) {
		return github.PRForBranch(repoDir, t.Branch)
	}})
	if err != nil {
		t.Fatalf("BuildLandPlan: %v", err)
	}
	if len(plan.Land) != 1 {
		t.Fatalf("plan.Land = %+v, want the stub's merged PR to land", plan.Land)
	}

	var finished []string
	res := Land(plan, func(ticket string) error {
		finished = append(finished, ticket)
		// A real finishTask never touches gh for anything but the same
		// read-only merge check BuildLandPlan already did — nothing to
		// shell out to here.
		return nil
	})
	if len(res.Landed) != 1 || len(finished) != 1 {
		t.Fatalf("res = %+v, finished = %v, want exactly one finish call", res, finished)
	}

	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(log)), "\n") {
		if line == "" {
			continue
		}
		for _, mutating := range []string{"issue close", "issue comment", "issue edit", "pr merge", "pr close", "pr comment"} {
			if strings.HasPrefix(line, mutating) {
				t.Errorf("gh invoked with a mutating verb: %q", line)
			}
		}
	}
}
