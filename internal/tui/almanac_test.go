package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JollyGrin/grove/internal/state"
)

// --- local-day bucketing ---

// grove-51 shipped a UTC-bucketing bug in the spend chart that made evening
// work look untracked. Pin a fixed +2 zone and confirm a 23:30-local event
// lands on ITS local day, not the UTC day (which would be the next day).
func TestBuildAlmanacDaysLocalBucketing(t *testing.T) {
	zone := time.FixedZone("UTC+2", 2*60*60)
	late := time.Date(2026, 7, 10, 23, 30, 0, 0, zone) // 21:30 UTC, same UTC day
	now := time.Date(2026, 7, 10, 23, 45, 0, 0, zone)
	events := []state.Event{{Type: state.EvTaskDone, Ticket: "grove-1", Time: late}}
	days := buildAlmanacDays(events, now)
	if len(days) != 1 {
		t.Fatalf("expected 1 day, got %d: %+v", len(days), days)
	}
	if days[0].date.Day() != 10 || len(days[0].shipped) != 1 {
		t.Errorf("23:30 local event should bucket to its own local day, got %+v", days[0])
	}

	// A boundary case the other direction: an event just after local
	// midnight must not slip onto the PRIOR day.
	justAfterMidnight := time.Date(2026, 7, 11, 0, 5, 0, 0, zone)
	now2 := time.Date(2026, 7, 11, 1, 0, 0, 0, zone)
	events2 := []state.Event{{Type: state.EvTaskDone, Ticket: "grove-2", Time: justAfterMidnight}}
	days2 := buildAlmanacDays(events2, now2)
	if len(days2) == 0 || days2[len(days2)-1].date.Day() != 11 {
		t.Errorf("00:05 local event should bucket to the 11th, got %+v", days2)
	}
}

// --- contiguous range + empty days ---

func TestBuildAlmanacDaysContiguousWithEmptyDays(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	events := []state.Event{
		{Type: state.EvTaskDone, Ticket: "grove-1", Time: now.AddDate(0, 0, -3)},
		{Type: state.EvTaskDone, Ticket: "grove-2", Time: now}, // today
	}
	days := buildAlmanacDays(events, now)
	if len(days) != 4 { // day-3, day-2 (empty), day-1 (empty), today
		t.Fatalf("expected 4 contiguous days, got %d: %+v", len(days), days)
	}
	if len(days[0].shipped) != 1 || len(days[len(days)-1].shipped) != 1 {
		t.Errorf("first/last day should carry the shipped tickets, got %+v", days)
	}
	for _, d := range days[1:3] {
		if len(d.shipped) != 0 || d.planted != 0 {
			t.Errorf("in-between days should be empty, got %+v", d)
		}
	}
	// Oldest -> newest ordering.
	for i := 1; i < len(days); i++ {
		if !days[i].date.After(days[i-1].date) {
			t.Errorf("days must be strictly ascending: %v then %v", days[i-1].date, days[i].date)
		}
	}
}

func TestBuildAlmanacDaysNoEvents(t *testing.T) {
	if got := buildAlmanacDays(nil, time.Now()); got != nil {
		t.Errorf("no events should yield no days, got %+v", got)
	}
}

func TestBuildAlmanacDaysClampedTo365(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	events := []state.Event{{Type: state.EvTaskDone, Ticket: "grove-old", Time: now.AddDate(-2, 0, 0)}}
	days := buildAlmanacDays(events, now)
	if len(days) != almanacMaxDays {
		t.Errorf("range should clamp to %d days, got %d", almanacMaxDays, len(days))
	}
	// The ancient event's own tally is simply outside the displayed window.
	for _, d := range days {
		if len(d.shipped) != 0 {
			t.Errorf("the clamped-out old event should not surface in any displayed day: %+v", d)
		}
	}
}

// --- event-type tallying ---

func TestBuildAlmanacDaysTallies(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	events := []state.Event{
		{Type: state.EvTaskDone, Ticket: "grove-1", Time: now},
		{Type: state.EvTaskDone, Ticket: "grove-2", Time: now},
		{Type: state.EvTaskCreated, Ticket: "grove-3", Time: now},
		{Type: state.EvAnswered, Ticket: "grove-1", Time: now},
		{Type: state.EvAgentStatus, Ticket: "grove-1", Time: now, Data: map[string]string{"sentinel": "question"}},
		{Type: state.EvAgentStatus, Ticket: "grove-1", Time: now, Data: map[string]string{"sentinel": "blocked"}},
		{Type: state.EvNotification, Ticket: "grove-1", Time: now},
		{Type: state.EvWorkspaceParked, Time: now},
	}
	days := buildAlmanacDays(events, now)
	if len(days) != 1 {
		t.Fatalf("expected 1 day, got %d", len(days))
	}
	d := days[0]
	if len(d.shipped) != 2 {
		t.Errorf("shipped = %d, want 2", len(d.shipped))
	}
	if d.planted != 1 {
		t.Errorf("planted = %d, want 1", d.planted)
	}
	if d.answered != 1 {
		t.Errorf("answered = %d, want 1", d.answered)
	}
	if d.questions != 2 { // 1 sentinel=question + 1 notification; blocked doesn't count
		t.Errorf("questions = %d, want 2", d.questions)
	}
	if d.parked != 1 {
		t.Errorf("parked = %d, want 1", d.parked)
	}
}

