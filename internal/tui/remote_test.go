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

func TestRemoteOffBoardIsLocalOnly(t *testing.T) {
	m := remoteTestModel(t)
	if len(m.tasks) != 1 || m.tasks[0].Ticket != "gr-1" || m.tasks[0].Host != "" {
		t.Fatalf("board with R off = %+v", m.tasks)
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
	if len(m.tasks) != 2 || m.tasks[1].Ticket != "gr-2" || m.nLive != 1 {
		t.Fatalf("board right after R = %+v nLive=%d", m.tasks, m.nLive)
	}
	if v := m.View(); !strings.Contains(v, "→ vps (") || !strings.Contains(v, "handed off") {
		t.Fatalf("tombstone line missing:\n%s", v)
	}

	// The answer: vps runs gr-2 live → tombstone replaced, row tagged @vps.
	remote := &state.Task{Ticket: "gr-2", Repo: "grove", Agent: state.AgentWorking, Host: "vps", Created: time.Now()}
	next, cmd = m.Update(remoteMsg{results: []fleet.Result{{Host: "vps", Rows: []fleet.Row{{Task: remote, Live: "working"}}}}})
	m = next.(Model)
	if cmd != nil {
		t.Fatal("remoteMsg re-armed something — the RAM rule forbids a poll")
	}
	if len(m.tasks) != 2 || m.tasks[1].HandedOffTo != "" || !fleet.IsRemote(m.tasks[1]) {
		t.Fatalf("merged board = %+v", m.tasks)
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
	if len(m.tasks) != 2 || m.liveTasks()[1].Host != "vps" {
		t.Fatalf("merge lost on refresh: %+v", m.tasks)
	}
	// The scene only sees live rows: with R on that is the head of the board.
	if len(m.liveTasks()) != m.nLive {
		t.Fatal("liveTasks is not the nLive prefix")
	}

	// Off again: local-only, answer discarded, and a late answer is dropped.
	next, _ = m.Update(key("R"))
	m = next.(Model)
	if m.remote || len(m.tasks) != 1 || m.remoteResults != nil {
		t.Fatalf("R off did not restore local board: %+v", m.tasks)
	}
	next, _ = m.Update(remoteMsg{results: []fleet.Result{{Host: "vps", Rows: []fleet.Row{{Task: remote, Live: "working"}}}}})
	m = next.(Model)
	if len(m.tasks) != 1 {
		t.Fatal("late remoteMsg applied after R off")
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
	if len(m.tasks) != 2 || m.live["gr-2"] != fleet.LiveElsewhere {
		t.Fatalf("board = %+v live=%v", m.tasks, m.live)
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
