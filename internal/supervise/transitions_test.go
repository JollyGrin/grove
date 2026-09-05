package supervise

import (
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/detect"
	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/state"
)

func t0() time.Time { return time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC) }

func newTask(ticket string) *state.Task {
	return &state.Task{Ticket: ticket, Agent: state.AgentWorking}
}

// applyAll folds every returned event onto task, mimicking what the real
// events.jsonl + fold loop would do for the NEXT observation in a sequence.
func applyDelivery(task *state.Task, evs []state.Event) {
	for _, ev := range evs {
		d := ev.Data
		switch ev.Type {
		case state.EvPROpened:
			task.Delivery = &state.Delivery{State: state.DeliveryOpened, URL: d["url"], At: ev.Time}
		case state.EvPRUpdated:
			task.Delivery = &state.Delivery{State: state.DeliveryOpened, At: ev.Time}
		case state.EvPRCIFailed:
			task.Delivery = &state.Delivery{State: state.DeliveryCIFailed, At: ev.Time}
		case state.EvPRConflicting:
			task.Delivery = &state.Delivery{State: state.DeliveryConflicting, At: ev.Time}
		case state.EvPRReady:
			task.Delivery = &state.Delivery{State: state.DeliveryReady, At: ev.Time}
		case state.EvPRMerged:
			task.Delivery = &state.Delivery{State: state.DeliveryMerged, At: ev.Time}
		case state.EvPRClosed:
			task.Delivery = &state.Delivery{State: state.DeliveryClosed, At: ev.Time}
		}
	}
}

func TestDeliveryTransitions_FullLifecycle(t *testing.T) {
	task := newTask("gr-1")

	step := func(pr *github.PR) []state.Event {
		evs := Transitions(Observation{Task: task, PR: pr, PRKnown: true, Now: t0()}, NewMemory())
		applyDelivery(task, evs)
		return evs
	}

	// none -> opened
	evs := step(&github.PR{Number: 10, URL: "https://x/10", State: "OPEN", CI: "pending"})
	if len(evs) != 1 || evs[0].Type != state.EvPROpened {
		t.Fatalf("none->opened: got %+v", evs)
	}
	if evs[0].Data["url"] != "https://x/10" || evs[0].Data["draft"] != "false" {
		t.Errorf("pr_opened data = %+v", evs[0].Data)
	}

	// re-observing the same state emits nothing
	if evs := Transitions(Observation{Task: task, PR: &github.PR{Number: 10, State: "OPEN", CI: "pending"}, PRKnown: true, Now: t0()}, NewMemory()); len(evs) != 0 {
		t.Fatalf("re-observing opened emitted %+v", evs)
	}

	// opened -> ci_failed
	evs = step(&github.PR{Number: 10, State: "OPEN", CI: "fail", Failing: []string{"build", "lint"}})
	if len(evs) != 1 || evs[0].Type != state.EvPRCIFailed || evs[0].Data["failing"] != "build,lint" {
		t.Fatalf("opened->ci_failed: got %+v", evs)
	}

	// ci_failed -> opened (fresh push, checks pending again) => pr_updated
	evs = step(&github.PR{Number: 10, State: "OPEN", CI: "pending"})
	if len(evs) != 1 || evs[0].Type != state.EvPRUpdated {
		t.Fatalf("ci_failed->opened: got %+v, want pr_updated", evs)
	}

	// opened -> ready
	evs = step(&github.PR{Number: 10, State: "OPEN", CI: "pass", MergeState: "CLEAN"})
	if len(evs) != 1 || evs[0].Type != state.EvPRReady {
		t.Fatalf("opened->ready: got %+v", evs)
	}
	if _, has := evs[0].Data["behind"]; has {
		t.Errorf("CLEAN ready must not carry behind: %+v", evs[0].Data)
	}

	// ready -> merged
	evs = step(&github.PR{Number: 10, State: "MERGED"})
	if len(evs) != 1 || evs[0].Type != state.EvPRMerged {
		t.Fatalf("ready->merged: got %+v", evs)
	}

	// re-observing merged emits nothing
	if evs := step(&github.PR{Number: 10, State: "MERGED"}); len(evs) != 0 {
		t.Fatalf("re-observing merged emitted %+v", evs)
	}
}

