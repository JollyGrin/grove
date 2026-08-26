package fleet

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/state"
)

func task(ticket string) *state.Task {
	return &state.Task{Ticket: ticket, Repo: "grove", Branch: ticket + "-work", Agent: state.AgentWorking}
}

func tomb(ticket, host string) *state.Task {
	t := task(ticket)
	t.Done = true
	t.Agent = state.AgentIdle
	t.HandedOffTo = host
	t.Updated = time.Date(2026, 8, 22, 9, 0, 0, 0, time.UTC)
	return t
}

func tickets(rows []Row) string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Ticket+"@"+r.Host+"/"+r.Live)
	}
	return strings.Join(out, " ")
}

func TestMergeLocalOnlyStampsHostAndListsTombstones(t *testing.T) {
	rows, warn := Merge([]Row{{Task: task("gr-1"), Live: "working"}}, []*state.Task{tomb("gr-2", "vps")}, nil)
	if len(warn) != 0 {
		t.Fatalf("unexpected warnings %v", warn)
	}
	if got := tickets(rows); got != "gr-1@local/working gr-2@/handed-off" {
		t.Fatalf("rows = %q", got)
	}
	if rows[1].HandedOffTo == "" {
		t.Fatal("tombstone row lost its handed_off_to pointer")
	}
	if IsRemote(rows[0]) || IsRemote(rows[1]) {
		t.Fatal("local/tombstone rows read as remote")
	}
}

func TestMergeStampsTheRowNeverTheTask(t *testing.T) {
	// Host is merge-output display data: it lands on the Row, and the
	// caller's Task pointer rides through untouched (the persisted type
	// has no host field to leak into tasks.json).
	local := task("gr-1")
	rows, _ := Merge([]Row{{Task: local, Live: "working"}}, nil, nil)
	if rows[0].Host != LocalHost || rows[0].Task != local {
		t.Fatalf("row = host %q task-ptr-same %v", rows[0].Host, rows[0].Task == local)
	}
}

func TestMergeTombstoneReplacedByLiveRemoteRow(t *testing.T) {
	rows, warn := Merge(
		[]Row{{Task: task("gr-1"), Live: "working"}},
		[]*state.Task{tomb("gr-2", "vps")},
		[]Result{{Host: "vps", Rows: []Row{{Task: task("gr-2"), Host: "vps", Live: "working"}}}},
	)
	if len(warn) != 0 {
		t.Fatalf("unexpected warnings %v", warn)
	}
	if got := tickets(rows); got != "gr-1@local/working gr-2@vps/working" {
		t.Fatalf("rows = %q", got)
	}
	if rows[1].HandedOffTo != "" {
		t.Fatal("live remote row should not carry the tombstone")
	}
}

func TestMergeLocalRowWinsOverStaleRemoteDuplicate(t *testing.T) {
	// After a `--from` take-back the task runs locally again while a cached
	// remote answer still lists it: the live local row is the truth, and
	// the stale remote copy must not appear as a second row (nor shadow a
	// dead local worker with the remote's old 'working').
	rows, warn := Merge(
		[]Row{{Task: task("gr-1"), Live: "gone"}},
		nil,
		[]Result{{Host: "vps", Rows: []Row{
			{Task: task("gr-1"), Host: "vps", Live: "working"},
			{Task: task("gr-4"), Host: "vps", Live: "working"},
		}}},
	)
	if len(warn) != 0 {
		t.Fatalf("unexpected warnings %v", warn)
	}
	if got := tickets(rows); got != "gr-1@local/gone gr-4@vps/working" {
		t.Fatalf("rows = %q", got)
	}
}

func TestMergeStaleTombstoneWhenNamedHostAnswersWithoutIt(t *testing.T) {
	rows, _ := Merge(nil, []*state.Task{tomb("gr-2", "vps"), tomb("gr-3", "pc")},
		[]Result{{Host: "vps", Rows: []Row{{Task: task("gr-7"), Host: "vps", Live: "idle"}}}})
	// gr-2: vps answered without it → stale. gr-3: pc was not asked → plain handed-off.
	if got := tickets(rows); got != "gr-7@vps/idle gr-2@/handed-off? gr-3@/handed-off" {
		t.Fatalf("rows = %q", got)
	}
	line := Elsewhere(rows[1], func(time.Time) string { return "2h" })
	want := "gr-2        grove       → vps? (handed off 2h; take back: gv handoff gr-2 --from vps · drop row: gv untrack gr-2)"
	if line != want {
		t.Fatalf("Elsewhere = %q, want %q", line, want)
	}
	if line := Elsewhere(rows[2], func(time.Time) string { return "2h" }); !strings.Contains(line, "→ pc (") {
		t.Fatalf("plain tombstone rendered as stale: %q", line)
	}
}

func TestMergeHostFailureWarnsAndKeepsLocal(t *testing.T) {
	rows, warn := Merge([]Row{{Task: task("gr-1"), Live: "working"}}, []*state.Task{tomb("gr-2", "vps")},
		[]Result{{Host: "vps", Err: errors.New("timed out after 5s")}})
	if len(warn) != 1 || warn[0] != "host vps: timed out after 5s" {
		t.Fatalf("warnings = %v", warn)
	}
	// The unreachable host cannot confirm or deny — the tombstone stays plain.
	if got := tickets(rows); got != "gr-1@local/working gr-2@/handed-off" {
		t.Fatalf("rows = %q", got)
	}
}

func TestDecodeStampsHostAndDropsRemoteTombstones(t *testing.T) {
	raw := []byte(`{"schema_version":1,"tasks":[
	  {"ticket":"gr-5","host":"local","agent":"working","live":"working"},
	  {"ticket":"gr-6","done":true,"handed_off_to":"mac","live":"handed-off"}
	]}`)
	rows, err := Decode("vps", raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := tickets(rows); got != "gr-5@vps/working" {
		t.Fatalf("rows = %q", got)
	}
	if _, err := Decode("vps", []byte("not json")); err == nil {
		t.Fatal("garbage decoded without error")
	}
}

func TestFetchParallelWithFailures(t *testing.T) {
	cfg := &config.Config{Hosts: map[string]*config.Host{
		"a": {SSH: "a.example", GV: "gv"},
		"b": {SSH: "b.example", GV: "gv"},
	}}
	run := func(ctx context.Context, h *config.Host) ([]byte, error) {
		if h.SSH == "b.example" {
			return nil, errors.New("ssh: connect refused")
		}
		return []byte(`{"schema_version":1,"tasks":[{"ticket":"gr-9","live":"working"}]}`), nil
	}
	res := Fetch(context.Background(), cfg, []string{"a", "b", "nope"}, run)
	if len(res) != 3 || res[0].Host != "a" || res[1].Host != "b" || res[2].Host != "nope" {
		t.Fatalf("order/len = %+v", res)
	}
	if res[0].Err != nil || len(res[0].Rows) != 1 || res[0].Rows[0].Host != "a" {
		t.Fatalf("host a = %+v", res[0])
	}
	if res[1].Err == nil || res[2].Err == nil {
		t.Fatalf("failures not surfaced: %+v", res[1:])
	}
	_, warn := Merge(nil, nil, res)
	if len(warn) != 2 {
		t.Fatalf("warnings = %v", warn)
	}
}
