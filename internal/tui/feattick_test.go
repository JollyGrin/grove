package tui

// grove-428: the feature panel's gh-backed network pass used to ride the 30s
// prTickMsg beat, burning 3-4 `gh` calls per open feature every 30s — cheap
// data (landed/queued issue lists) refetched on a cadence meant for live PR
// status, enough to exhaust the shared gh rate-limit budget across attached
// cockpits. It now rides its own featTickMsg beat, one per 10min. Same
// re-arm discipline as prTickMsg (grove-118/grove-24 pattern): only
// featTickMsg may re-arm itself — an ad-hoc featuresCmd delivery (the 'r'
// key, the open-feature-set-changed pass) must never add another loop.

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// withFastFeatTick shrinks featTickInterval for the test's duration.
// tea.Tick starts its real timer the instant featTickEvery is CALLED (inside
// Update), not when the returned Cmd is later invoked — so a test that left
// the production 10min interval in place would block real wall-clock
// minutes waiting for the timer instead of asserting shape.
func withFastFeatTick(t *testing.T) {
	t.Helper()
	old := featTickInterval
	featTickInterval = time.Millisecond
	t.Cleanup(func() { featTickInterval = old })
}

// countArmedFeatTicks executes cmd (recursing into tea.Batch results) and
// counts how many resulting messages are featTickMsg — i.e. how many
// feature-network-pass loops are currently armed downstream of this Update
// call.
func countArmedFeatTicks(t *testing.T, cmd tea.Cmd) int {
	t.Helper()
	if cmd == nil {
		return 0
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		n := 0
		for _, c := range msg {
			n += countArmedFeatTicks(t, c)
		}
		return n
	case featTickMsg:
		return 1
	default:
		return 0
	}
}

// The canonical loop: a featTickMsg beat must re-arm itself exactly once.
func TestFeatTickReArmsExactlyOnce(t *testing.T) {
	withFastFeatTick(t)
	m := fixtureModel(t)
	_, cmd := m.Update(featTickMsg{})
	if n := countArmedFeatTicks(t, cmd); n != 1 {
		t.Errorf("featTickMsg re-armed %d feature-poll loops, want exactly 1", n)
	}
}

// The 30s PR-poll beat must no longer carry the feature network pass's
// timer — that's the whole point of grove-428's decoupling.
func TestPRTickDoesNotArmFeatTick(t *testing.T) {
	m := fixtureModel(t)
	_, cmd := m.Update(prTickMsg{})
	if n := countArmedFeatTicks(t, cmd); n != 0 {
		t.Errorf("prTickMsg armed %d feature-poll loops, want 0", n)
	}
}

// The regression shape: N ad-hoc featuresMsg deliveries — what every manual
// 'r' refresh and the open-feature-set-changed pass produce — must never arm
// a feature-poll loop.
func TestAdHocFeaturesMsgDoesNotArmFeatTick(t *testing.T) {
	m := fixtureModel(t)
	total := 0
	for i := 0; i < 5; i++ {
		next, cmd := m.Update(featuresMsg{})
		m = next.(Model)
		total += countArmedFeatTicks(t, cmd)
	}
	if total != 0 {
		t.Errorf("5 ad-hoc featuresMsg deliveries armed %d feature-poll loops, want 0", total)
	}
}

// 'r' fires featuresCmd directly (never featTickEvery) — the actual
// key-press path. This pins that featuresCmd's own cmd, not just the
// handler, stays inert with respect to the tick.
func TestManualRefreshKeyDoesNotArmFeatTick(t *testing.T) {
	m := fixtureModel(t)
	for i := 0; i < 5; i++ {
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
		m = next.(Model)
		if n := countArmedFeatTicks(t, cmd); n != 0 {
			t.Errorf("press %d of 'r' armed %d feature-poll loops, want 0", i+1, n)
		}
	}
}