func TestDeliveryTransitions_PRUnknownEmitsNothing(t *testing.T) {
	task := newTask("gr-2")
	task.Delivery = &state.Delivery{State: state.DeliveryOpened, PR: 5}
	before := *task.Delivery

	evs := Transitions(Observation{Task: task, PR: nil, PRKnown: false, Now: t0()}, NewMemory())
	if len(evs) != 0 {
		t.Fatalf("PRKnown=false emitted %+v", evs)
	}
	if task.Delivery.State != before.State || task.Delivery.PR != before.PR {
		t.Errorf("PRKnown=false must not touch Delivery: got %+v", task.Delivery)
	}
}

func TestDeliveryTransitions_ClosedThenReopen(t *testing.T) {
	task := newTask("gr-3")
	task.Delivery = &state.Delivery{State: state.DeliveryClosed, PR: 7}

	evs := Transitions(Observation{Task: task, PR: &github.PR{Number: 7, URL: "https://x/7", State: "OPEN", CI: "pending"}, PRKnown: true, Now: t0()}, NewMemory())
	if len(evs) != 1 || evs[0].Type != state.EvPROpened {
		t.Fatalf("closed->opened (reopen): got %+v, want pr_opened", evs)
	}
}

func TestDeliveryTransitions_DraftNeverReady(t *testing.T) {
	task := newTask("gr-4")
	evs := Transitions(Observation{Task: task, PR: &github.PR{Number: 1, State: "OPEN", CI: "pass", Draft: true}, PRKnown: true, Now: t0()}, NewMemory())
	if len(evs) != 1 || evs[0].Type != state.EvPROpened {
		t.Fatalf("draft+green CI: got %+v, want pr_opened (never ready)", evs)
	}
}

func TestDeliveryTransitions_BehindIsReady(t *testing.T) {
	task := newTask("gr-5")
	evs := Transitions(Observation{Task: task, PR: &github.PR{Number: 1, State: "OPEN", CI: "pass", MergeState: "BEHIND"}, PRKnown: true, Now: t0()}, NewMemory())
	if len(evs) != 1 || evs[0].Type != state.EvPRReady {
		t.Fatalf("BEHIND: got %+v, want pr_ready", evs)
	}
	if evs[0].Data["behind"] != "true" {
		t.Errorf("pr_ready data missing behind=true: %+v", evs[0].Data)
	}
}

func TestDeliveryTransitions_DirtyIsConflictingEvenWithGreenCI(t *testing.T) {
	task := newTask("gr-6")
	evs := Transitions(Observation{Task: task, PR: &github.PR{Number: 1, State: "OPEN", CI: "pass", MergeState: "DIRTY"}, PRKnown: true, Now: t0()}, NewMemory())
	if len(evs) != 1 || evs[0].Type != state.EvPRConflicting {
		t.Fatalf("DIRTY+green CI: got %+v, want pr_conflicting", evs)
	}
}

// --- Liveness ---

func TestLivenessTransitions_WaitingRequires10sContinuous(t *testing.T) {
	task := newTask("gr-10")
	mem := NewMemory()
	live := detect.LiveInfo{Exists: true, HasClaude: true, Status: detect.StatusWaiting, PaneContent: "do you want to proceed?"}

	// First sighting at t0 — not yet 10s in.
	evs := Transitions(Observation{Task: task, PRKnown: true, Live: live, Now: t0()}, mem)
	if len(evs) != 0 {
		t.Fatalf("first sighting fired early: %+v", evs)
	}

	// A 5s blip: still waiting 5s later — under the threshold, nothing yet.
	evs = Transitions(Observation{Task: task, PRKnown: true, Live: live, Now: t0().Add(5 * time.Second)}, mem)
	if len(evs) != 0 {
		t.Fatalf("5s blip fired: %+v", evs)
	}

	// 11s later: continuous waiting past the hysteresis window.
	evs = Transitions(Observation{Task: task, PRKnown: true, Live: live, Now: t0().Add(11 * time.Second)}, mem)
	if len(evs) != 1 || evs[0].Type != state.EvWorkerWaiting {
		t.Fatalf("11s continuous: got %+v, want worker_waiting", evs)
	}
	if evs[0].Data["marker"] != "do_you_want" {
		t.Errorf("marker = %q, want do_you_want", evs[0].Data["marker"])
	}

	// Re-observing the same waiting state emits nothing.
	task.Liveness = &state.Liveness{State: state.LivenessWaiting}
	evs = Transitions(Observation{Task: task, PRKnown: true, Live: live, Now: t0().Add(12 * time.Second)}, mem)
	if len(evs) != 0 {
		t.Fatalf("re-observing waiting emitted %+v", evs)
	}
}

