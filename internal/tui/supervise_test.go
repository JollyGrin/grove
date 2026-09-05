package tui

// grove-254: the cockpit as supervisor. The engine is shared with `gv
// supervise` (internal/supervise.Transitions); these pin the cockpit's
// driver around it — the refreshMsg/prsMsg beats emit exactly what the
// engine would for the same observations, the lock arbitrates in both
// directions, and the frame does no new work.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/JollyGrin/grove/internal/detect"
	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/supervise"
)

// seedWorker writes a task_created + working status into dir's log.
func seedWorker(t *testing.T, dir, ticket string) {
	t.Helper()
	for _, ev := range []state.Event{
		{Type: state.EvTaskCreated, Ticket: ticket, Data: map[string]string{
			"title": "supervised", "repo": "grove", "branch": ticket + "-x",
			"tmux_session": "grove-test", "tmux_window": "grove · " + ticket,
		}},
		{Type: state.EvAgentStatus, Ticket: ticket, Data: map[string]string{"status": "working"}},
	} {
		if err := state.Append(dir, ev); err != nil {
			t.Fatal(err)
		}
	}
}

// eventTypes reads the log's event types, oldest first, filtered to the
// delivery/liveness dimensions.
func eventTypes(t *testing.T, dir string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var ev state.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(ev.Type, "pr_") || strings.HasPrefix(ev.Type, "worker_") {
			out = append(out, ev.Type)
		}
	}
	return out
}

// folded is the beat's view of the log — what refreshCmd hands Update.
func folded(t *testing.T, dir string) []*state.Task {
	t.Helper()
	tasks, err := state.Peek(dir)
	if err != nil {
		t.Fatal(err)
	}
	return state.Active(tasks)
}

func supervisingModel(t *testing.T, dir string) Model {
	t.Helper()
	pinHour(t, 10)
	m := New(nil, dir, "")
	m.width, m.height = 120, 40
	m.acquireSupervise()
	if m.sup == nil {
		t.Fatalf("cockpit did not take the supervise lock: note=%q", m.supNote)
	}
	t.Cleanup(m.releaseSupervise)
	return m
}

func update(t *testing.T, m Model, msg any) Model {
	t.Helper()
	out, _ := m.Update(msg)
	return out.(Model)
}

