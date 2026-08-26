package tui

// grove-178: the R remote-fleet merge. One fetch per keypress (never a
// tick), the board is rebuilt in Update (assemble), local-only with R off,
// and remote/tombstone rows are read-only on the cockpit.

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/fleet"
	"github.com/JollyGrin/grove/internal/state"
)

func remoteTestModel(t *testing.T) Model {
	t.Helper()
	cfg := &config.Config{Hosts: map[string]*config.Host{"vps": {SSH: "vps", GV: "gv"}}}
	m := New(cfg, t.TempDir(), "test")
	m.width, m.height = 120, 40
	at := time.Now().Add(-2 * time.Hour)
	local := &state.Task{Ticket: "gr-1", Repo: "grove", Agent: state.AgentWorking, Created: at}
	tomb := &state.Task{Ticket: "gr-2", Repo: "grove", Branch: "gr-2-work", Done: true, Agent: state.AgentIdle,
		Created: at, Updated: at, HandedOffTo: "vps"}
	next, _ := m.Update(refreshMsg{tasks: []*state.Task{local}, handedOff: []*state.Task{tomb},
		live: map[string]string{"gr-1": "working"}, ok: true})
	return next.(Model)
}

func key(r string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(r)} }

func boardTickets(m Model) string {
	var out []string
	for _, r := range m.board {
		out = append(out, r.Ticket+"@"+r.Host)
	}
	return strings.Join(out, " ")
}

func TestRemoteOffBoardIsLocalOnly(t *testing.T) {
	m := remoteTestModel(t)
	if got := boardTickets(m); got != "gr-1@local" {
		t.Fatalf("board with R off = %q", got)
	}
	if len(m.scene) != 1 || m.scene[0].Ticket != "gr-1" {
		t.Fatalf("scene with R off = %+v", m.scene)
	}
	if !strings.Contains(m.View(), "gr-1") || strings.Contains(m.View(), "gr-2") {
		t.Fatal("tombstone rendered with R off")
	}
}

func TestRemoteToggleFetchesOnceAndMerges(t *testing.T) {
	m := remoteTestModel(t)
	next, cmd := m.Update(key("R"))
	m = next.(Model)
	if !m.remote || cmd == nil {
		t.Fatal("R did not arm the merge + one fetch")
	}
	// Tombstone shows at once (before the answer), after the live rows.
	if got := boardTickets(m); got != "gr-1@local gr-2@" {
		t.Fatalf("board right after R = %q", got)
	}
	if len(m.scene) != 1 {
		t.Fatalf("scene must exclude the tombstone, got %+v", m.scene)
	}
	if v := m.View(); !strings.Contains(v, "→ vps (") || !strings.Contains(v, "handed off") {
		t.Fatalf("tombstone line missing:\n%s", v)
	}

	// The answer: vps runs gr-2 live → tombstone replaced, row tagged @vps.
	remote := &state.Task{Ticket: "gr-2", Repo: "grove", Agent: state.AgentWorking, Created: time.Now()}
	answer := []fleet.Result{{Host: "vps", Rows: []fleet.Row{{Task: remote, Host: "vps", Live: "working"}}}}
	next, cmd = m.Update(remoteMsg{results: answer})
	m = next.(Model)
	if cmd != nil {
		t.Fatal("remoteMsg re-armed something — the RAM rule forbids a poll")
	}
	if got := boardTickets(m); got != "gr-1@local gr-2@vps" {
		t.Fatalf("merged board = %q", got)
	}
	if m.board[1].HandedOffTo != "" || !fleet.IsRemote(m.board[1].Row) {
		t.Fatalf("merged row = %+v", m.board[1])
	}
	// The shared local live map is never written by the merge (a remote
	// row's Live rides on the board row alone).
	if len(m.live) != 1 || m.live["gr-1"] != "working" {
		t.Fatalf("merge leaked into m.live: %v", m.live)
	}
	if v := m.View(); !strings.Contains(v, "@vps") {
		t.Fatalf("host tag missing:\n%s", v)
	}
	if m.flash != "remote fleet: 1 row(s) from 1 host(s)" {
		t.Fatalf("flash = %q", m.flash)
	}

	// A refresh while R is on keeps the merge (local task list rebuilt).
	next, _ = m.Update(refreshMsg{tasks: m.localTasks, handedOff: m.handedOff, live: map[string]string{"gr-1": "working"}, ok: true})
	m = next.(Model)
	if got := boardTickets(m); got != "gr-1@local gr-2@vps" {
		t.Fatalf("merge lost on refresh: %q", got)
	}

	// Off again: local-only, answer discarded, and a late answer is dropped.
	next, _ = m.Update(key("R"))
	m = next.(Model)
	if m.remote || len(m.board) != 1 || m.remoteResults != nil {
		t.Fatalf("R off did not restore local board: %q", boardTickets(m))
	}
	next, _ = m.Update(remoteMsg{results: answer})
	m = next.(Model)
	if len(m.board) != 1 {
		t.Fatal("late remoteMsg applied after R off")
	}
}

