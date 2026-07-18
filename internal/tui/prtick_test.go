package tui

// grove-118 (grove-24 pattern, second occurrence): the prsMsg handler used to
// unconditionally re-arm its own 30s tick, so every ad-hoc prsCmd delivery —
// including the one 'r' (manual refresh) fires — permanently added another
// self-perpetuating PR-poll loop. Only prTickMsg, the canonical beat, may
// re-arm; prsMsg must be pure data application.

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// countArmedPRTicks executes cmd (recursing into tea.Batch results) and
// counts how many resulting messages are prTickMsg — i.e. how many PR-poll
// loops are currently armed downstream of this Update call.
func countArmedPRTicks(t *testing.T, cmd tea.Cmd) int {
	t.Helper()
	if cmd == nil {
		return 0
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		n := 0
		for _, c := range msg {
			n += countArmedPRTicks(t, c)
		}
		return n
	case prTickMsg:
		return 1
	default:
		return 0
	}
}

// The canonical loop: a prTickMsg beat must re-arm itself exactly once.
func TestPRTickReArmsExactlyOnce(t *testing.T) {
	m := fixtureModel(t)
	_, cmd := m.Update(prTickMsg{})
	if n := countArmedPRTicks(t, cmd); n != 1 {
		t.Errorf("prTickMsg re-armed %d PR-poll loops, want exactly 1", n)
	}
}

// The regression: N ad-hoc prsMsg deliveries — the shape every manual 'r'
// refresh and post-action refresh takes — must never arm a PR-poll loop.
// Before the fix, every single one did, so five 'r' presses left six
// concurrent 30s loops running forever.
func TestAdHocPRsMsgDoesNotArmPRTick(t *testing.T) {
	m := fixtureModel(t)
	total := 0
	for i := 0; i < 5; i++ {
		next, cmd := m.Update(prsMsg{})
		m = next.(Model)
		total += countArmedPRTicks(t, cmd)
	}
	if total != 0 {
		t.Errorf("5 ad-hoc prsMsg deliveries armed %d PR-poll loops, want 0", total)
	}
}

// 'r' fires prsCmd directly (never prTickEvery) — the actual key-press path
// the bug rode on. This pins that prsCmd's own cmd, not just the handler,
// stays inert with respect to the tick.
func TestManualRefreshKeyDoesNotArmPRTick(t *testing.T) {
	m := fixtureModel(t)
	for i := 0; i < 5; i++ {
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
		m = next.(Model)
		if n := countArmedPRTicks(t, cmd); n != 0 {
			t.Errorf("press %d of 'r' armed %d PR-poll loops, want 0", i+1, n)
		}
	}
}