// A refreshMsg + prsMsg sequence on a fake model emits into the scratch
// state dir exactly what the shared engine emits for the same
// observations — and never twice.
func TestCockpitSupervisesOnItsBeat(t *testing.T) {
	dir := t.TempDir()
	seedWorker(t, dir, "grove-254")
	m := supervisingModel(t, dir)
	now := time.Now()

	// Beat 1: a healthy pane, no PR yet — nothing.
	ok := map[string]detect.LiveInfo{"grove-254": {Exists: true, HasClaude: true, Status: detect.StatusBusy, PaneContent: "❯ working"}}
	m = update(t, m, refreshMsg{tasks: folded(t, dir), live: map[string]string{"grove-254": "busy"}, infos: ok, ok: true})
	if got := eventTypes(t, dir); len(got) != 0 {
		t.Fatalf("healthy beat emitted %v", got)
	}

	// PR poll: opened → pr_opened once; the same poll again → nothing.
	open := map[string]*github.PR{"grove-254": {Number: 9, URL: "https://x/9", State: "OPEN", CI: "pending"}}
	m = update(t, m, prsMsg{prs: open, unknown: map[string]bool{}})
	m = update(t, m, prsMsg{prs: open, unknown: map[string]bool{}})
	// A stale refresh (folded before the append) must not re-emit either.
	m = update(t, m, refreshMsg{tasks: folded(t, dir), infos: ok, ok: true})
	if got := eventTypes(t, dir); strings.Join(got, ",") != "pr_opened" {
		t.Fatalf("after opened: %v, want [pr_opened]", got)
	}

	// gh outage: the PR is missing AND flagged unknown — no transition.
	m = update(t, m, prsMsg{prs: map[string]*github.PR{}, unknown: map[string]bool{"grove-254": true}})
	if got := eventTypes(t, dir); strings.Join(got, ",") != "pr_opened" {
		t.Fatalf("gh outage emitted: %v", got)
	}

	// Green + CLEAN → pr_ready, with the operator flash.
	ready := map[string]*github.PR{"grove-254": {Number: 9, URL: "https://x/9", State: "OPEN", CI: "pass", MergeState: "CLEAN", Checks: 4}}
	m = update(t, m, prsMsg{prs: ready, unknown: map[string]bool{}})
	if !strings.Contains(m.flash, "grove-254 ready — 4 checks green") {
		t.Errorf("flash = %q", m.flash)
	}

	// Liveness on the refresh beat: an error marker → worker_errored once.
	errored := map[string]detect.LiveInfo{"grove-254": {Exists: true, HasClaude: true, PaneContent: "API Error: Request rejected (429)"}}
	m = update(t, m, refreshMsg{tasks: folded(t, dir), infos: errored, ok: true})
	m = update(t, m, refreshMsg{tasks: folded(t, dir), infos: errored, ok: true})
	if !strings.Contains(m.flash, "grove-254 errored — usage_limit") {
		t.Errorf("flash = %q", m.flash)
	}
	// Merged → pr_merged.
	merged := map[string]*github.PR{"grove-254": {Number: 9, State: "MERGED", CI: "pass"}}
	m = update(t, m, prsMsg{prs: merged, unknown: map[string]bool{}})

	got := eventTypes(t, dir)
	want := "pr_opened,pr_ready,worker_errored,pr_merged"
	if strings.Join(got, ",") != want {
		t.Fatalf("cockpit emitted %v, want %s", got, want)
	}

	// Shared engine: the same observation sequence through a fresh
	// supervise.Memory, folded the same way, yields the same types.
	ref := t.TempDir()
	seedWorker(t, ref, "grove-254")
	mem := supervise.NewMemory()
	step := func(pr *github.PR, known bool, live detect.LiveInfo) {
		for _, tk := range folded(t, ref) {
			for _, ev := range supervise.Transitions(supervise.Observation{Task: tk, PR: pr, PRKnown: known, Live: live, Now: now}, mem) {
				if err := state.Append(ref, ev); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	step(nil, true, ok["grove-254"])
	step(open["grove-254"], true, detect.LiveInfo{})
	step(open["grove-254"], true, detect.LiveInfo{})
	step(nil, false, detect.LiveInfo{})
	step(ready["grove-254"], true, detect.LiveInfo{})
	step(nil, true, errored["grove-254"])
	step(nil, true, errored["grove-254"])
	step(merged["grove-254"], true, detect.LiveInfo{})
	if refGot := eventTypes(t, ref); strings.Join(refGot, ",") != strings.Join(got, ",") {
		t.Fatalf("engine fixture emitted %v, cockpit emitted %v", refGot, got)
	}
}

// A lock held by another holder (a headless gv supervise): zero appends,
// and the header names the pid.
func TestCockpitDefersToHeadlessSupervisor(t *testing.T) {
	dir := t.TempDir()
	seedWorker(t, dir, "grove-254")
	unlock, err := supervise.Lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	pinHour(t, 10)
	m := New(nil, dir, "")
	m.width, m.height = 120, 40
	m.acquireSupervise()
	if m.sup != nil {
		t.Fatal("cockpit took a lock another process holds")
	}
	want := "⟳ supervised by pid " + strconv.Itoa(os.Getpid())
	if !strings.Contains(m.supNote, want) {
		t.Errorf("supNote = %q, want it to contain %q", m.supNote, want)
	}
	if hdr := m.viewHeader(); !strings.Contains(hdr, want) {
		t.Errorf("header = %q, want the supervised-by note", hdr)
	}

	open := map[string]*github.PR{"grove-254": {Number: 9, URL: "https://x/9", State: "OPEN", CI: "pending"}}
	errored := map[string]detect.LiveInfo{"grove-254": {Exists: true, HasClaude: true, PaneContent: "API Error: boom"}}
	m = update(t, m, refreshMsg{tasks: folded(t, dir), infos: errored, ok: true})
	m = update(t, m, prsMsg{prs: open, unknown: map[string]bool{}})
	if got := eventTypes(t, dir); len(got) != 0 {
		t.Fatalf("a non-holder cockpit appended %v", got)
	}

	// Once the holder is gone the lock is free for the cockpit.
	unlock()
	m.acquireSupervise()
	if m.sup == nil {
		t.Fatal("cockpit could not take the released lock")
	}
	m.releaseSupervise()
	// …and releasing it frees it for a headless gv supervise again.
	unlock2, err := supervise.Lock(dir)
	if err != nil {
		t.Fatalf("lock not released on cockpit quit: %v", err)
	}
	unlock2()
}

// A narrow header sheds the supervise note before the counts, and never
// exceeds the pane width with it.
func TestViewHeaderShedsSuperviseNote(t *testing.T) {
	m := New(nil, "", "a-long-workspace-label")
	m.fx = fxOff
	m.supNote = "⟳ supervised by pid 4242 · "
	for _, w := range []int{30, 40, 60, 200} {
		m.width = w
		out := m.viewHeader()
		if lw := lipgloss.Width(out); lw > w {
			t.Errorf("width %d: header is %d cells", w, lw)
		}
	}
}

// The frame allocates the same with the supervisor armed (the memory is
// consulted only in Update) and with the note rendered (pre-built string,
// one concat expression either way) as it does bare — no new per-frame
// work (the cockpit RAM rule, stated in the PR body).
func TestViewAllocsFlatUnderSupervision(t *testing.T) {
	base := fixtureModel(t)
	base.events = []state.Event{{Type: state.EvTaskCreated, Ticket: "grove-18", Time: time.Now()}}
	measure := func(m Model) float64 {
		return testing.AllocsPerRun(50, func() { _ = m.View() })
	}
	bare := measure(base)

	armed := base
	armed.sup = supervise.NewMemory()
	if got := measure(armed); got != bare {
		t.Errorf("View allocs with the supervisor armed = %v, bare = %v", got, bare)
	}
	// The note changes the header's gap width (one Repeat either way), so
	// the count may move by the layout's own ±1 — it must never grow.
	noted := base
	noted.supNote = "⟳ supervised by pid 4242 · "
	if got := measure(noted); got > bare+1 {
		t.Errorf("View allocs with the supervised-by note = %v, bare = %v", got, bare)
	}
}
