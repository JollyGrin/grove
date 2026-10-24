package tui

// grove-507: the PR beat follows the GraphQL quota plan. Same re-arm
// discipline as prtick_test.go / feattick_test.go: only prTickMsg re-arms,
// at the planned interval; a skipped beat keeps the PRs on screen, stale.

import (
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/github"
)

func TestPRTickReArmsAtPlannedInterval(t *testing.T) {
	m := fixtureModel(t)
	m.prInterval = 0 // the fixture pins a fast beat (grove-572); this asserts the unplanned default
	if got := m.prBeat(); got != github.BeatNormal {
		t.Errorf("default beat = %v, want %v", got, github.BeatNormal)
	}
	m.prInterval = time.Millisecond // fast, and proves prBeat honours the plan
	if got := m.prBeat(); got != time.Millisecond {
		t.Errorf("planned beat = %v", got)
	}
	_, cmd := m.Update(prTickMsg{})
	if n := countArmedPRTicks(t, cmd); n != 1 {
		t.Errorf("prTickMsg re-armed %d loops, want exactly 1", n)
	}
}

func TestSkippedBeatKeepsPRsAndMarksStale(t *testing.T) {
	m := fixtureModel(t)
	m.prs = map[string]*github.PR{"T-1": {Number: 3, State: "OPEN"}}
	m.prUnknown = map[string]bool{"T-2": true}
	next, cmd := m.Update(prsMsg{skipped: true, interval: 5 * time.Minute, low: true})
	m = next.(Model)
	if countArmedPRTicks(t, cmd) != 0 {
		t.Error("a skipped prsMsg armed a PR-poll loop")
	}
	if m.prs["T-1"] == nil || !m.prUnknown["T-2"] {
		t.Errorf("skipped beat blanked PR data: prs=%v unknown=%v", m.prs, m.prUnknown)
	}
	if !m.prStale || !m.prQuotaLow || m.prBeat() != 5*time.Minute {
		t.Errorf("stale=%v low=%v beat=%v", m.prStale, m.prQuotaLow, m.prBeat())
	}
	// The next real poll clears stale; low tracks the plan.
	next, _ = m.Update(prsMsg{prs: map[string]*github.PR{}, interval: github.BeatNormal})
	m = next.(Model)
	if m.prStale || m.prQuotaLow {
		t.Errorf("fresh poll left stale=%v low=%v", m.prStale, m.prQuotaLow)
	}
}