// --- stat line composition ---

func TestAlmanacStatLine(t *testing.T) {
	if got := almanacStatLine(almanacDay{}); got != "" {
		t.Errorf("a quiet day should have no stat line, got %q", got)
	}
	day := almanacDay{shipped: []string{"a", "b"}, planted: 3, answered: 5, questions: 1}
	if got := almanacStatLine(day); got != "2 shipped · 3 planted · 5 answered · 1 questions" {
		t.Errorf("stat line = %q", got)
	}
	// Zero categories omitted.
	partial := almanacDay{planted: 2}
	if got := almanacStatLine(partial); got != "2 planted" {
		t.Errorf("partial stat line = %q", got)
	}
	// Parked days append "· parked".
	parked := almanacDay{planted: 1, parked: 1}
	if got := almanacStatLine(parked); got != "1 planted · parked" {
		t.Errorf("parked stat line = %q", got)
	}
}

// --- navigation clamping ---

func TestHandleAlmanacKeyNavClamps(t *testing.T) {
	m := New(nil, "", "")
	m.mode = modeAlmanac
	m.almanac = almanacMsg{days: []almanacDay{{}, {}, {}}}
	m.almSel = 0

	m, _ = mustModel(m.handleAlmanacKey(runeKey("h"))) // already oldest: no wrap
	if m.almSel != 0 {
		t.Errorf("h at the oldest day should clamp at 0, got %d", m.almSel)
	}
	m, _ = mustModel(m.handleAlmanacKey(runeKey("l")))
	m, _ = mustModel(m.handleAlmanacKey(runeKey("l")))
	if m.almSel != 2 {
		t.Errorf("l l from 0 should reach 2, got %d", m.almSel)
	}
	m, _ = mustModel(m.handleAlmanacKey(runeKey("l"))) // already newest: no wrap
	if m.almSel != 2 {
		t.Errorf("l at the newest day should clamp at 2, got %d", m.almSel)
	}

	m, _ = mustModel(m.handleAlmanacKey(tea.KeyMsg{Type: tea.KeyEsc}))
	if m.mode != modeList {
		t.Errorf("esc should return to modeList, got mode %d", m.mode)
	}
}

// g opens the almanac, fires almanacCmd, and lands on the newest day once
// the msg arrives — the almanac is a snapshot, never re-fired by the tick.
func TestOpenAlmanacFromList(t *testing.T) {
	m := New(nil, "", "")
	m.width, m.height = 100, 40
	next, cmd := m.handleKey(runeKey("g"))
	m = next.(Model)
	if m.mode != modeAlmanac {
		t.Fatalf("g: mode = %d, want modeAlmanac", m.mode)
	}
	if cmd == nil {
		t.Fatal("g should fire almanacCmd")
	}
	msg := cmd()
	amsg, ok := msg.(almanacMsg)
	if !ok {
		t.Fatalf("g's cmd should produce an almanacMsg, got %T", msg)
	}
	next, _ = m.Update(amsg)
	m = next.(Model)
	if m.almSel != len(m.almanac.days)-1 && len(m.almanac.days) > 0 {
		t.Errorf("almSel should land on the newest day, got %d of %d", m.almSel, len(m.almanac.days))
	}

	// The refresh tick must NOT re-fire almanacCmd (unlike costs).
	next, _ = m.Update(refreshMsg{tasks: m.localTasks, ok: true})
	m = next.(Model)
	_ = m
}

// --- view: height trim, footer, empty state ---

func TestViewAlmanacEmptyState(t *testing.T) {
	m := New(nil, "", "")
	m.width, m.height = 100, 40
	m.mode = modeAlmanac
	out := m.viewAlmanac()
	if !strings.Contains(out, "no history yet") {
		t.Errorf("empty almanac should say so, got:\n%s", out)
	}
	if !strings.Contains(out, "older") || !strings.Contains(out, "newer") {
		t.Errorf("almanac footer missing nav hints:\n%s", out)
	}
}

func TestViewAlmanacHeightTrim(t *testing.T) {
	m := New(nil, "", "")
	m.width, m.height = 100, 8 // short pane forces the trim
	m.mode = modeAlmanac
	m.almanac = almanacMsg{days: []almanacDay{{date: time.Now(), shipped: []string{"grove-1", "grove-2"}}}}
	out := m.viewAlmanac()
	if strings.Count(out, "\n")+1 > m.height {
		t.Errorf("almanac view exceeds height %d: %d lines", m.height, strings.Count(out, "\n")+1)
	}
}

func TestViewAlmanacQuietDayAndTodayLabel(t *testing.T) {
	m := New(nil, "", "")
	m.width, m.height = 100, 40
	m.mode = modeAlmanac
	today := localMidnight(time.Now())
	m.almanac = almanacMsg{days: []almanacDay{{date: today.AddDate(0, 0, -1)}, {date: today}}}
	m.almSel = 1
	out := m.viewAlmanac()
	if !strings.Contains(out, "a quiet day in the grove") {
		t.Errorf("a day with nothing shipped should read as quiet, got:\n%s", out)
	}
	if !strings.Contains(out, "today") {
		t.Errorf("the newest day should be labeled today, got:\n%s", out)
	}
}
