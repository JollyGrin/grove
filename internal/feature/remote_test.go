package feature

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/fleet"
	"github.com/JollyGrin/grove/internal/provider"
	"github.com/JollyGrin/grove/internal/state"
)

// TestStatusesRemoteCars (grove-398): a host's tracked car on an open
// feature becomes an active car with its real state and the host; a
// ticket tracked here too stays local; an erroring host is a warning and
// its cars fall back to queued, the status otherwise complete.
func TestStatusesRemoteCars(t *testing.T) {
	events := []state.Event{ev(1, state.EvTaskCreated, "grove-30", onTrain("local car"))}
	remoteTask := func(ticket, feat, sentinel string) fleet.Row {
		return fleet.Row{Task: &state.Task{Ticket: ticket, Title: ticket + " title", Feature: feat, Sentinel: sentinel, Created: t1(2)}}
	}
	calls := 0
	in := StatusInput{
		Features: trainsOnly(), Tasks: foldAll(t, events), Events: events,
		Issues: func(*state.Feature) ([]*provider.Task, error) {
			return []*provider.Task{
				{ID: "grove-30", Labels: []string{"trains"}},
				{ID: "grove-31", Title: "on pc", Labels: []string{"trains"}},
				{ID: "grove-32", Title: "on dead host", Labels: []string{"trains"}},
			}, nil
		},
		Remote: func() []fleet.Result {
			calls++
			pc := []fleet.Row{
				remoteTask("grove-31", "trains", "question"),
				remoteTask("grove-30", "trains", "done"), // also local: local wins
				remoteTask("grove-40", "other", ""),      // other train: ignored
			}
			for i := range pc {
				pc[i].Host = "pc"
			}
			return []fleet.Result{{Host: "pc", Rows: pc}, {Host: "dead", Err: errors.New("timed out after 5s")}}
		},
	}
	st, err := Statuses(in)
	if calls != 1 {
		t.Errorf("Remote called %d times, want once per Statuses", calls)
	}
	if err == nil || !strings.Contains(err.Error(), "host dead") {
		t.Errorf("err = %v, want a warning naming host dead", err)
	}
	s := st["trains"]
	if s == nil {
		t.Fatal("trains status missing")
	}
	want := []string{"grove-30:working", "grove-31:question", "grove-32:queued"}
	if got := tickets(s.Cars); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("cars = %v, want %v", got, want)
	}
	if s.Cars[0].Host != "" || s.Cars[1].Host != "pc" || s.Cars[2].Host != "" {
		t.Errorf("hosts = %q %q %q, want \"\" pc \"\"", s.Cars[0].Host, s.Cars[1].Host, s.Cars[2].Host)
	}

	calls = 0
	in.SkipQueued = true
	_, _ = Statuses(in)
	if calls != 0 {
		t.Errorf("SkipQueued still asked the hosts (%d calls)", calls)
	}
}

// TestRemoteLookupNoHosts: a config with no hosts never builds a lookup.
func TestRemoteLookupNoHosts(t *testing.T) {
	if RemoteLookup(&config.Config{}, nil) != nil || RemoteLookup(nil, nil) != nil {
		t.Error("RemoteLookup without hosts is non-nil")
	}
	n := 0
	run := func(context.Context, *config.Host) ([]byte, error) { n++; return []byte(`{"tasks":[]}`), nil }
	cfg := &config.Config{Hosts: map[string]*config.Host{"pc": {SSH: "pc", GV: "gv"}}}
	if res := RemoteLookup(cfg, run)(); len(res) != 1 || res[0].Err != nil || n != 1 {
		t.Errorf("RemoteLookup = %+v (%d runs)", res, n)
	}
}
