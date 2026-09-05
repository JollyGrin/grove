package supervise

import (
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/detect"
	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/tmux"
)

func seedActiveTask(t *testing.T, stateDir, ticket, repo, branch, session, window string) {
	t.Helper()
	if err := state.Append(stateDir, state.Event{
		Type: state.EvTaskCreated, Ticket: ticket,
		Data: map[string]string{
			"repo": repo, "branch": branch,
			"tmux_session": session, "tmux_window": window,
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := state.Append(stateDir, state.Event{
		Type: state.EvSessionStarted, Ticket: ticket,
		Data: map[string]string{"session_id": "s-" + ticket},
	}); err != nil {
		t.Fatal(err)
	}
}

func idleSnap(string) (*tmux.SessionSnapshot, error) {
	return tmux.ParseSessionSnapshot("@1\t1\tgr-1\n", "@1\t%1\t0\t24\tbash\n"), nil
}

func idleDetect(*tmux.SessionSnapshot, string) detect.LiveInfo {
	return detect.LiveInfo{Exists: true, HasClaude: true, Status: detect.StatusIdle, PaneContent: "❯ \n/help"}
}

func TestPoll_AppendsDeliveryEventAndFolds(t *testing.T) {
	stateDir := t.TempDir()
	seedActiveTask(t, stateDir, "gr-1", "dummy", "gr-1-branch", "grove-x", "gr-1")

	fetchCalls := 0
	deps := PollDeps{
		StateDir:   stateDir,
		RepoLookup: func(repo string) (string, bool) { return "/repos/" + repo, repo == "dummy" },
		SnapFor:    idleSnap,
		DetectFrom: idleDetect,
		FetchAll: func(lookups map[string][2]string) (map[string]*github.PR, map[string]error) {
			fetchCalls++
			if lookups["gr-1"] != [2]string{"/repos/dummy", "gr-1-branch"} {
				t.Fatalf("unexpected lookups: %+v", lookups)
			}
			return map[string]*github.PR{
				"gr-1": {Number: 9, URL: "https://x/9", State: "OPEN", CI: "pending"},
			}, map[string]error{}
		},
	}

	evs, err := Poll(deps, NewMemory(), t0())
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Type != state.EvPROpened {
		t.Fatalf("Poll events = %+v", evs)
	}
	if fetchCalls != 1 {
		t.Fatalf("FetchAll called %d times, want 1", fetchCalls)
	}

	tasks, err := state.Peek(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	got := tasks["gr-1"]
	if got.Delivery == nil || got.Delivery.State != state.DeliveryOpened {
		t.Fatalf("folded delivery = %+v, want opened", got.Delivery)
	}

	// Re-polling the identical PR state must emit nothing — Transitions
	// diffs against the now-folded state, and Poll must re-fold it (via
	// Peek) rather than trust a stale in-memory map from the first pass.
	evs2, err := Poll(deps, NewMemory(), t0().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs2) != 0 {
		t.Fatalf("re-poll of the same PR state emitted %+v, want none", evs2)
	}
}

func TestPoll_UnknownLookupSuppressesDelivery(t *testing.T) {
	stateDir := t.TempDir()
	seedActiveTask(t, stateDir, "gr-2", "dummy", "gr-2-branch", "grove-x", "gr-2")

	deps := PollDeps{
		StateDir:   stateDir,
		RepoLookup: func(repo string) (string, bool) { return "/repos/" + repo, true },
		SnapFor:    idleSnap,
		DetectFrom: idleDetect,
		FetchAll: func(lookups map[string][2]string) (map[string]*github.PR, map[string]error) {
			return nil, map[string]error{"gr-2": errLookup}
		},
	}

	evs, err := Poll(deps, NewMemory(), t0())
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 0 {
		t.Fatalf("a failed lookup must never emit a delivery transition, got %+v", evs)
	}
}

func TestPoll_NoRepoMappingSkipsLookupSafely(t *testing.T) {
	stateDir := t.TempDir()
	seedActiveTask(t, stateDir, "gr-3", "unmapped", "gr-3-branch", "grove-x", "gr-3")

	deps := PollDeps{
		StateDir:   stateDir,
		RepoLookup: func(repo string) (string, bool) { return "", false },
		SnapFor:    idleSnap,
		DetectFrom: idleDetect,
		FetchAll: func(lookups map[string][2]string) (map[string]*github.PR, map[string]error) {
			if len(lookups) != 0 {
				t.Fatalf("unmapped repo must never reach FetchAll, got %+v", lookups)
			}
			return map[string]*github.PR{}, map[string]error{}
		},
	}

	evs, err := Poll(deps, NewMemory(), t0())
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 0 {
		t.Fatalf("no-PR task must emit nothing, got %+v", evs)
	}
}

func TestPoll_SnapshotMemoizedPerSession(t *testing.T) {
	stateDir := t.TempDir()
	seedActiveTask(t, stateDir, "gr-4", "dummy", "gr-4-branch", "grove-shared", "gr-4")
	seedActiveTask(t, stateDir, "gr-5", "dummy", "gr-5-branch", "grove-shared", "gr-5")

	snapCalls := 0
	deps := PollDeps{
		StateDir:   stateDir,
		RepoLookup: func(repo string) (string, bool) { return "", false }, // liveness-only pass
		SnapFor: func(session string) (*tmux.SessionSnapshot, error) {
			snapCalls++
			return idleSnap(session)
		},
		DetectFrom: idleDetect,
		FetchAll: func(lookups map[string][2]string) (map[string]*github.PR, map[string]error) {
			return map[string]*github.PR{}, map[string]error{}
		},
	}

	if _, err := Poll(deps, NewMemory(), t0()); err != nil {
		t.Fatal(err)
	}
	if snapCalls != 1 {
		t.Fatalf("SnapFor called %d times for one shared session, want 1 (grove-149 shape)", snapCalls)
	}
}

var errLookup = &lookupErr{}

type lookupErr struct{}

func (*lookupErr) Error() string { return "gh: lookup failed" }
