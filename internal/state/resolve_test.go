package state

import (
	"strings"
	"testing"
	"time"
)

func TestResolve(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	live := Signals{WindowExists: true, PaneCommand: "claude"}
	withClaude := func(status, waitingFor string) Signals {
		s := live
		s.Claude = &ClaudeStatus{Status: status, WaitingFor: waitingFor, SessionID: "sid"}
		return s
	}
	cases := []struct {
		name  string
		task  Task
		sig   Signals
		state string
		det   string
		src   string
		label string
	}{
		{"done wins over a dead window", Task{Done: true}, Signals{}, StateDone, "", SourceHook, "done"},
		{"paused wins over a dead window", Task{Paused: true}, Signals{}, StatePaused, "", SourceHook, "paused"},
		{"window gone", Task{Agent: AgentWorking}, Signals{}, StateDead, "window gone", SourcePane, "dead"},
		{"pane not claude after session_started", Task{Agent: AgentWorking, SessionStarted: true},
			Signals{WindowExists: true, PaneCommand: "zsh"}, StateDead, "claude exited", SourcePane, "dead"},
		{"pane is a shell but claude not started yet", Task{Agent: AgentSetup, LiveSince: ago(time.Minute)},
			Signals{WindowExists: true, PaneCommand: "zsh"}, StateSetup, "", SourceHook, "setup"},
		{"version-string pane command is claude", Task{Agent: AgentWorking, SessionStarted: true},
			Signals{WindowExists: true, PaneCommand: "2.1.303"}, StateWorking, "", SourceHook, "working"},
		{"setup + setup_failed", Task{Agent: AgentSetup, SetupFailed: true, SetupError: "npm ci"},
			live, StateSetupFailed, "npm ci", SourceHook, "setup failed (npm ci)"},
		{"setup older than timeout, no claude", Task{Agent: AgentSetup, LiveSince: ago(11 * time.Minute)},
			Signals{WindowExists: true, PaneCommand: "zsh"}, StateSetupFailed, "stuck?", SourceTimeout, "setup failed (stuck?)"},
		{"setup older than timeout but claude is up", Task{Agent: AgentSetup, LiveSince: ago(11 * time.Minute)},
			live, StateSetup, "", SourceHook, "setup"},
		{"setup within timeout", Task{Agent: AgentSetup, LiveSince: ago(9 * time.Minute)},
			Signals{WindowExists: true, PaneCommand: "zsh"}, StateSetup, "", SourceHook, "setup"},
		{"stop_failed newer than prompt", Task{Agent: AgentWorking, StopFailedAt: ago(time.Minute), PromptAt: ago(2 * time.Minute), StopError: "rate_limit"},
			live, StateErrored, "rate_limit", SourceHook, "errored (rate_limit)"},
		{"stop_failed older than prompt", Task{Agent: AgentWorking, StopFailedAt: ago(2 * time.Minute), PromptAt: ago(time.Minute), StopError: "rate_limit"},
			live, StateWorking, "", SourceHook, "working"},
		{"session file busy", Task{Agent: AgentIdle, SessionID: "sid"}, withClaude("busy", ""), StateWorking, "", SourceClaude, "working"},
		{"session file for another session is ignored", Task{Agent: AgentIdle, SessionID: "other"}, withClaude("busy", ""),
			StateIdle, "stalled?", SourceHook, "stalled?"},
		{"session file waiting", Task{Agent: AgentWorking, SessionID: "sid"}, withClaude("waiting", "permission prompt"),
			StateWaiting, "permission prompt", SourceClaude, "waiting (permission prompt)"},
		{"session idle after hook working = interrupted", Task{Agent: AgentWorking, SessionID: "sid"}, withClaude("idle", ""),
			StateIdle, detailInterrupted, SourceClaude, "idle (interrupted)"},
		{"session idle + done sentinel", Task{Agent: AgentIdle, Sentinel: "done", SessionID: "sid"}, withClaude("idle", ""),
			StateIdle, detailDone, SourceHook, "idle ✓"},
		{"session idle + done_unverified", Task{Agent: AgentIdle, Sentinel: "done_unverified", SessionID: "sid"}, withClaude("idle", ""),
			StateIdle, detailDoneUnverif, SourceHook, "done?"},
		{"session idle + question keeps the question", Task{Agent: AgentWaiting, SessionID: "sid"}, withClaude("idle", ""),
			StateWaiting, detailQuestion, SourceHook, "QUESTION"},
		{"nil ClaudeStatus = hook fold", Task{Agent: AgentWorking, SessionID: "sid"}, live, StateWorking, "", SourceHook, "working"},
		{"answered, no prompt_submitted after grace, file idle", Task{Agent: AgentWorking, SessionID: "sid", AnsweredAt: ago(20 * time.Second)},
			withClaude("idle", ""), StateIdle, detailNotDeliver, SourceClaude, "idle (answer not delivered?)"},
		{"answered, within grace", Task{Agent: AgentWorking, SessionID: "sid", AnsweredAt: ago(5 * time.Second)},
			withClaude("idle", ""), StateIdle, detailInterrupted, SourceClaude, "idle (interrupted)"},
		{"answered and prompt_submitted since", Task{Agent: AgentWorking, SessionID: "sid", AnsweredAt: ago(20 * time.Second), PromptAt: ago(19 * time.Second)},
			withClaude("idle", ""), StateIdle, detailInterrupted, SourceClaude, "idle (interrupted)"},
		{"hook fold: notification wait", Task{Agent: AgentWorking, WaitingSince: ago(time.Second)}, live,
			StateWaiting, "permission prompt", SourceHook, "waiting (permission prompt)"},
		{"hook fold: blocked", Task{Agent: AgentBlocked}, live, StateBlocked, "", SourceHook, "BLOCKED"},
		{"hook fold: session ended", Task{Agent: AgentDead}, live, StateDead, "", SourceHook, "dead"},
		{"pane fallback error", Task{Agent: AgentWorking}, Signals{WindowExists: true, PaneCommand: "claude", PaneError: "rate_limit"},
			StateErrored, "rate_limit", SourcePane, "errored (rate_limit)"},
	}
	for _, c := range cases {
		task := c.task
		got := Resolve(&task, c.sig, now)
		if got.State != c.state || got.Detail != c.det || got.Source != c.src {
			t.Errorf("%s: got %+v, want {%s %q %s}", c.name, got, c.state, c.det, c.src)
		}
		if l := got.Label(); l != c.label {
			t.Errorf("%s: label %q, want %q", c.name, l, c.label)
		}
	}
}