// Toggling R off with the cursor parked on a trailing remote/tombstone row
// must clamp the selection — otherwise enter/d act on a row the user never
// visibly picked (the board shrank under them).
func TestRemoteOffClampsSelection(t *testing.T) {
	m := remoteTestModel(t)
	next, _ := m.Update(key("R"))
	m = next.(Model)
	m.sel = 1 // the tombstone row
	next, _ = m.Update(key("R"))
	m = next.(Model)
	if m.sel != 0 {
		t.Fatalf("sel = %d after the board shrank to 1 row", m.sel)
	}
}

func TestRemoteHostFailureWarnsKeepsLocal(t *testing.T) {
	m := remoteTestModel(t)
	next, _ := m.Update(key("R"))
	m = next.(Model)
	next, _ = m.Update(remoteMsg{results: []fleet.Result{{Host: "vps", Err: errors.New("timed out after 5s")}}})
	m = next.(Model)
	if m.flash != "remote: vps: timed out after 5s" {
		t.Fatalf("flash = %q", m.flash)
	}
	// Unreachable host can't confirm or deny: the tombstone stays plain.
	if len(m.board) != 2 || m.board[1].Live != fleet.LiveElsewhere {
		t.Fatalf("board = %q live=%q", boardTickets(m), m.board[1].Live)
	}
}

func TestRemoteRowsAreReadOnly(t *testing.T) {
	m := remoteTestModel(t)
	next, _ := m.Update(key("R"))
	m = next.(Model)
	m.sel = 1 // the tombstone
	for _, k := range []string{"enter", "n", "a", "d", "v"} {
		var msg tea.KeyMsg
		if k == "enter" {
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		} else {
			msg = key(k)
		}
		next, cmd := m.Update(msg)
		got := next.(Model)
		if got.mode != modeList || cmd != nil || !strings.Contains(got.flash, "handed off to vps") {
			t.Fatalf("%q acted on a tombstone: mode=%d flash=%q", k, got.mode, got.flash)
		}
	}
}

// The detail view repoints over LOCAL tasks only, and drops out entirely
// when the ticket leaves the local fleet — a stale detail pointer is how a
// recycled window name steers the wrong worker (grove-116 class).
func TestDetailDropsWhenTaskLeavesLocalFleet(t *testing.T) {
	m := remoteTestModel(t)
	m.detail = m.localTasks[0]
	m.mode = modeDetail
	next, _ := m.Update(refreshMsg{tasks: nil, ok: true}) // gr-1 gone (done/handed off)
	m = next.(Model)
	if m.detail != nil || m.mode != modeList {
		t.Fatalf("detail survived the task leaving: detail=%v mode=%d", m.detail, m.mode)
	}
}

func TestRemoteNoHostsConfigured(t *testing.T) {
	m := New(&config.Config{}, t.TempDir(), "test")
	next, cmd := m.Update(key("R"))
	m = next.(Model)
	if m.remote || cmd != nil || !strings.Contains(m.flash, "no hosts configured") {
		t.Fatalf("R without hosts: remote=%v flash=%q", m.remote, m.flash)
	}
}

func TestHelpDocumentsR(t *testing.T) {
	found := false
	for _, e := range helpGlobal {
		if e.key == "R" {
			found = true
		}
	}
	if !found {
		t.Fatal("R missing from the ? overlay")
	}
}