func TestLivenessTransitions_VanishedRequiresHysteresisAndBootGrace(t *testing.T) {
	task := newTask("gr-11")
	task.LiveSince = t0()
	mem := NewMemory()
	gone := detect.LiveInfo{Exists: true, HasClaude: false, Status: detect.StatusUnknown}

	// First sighting of "gone" at t0+5s: within boot grace (120s) — must
	// not fire regardless of how long it's been continuously gone.
	evs := Transitions(Observation{Task: task, PRKnown: true, Live: gone, Now: t0().Add(5 * time.Second)}, mem)
	if len(evs) != 0 {
		t.Fatalf("inside boot grace fired: %+v", evs)
	}

	// t0+70s: continuously gone for 65s (past the 60s hysteresis), but
	// still inside the 120s boot grace — must not fire.
	evs = Transitions(Observation{Task: task, PRKnown: true, Live: gone, Now: t0().Add(70 * time.Second)}, mem)
	if len(evs) != 0 {
		t.Fatalf("60s continuous but inside boot grace fired: %+v", evs)
	}

	// t0+125s: continuously gone for 120s AND past the 120s boot grace —
	// both conditions now hold.
	evs = Transitions(Observation{Task: task, PRKnown: true, Live: gone, Now: t0().Add(125 * time.Second)}, mem)
	if len(evs) != 1 || evs[0].Type != state.EvWorkerVanished {
		t.Fatalf("continuous past grace: got %+v, want worker_vanished", evs)
	}
}

func TestLivenessTransitions_ErroredIsImmediate(t *testing.T) {
	task := newTask("gr-12")
	live := detect.LiveInfo{Exists: true, HasClaude: true, Status: detect.StatusIdle,
		PaneContent: "some earlier line\nAPI Error: Request rejected (429) usage limit reached\nmore"}
	evs := Transitions(Observation{Task: task, PRKnown: true, Live: live, Now: t0()}, NewMemory())
	if len(evs) != 1 || evs[0].Type != state.EvWorkerErrored {
		t.Fatalf("errored marker: got %+v", evs)
	}
	if evs[0].Data["reason"] != "usage_limit" {
		t.Errorf("reason = %q, want usage_limit", evs[0].Data["reason"])
	}
	if evs[0].Data["line"] == "" {
		t.Error("errored event must carry the matched line")
	}
}

func TestLivenessTransitions_RecoveryEmitsOnce(t *testing.T) {
	task := newTask("gr-13")
	task.Liveness = &state.Liveness{State: state.LivenessErrored}
	live := detect.LiveInfo{Exists: true, HasClaude: true, Status: detect.StatusBusy, PaneContent: "✽ thinking"}

	evs := Transitions(Observation{Task: task, PRKnown: true, Live: live, Now: t0()}, NewMemory())
	if len(evs) != 1 || evs[0].Type != state.EvWorkerRecovered || evs[0].Data["from"] != state.LivenessErrored {
		t.Fatalf("recovery: got %+v", evs)
	}

	// Fold the recovery and re-observe: must not fire again.
	task.Liveness = &state.Liveness{State: state.LivenessOK}
	evs = Transitions(Observation{Task: task, PRKnown: true, Live: live, Now: t0().Add(time.Second)}, NewMemory())
	if len(evs) != 0 {
		t.Fatalf("re-observing ok emitted %+v", evs)
	}
}

func TestLivenessTransitions_OutOfScopeTasksEmitNothing(t *testing.T) {
	live := detect.LiveInfo{Exists: true, HasClaude: false, Status: detect.StatusUnknown}
	now := t0().Add(10 * time.Minute)

	cases := []struct {
		name string
		mut  func(*state.Task)
	}{
		{"done", func(tk *state.Task) { tk.Done = true }},
		{"paused", func(tk *state.Task) { tk.Paused = true }},
		{"handed_off", func(tk *state.Task) { tk.HandedOffTo = "remote-host" }},
		{"setup", func(tk *state.Task) { tk.Agent = state.AgentSetup }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			task := newTask("gr-scope-" + c.name)
			c.mut(task)
			evs := Transitions(Observation{Task: task, PRKnown: true, Live: live, Now: now}, NewMemory())
			if len(evs) != 0 {
				t.Fatalf("%s task emitted liveness events: %+v", c.name, evs)
			}
		})
	}
}
