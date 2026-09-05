package tui

// The cockpit as supervisor (grove-254, part 4/4 of the supervisor train).
//
// While the cockpit is open it drives internal/supervise on the data it
// already reads every tick: refreshMsg carries each task's pane read
// (detect.LiveInfo) once a second, prsMsg carries the PR poll every 30s or
// on `r`. Both are fed to supervise.Transitions in Update — never in View —
// and the results state.Append'ed; the folder picks them up on the next
// tick like any hook append. No new goroutine, poll, timer, or cache (the
// cockpit RAM rule): the only additions are the engine's hysteresis Memory
// (one small struct of per-ticket maps) and the flock.
//
// Single emitter: the same <state>/supervise.lock a headless `gv supervise`
// takes. Whoever holds it emits; the other stays silent. A cockpit that
// finds the lock taken renders `⟳ supervised by pid N` in the header and
// never appends; a `gv supervise` started under an open cockpit gets the
// existing "already supervised (pid N)" refusal.

import (
	"errors"
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JollyGrin/grove/internal/detect"
	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/supervise"
)

// acquireSupervise takes the workspace's supervise lock. Holder: the
// engine memory is armed and the cockpit emits. Refused: the pid that
// holds it is rendered in the header and the cockpit emits nothing. No
// state dir (tests' bare New) means no lock and no supervision.
func (m *Model) acquireSupervise() {
	if m.stateDir == "" || m.sup != nil {
		return
	}
	unlock, err := supervise.Lock(m.stateDir)
	if err != nil {
		var le *supervise.LockErr
		if errors.As(err, &le) {
			m.supNote = "⟳ supervised by pid " + strconv.Itoa(le.PID) + " · "
		}
		return
	}
	m.sup, m.supUnlock, m.supNote = supervise.NewMemory(), unlock, ""
}

// releaseSupervise hands the lock back (quit, park). Idempotent.
func (m *Model) releaseSupervise() {
	if m.supUnlock != nil {
		m.supUnlock()
	}
	m.sup, m.supUnlock = nil, nil
}

// superviseObserve runs one beat of the engine over tasks — the folded
// local tasks of the latest refresh — against the given PR set and pane
// reads, appends whatever fired, flashes the operator-facing ones, and
// returns the push as a one-shot command (ntfy/desktop are synchronous
// with a 1.5s bound; they must not stall the frame). Returns nil when
// nothing fired or the cockpit is not the lock holder.
//
// infos may be nil (the prsMsg beat): a zero LiveInfo is out of liveness
// scope by construction, so only the delivery dimension runs. unknown is
// prsCmd's gh-failure set — PRKnown=false for those tickets, so a gh
// outage never emits a delivery transition from here either.
func (m *Model) superviseObserve(tasks []*state.Task, infos map[string]detect.LiveInfo, prs map[string]*github.PR, unknown map[string]bool, now time.Time) tea.Cmd {
	if m.sup == nil {
		return nil
	}
	var fired []state.Event
	for _, t := range tasks {
		obs := supervise.Observation{
			Task:    t,
			PR:      prs[t.Ticket],
			PRKnown: !unknown[t.Ticket],
			Live:    infos[t.Ticket],
			Now:     now,
		}
		for _, ev := range supervise.Transitions(obs, m.sup) {
			if err := state.Append(m.stateDir, ev); err != nil {
				m.flash = "supervise: " + err.Error()
				return nil
			}
			fired = append(fired, ev)
		}
	}
	if len(fired) == 0 {
		return nil
	}
	for _, ev := range fired {
		if line := superviseFlash(ev, prs[ev.Ticket]); line != "" {
			m.flash = line
		}
	}
	return func() tea.Msg {
		for _, ev := range fired {
			supervise.Push(ev)
		}
		return nil
	}
}

// superviseFlash is the footer line for an emitted transition — the
// operator-facing subset (pr_ready/pr_ci_failed/pr_conflicting/worker_*).
// pr_merged keeps its own sparkle in the prsMsg handler; the quiet rows
// of the push table (pr_opened/updated/closed) stay quiet here too.
func superviseFlash(ev state.Event, pr *github.PR) string {
	d := ev.Data
	switch ev.Type {
	case state.EvPRReady:
		if pr != nil && pr.Checks > 0 {
			return "✓ " + ev.Ticket + " ready — " + strconv.Itoa(pr.Checks) + " checks green"
		}
		return "✓ " + ev.Ticket + " ready — #" + d["pr"]
	case state.EvPRCIFailed:
		return "✗ " + ev.Ticket + " CI failed — " + d["failing"]
	case state.EvPRConflicting:
		return "✗ " + ev.Ticket + " conflicting — " + d["merge_state"]
	case state.EvWorkerWaiting:
		return "? " + ev.Ticket + " waiting on a menu — " + d["marker"]
	case state.EvWorkerVanished:
		return "∅ " + ev.Ticket + " vanished — no claude in its pane"
	case state.EvWorkerErrored:
		return "✗ " + ev.Ticket + " errored — " + d["reason"]
	case state.EvWorkerRecovered:
		return "✓ " + ev.Ticket + " recovered from " + d["from"]
	}
	return ""
}