func TestLabelEmptySignalsMatchesLegacy(t *testing.T) {
	cases := []struct {
		t    Task
		want string
	}{
		{Task{Agent: AgentSetup}, "setup"},
		{Task{Agent: AgentWorking}, "working"},
		{Task{Agent: AgentWaiting}, "QUESTION"},
		{Task{Agent: AgentBlocked}, "BLOCKED"},
		{Task{Agent: AgentDead}, "dead"},
		{Task{Agent: AgentIdle, Sentinel: "done"}, "idle ✓"},
		{Task{Agent: AgentIdle, Sentinel: "done_unverified"}, "done?"},
		{Task{Agent: AgentIdle}, "stalled?"},
		{Task{Agent: AgentWorking, Done: true}, "done"},
		{Task{Agent: AgentWorking, Paused: true}, "paused"},
	}
	for _, c := range cases {
		task := c.t
		if got := task.Label(); got != c.want {
			t.Errorf("Label(%+v) = %q, want %q", c.t, got, c.want)
		}
	}
}

func TestResolveAllocs(t *testing.T) {
	now := time.Now()
	task := &Task{Agent: AgentIdle, Sentinel: "done", SessionID: "sid"}
	cs := &ClaudeStatus{Status: "idle", SessionID: "sid"}
	sig := Signals{WindowExists: true, PaneCommand: "claude", Claude: cs}
	if n := testing.AllocsPerRun(100, func() { _ = Resolve(task, sig, now) }); n > 1 {
		t.Errorf("Resolve allocs = %v, want <= 1", n)
	}
}

func TestFoldWorkerStateEvents(t *testing.T) {
	t0 := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	tasks := map[string]*Task{}
	ev := func(typ string, at time.Duration, d map[string]string) {
		fold(tasks, Event{Time: t0.Add(at), Type: typ, Ticket: "T-1", Data: d})
	}
	ev(EvTaskCreated, 0, nil)
	ev(EvSessionStarted, time.Second, map[string]string{"session_id": "sid"})
	ev(EvNotification, 2*time.Second, map[string]string{"notification_type": "idle_prompt"})
	task := tasks["T-1"]
	if !task.WaitingSince.IsZero() {
		t.Error("idle_prompt notification must not set WaitingSince")
	}
	ev(EvNotification, 3*time.Second, map[string]string{"notification_type": "permission_prompt"})
	if task.WaitingSince.IsZero() || task.Label() != "waiting (permission prompt)" {
		t.Errorf("permission_prompt: WaitingSince=%v label=%q", task.WaitingSince, task.Label())
	}
	ev(EvPromptSubmitted, 4*time.Second, nil)
	if !task.WaitingSince.IsZero() || task.PromptAt.IsZero() {
		t.Error("prompt_submitted must clear WaitingSince and set PromptAt")
	}
	ev(EvNotification, 5*time.Second, map[string]string{"notification_type": "elicitation_dialog"})
	ev(EvAgentStatus, 6*time.Second, map[string]string{"status": AgentIdle})
	if !task.WaitingSince.IsZero() {
		t.Error("agent_status must clear WaitingSince")
	}
	ev(EvPromptSubmitted, 7*time.Second, nil)
	if task.Agent != AgentWorking {
		t.Errorf("prompt_submitted on idle: Agent = %q, want working", task.Agent)
	}
	ev(EvStopFailed, 8*time.Second, map[string]string{"error": strings.Repeat("é", 500), "details": "d"})
	if n := len([]rune(task.StopError)); n != 200 {
		t.Errorf("StopError runes = %d, want 200", n)
	}
	ev(EvSetupFailed, 9*time.Second, map[string]string{"error": "boom"})
	if !task.SetupFailed || task.SetupError != "boom" {
		t.Error("setup_failed not folded")
	}
	ev(EvAnswered, 10*time.Second, nil)
	if !task.AnsweredAt.Equal(t0.Add(10 * time.Second)) {
		t.Error("answered must stamp AnsweredAt")
	}
}
