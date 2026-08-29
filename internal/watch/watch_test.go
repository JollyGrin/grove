// grove-205: these pin the two properties that make `gv watch` a signal a
// monitor can trust — a from-now baseline nobody can sample late, and
// coverage that never lets silence read as success.
package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/state"
)

func ev(t time.Time, typ, ticket string, data map[string]string) state.Event {
	return state.Event{Time: t, Type: typ, Ticket: ticket, Data: data, V: 1}
}

func writeLog(t *testing.T, dir string, evs ...state.Event) {
	t.Helper()
	var b bytes.Buffer
	for _, e := range evs {
		line, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendLog(t *testing.T, dir string, evs ...state.Event) {
	t.Helper()
	for _, e := range evs {
		if err := state.Append(dir, e); err != nil {
			t.Fatal(err)
		}
	}
}

func agentStatus(ticket, status, sentinel, msg string) state.Event {
	return ev(time.Now(), state.EvAgentStatus, ticket, map[string]string{
		"status": status, "sentinel": sentinel, "message": msg,
	})
}

// --- Tailer -------------------------------------------------------------

func TestTailerFromNowSkipsHistory(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, agentStatus("t-1", "idle", "done", "already finished"))

	tail := NewTailer(dir, false)
	lines, err := tail.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 0 {
		t.Fatalf("from-now tailer replayed history: %q", lines)
	}
	appendLog(t, dir, agentStatus("t-1", "idle", "done", "fresh"))
	lines, err = tail.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || !strings.Contains(string(lines[0]), "fresh") {
		t.Fatalf("expected the appended record, got %q", lines)
	}
}

func TestTailerReplayReadsHistory(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir,
		agentStatus("t-1", "waiting", "question", "which one?"),
		agentStatus("t-1", "idle", "done", "shipped"))

	lines, err := NewTailer(dir, true).Poll()
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("replay expected 2 records, got %d", len(lines))
	}
}

func TestTailerMissingLogWaitsThenCatchesUp(t *testing.T) {
	dir := t.TempDir()
	tail := NewTailer(dir, false) // no events.jsonl at all
	lines, err := tail.Poll()
	if err != nil {
		t.Fatalf("missing log must not error: %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("missing log yielded %q", lines)
	}
	appendLog(t, dir, agentStatus("t-1", "idle", "done", "first ever"))
	lines, err = tail.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected the first record once the log appeared, got %d", len(lines))
	}
}

func TestTailerHoldsBackTornTrailingLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	full, _ := json.Marshal(agentStatus("t-1", "idle", "done", "complete"))
	if err := os.WriteFile(path, append(append([]byte{}, full...), []byte("\n{\"type\":\"agent_st")...), 0o644); err != nil {
		t.Fatal(err)
	}
	tail := NewTailer(dir, true)
	lines, err := tail.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("torn trailing line was consumed: %q", lines)
	}
	// Completing the append delivers it on the next poll — never twice.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("atus\",\"ticket\":\"t-1\"}\n")
	f.Close()
	lines, err = tail.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected exactly the completed line, got %q", lines)
	}
}

func TestTailerRefollowsAReplacedLog(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, agentStatus("t-1", "idle", "done", "one"), agentStatus("t-1", "idle", "done", "two"))
	tail := NewTailer(dir, false)
	if _, err := tail.Poll(); err != nil {
		t.Fatal(err)
	}
	// Scratch reuse: the log is replaced by a shorter one.
	writeLog(t, dir, agentStatus("t-2", "working", "none", "brand new file"))
	lines, err := tail.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || !strings.Contains(string(lines[0]), "brand new file") {
		t.Fatalf("replaced log not re-followed: %q", lines)
	}
}

// --- Filter -------------------------------------------------------------

func TestFilterDefaultTypesCoverEveryTerminalState(t *testing.T) {
	f := NewFilter(nil, nil, nil, time.Time{})
	// A crashed, wedged, parked or wandered-off worker must produce a line.
	must := []state.Event{
		agentStatus("t-1", "idle", "none", "…just stopped, no STATUS line"),
		ev(time.Now(), state.EvNotification, "t-1", map[string]string{"message": "needs input"}),
		ev(time.Now(), state.EvSessionEnded, "t-1", nil),
		ev(time.Now(), state.EvTaskDone, "t-1", nil),
		ev(time.Now(), state.EvTaskUntracked, "t-1", nil),
		ev(time.Now(), state.EvTaskPaused, "t-1", nil),
	}
	for _, e := range must {
		if !f.Match(e) {
			t.Errorf("default stream dropped %s — silence would look like success", e.Type)
		}
	}
	// Chatter that is not a transition stays off the default stream.
	for _, typ := range []string{state.EvAttached, state.EvTaskCreated, state.EvSessionStarted} {
		if f.Match(ev(time.Now(), typ, "t-1", nil)) {
			t.Errorf("default stream included %s", typ)
		}
	}
}

func TestFilterTicketTypeSentinelSince(t *testing.T) {
	base := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	f := NewFilter([]string{"t-1"}, nil, nil, time.Time{})
	if f.Match(agentStatus("t-2", "idle", "done", "")) {
		t.Error("ticket filter let another ticket through")
	}
	f = NewFilter(nil, nil, []string{"done"}, time.Time{})
	if f.Match(agentStatus("t-1", "waiting", "question", "")) {
		t.Error("sentinel filter let a question through")
	}
	if f.Match(ev(base, state.EvNotification, "t-1", nil)) {
		t.Error("a sentinel filter is a question about agent transitions only")
	}
	if !f.Match(agentStatus("t-1", "idle", "done", "")) {
		t.Error("sentinel filter dropped its own sentinel")
	}
	f = NewFilter(nil, []string{TypeAll}, nil, time.Time{})
	if !f.Match(ev(base, state.EvAttached, "t-1", nil)) {
		t.Error("--type all must pass everything")
	}
	f = NewFilter(nil, nil, nil, base)
	if f.Match(ev(base.Add(-time.Second), state.EvSessionEnded, "t-1", nil)) {
		t.Error("--since let an older event through")
	}
	if !f.Match(ev(base.Add(time.Second), state.EvSessionEnded, "t-1", nil)) {
		t.Error("--since dropped a newer event")
	}
}

// --- Run ----------------------------------------------------------------

// The ticket's headline case: a task that is ALREADY done when the watch
// starts must not fire, and must fire exactly once when a new done lands.
func TestRunUntilIgnoresHistoryFiresOnceOnTheNewTransition(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, agentStatus("t-1", "idle", "done", "done an hour ago"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out bytes.Buffer
	done := make(chan bool, 1)
	go func() {
		fired, err := Run(ctx, Options{
			StateDir: dir, Tickets: []string{"t-1"}, Until: "done",
			Interval: time.Millisecond,
		}, &out)
		if err != nil {
			t.Error(err)
		}
		done <- fired
	}()

	// Give the follower a few beats on the stale log: it must stay quiet.
	time.Sleep(50 * time.Millisecond)
	if out.Len() != 0 {
		t.Fatalf("fired on history: %q", out.String())
	}
	appendLog(t, dir, agentStatus("t-1", "waiting", "question", "one thing first?"))
	appendLog(t, dir, agentStatus("t-1", "idle", "done", "shipped it\nsecond line"))

	select {
	case fired := <-done:
		if !fired {
			t.Fatal("Run returned without firing --until")
		}
	case <-ctx.Done():
		t.Fatal("Run never fired on the new done event")
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected the question then the done row, got %q", out.String())
	}
	if !strings.Contains(lines[1], "done") || !strings.Contains(lines[1], "shipped it") {
		t.Fatalf("done row malformed: %q", lines[1])
	}
	if strings.Contains(lines[1], "second line") {
		t.Fatalf("detail must be one line: %q", lines[1])
	}
}

// A worker whose pane is full of the kickoff prompt's three STATUS lines
// but which has emitted no sentinel produces NO output. This is the exact
// false positive that filed grove-205.
func TestRunIgnoresKickoffPromptText(t *testing.T) {
	dir := t.TempDir()
	// Everything a live-but-working worker actually appends. The pane holds
	// `STATUS: DONE — <one paragraph: …>` from the kickoff template; the
	// event log — the only thing watch reads — holds nothing of the sort.
	writeLog(t, dir,
		ev(time.Now(), state.EvTaskCreated, "t-1", map[string]string{"title": "STATUS: DONE — placeholder in a title"}),
		ev(time.Now(), state.EvSessionStarted, "t-1", map[string]string{"session_id": "s-1"}))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	var out bytes.Buffer
	fired, err := Run(ctx, Options{StateDir: dir, Replay: true, Tickets: []string{"t-1"},
		Until: "done", Interval: time.Millisecond}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if fired {
		t.Fatal("prompt text fired --until done")
	}
	if out.Len() != 0 {
		t.Fatalf("prompt text produced output: %q", out.String())
	}
}

func TestRunSkipsMalformedLineAndKeepsGoing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	good, _ := json.Marshal(agentStatus("t-1", "idle", "done", "after the scar"))
	body := []byte("{ not json at all\n")
	body = append(body, good...)
	body = append(body, '\n')
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var out bytes.Buffer
	fired, err := Run(ctx, Options{StateDir: dir, Replay: true, Until: "done", Interval: time.Millisecond}, &out)
	if err != nil {
		t.Fatalf("a torn line must not be fatal: %v", err)
	}
	if !fired {
		t.Fatal("folding stopped at the malformed line")
	}
}

func TestRunJSONPassesTheRecordThrough(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, agentStatus("t-1", "idle", "done", "ok"))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var out bytes.Buffer
	if _, err := Run(ctx, Options{StateDir: dir, Replay: true, JSON: true, Until: "done",
		Interval: time.Millisecond}, &out); err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(out.String())
	var back map[string]any
	if err := json.Unmarshal([]byte(line), &back); err != nil {
		t.Fatalf("--json line is not one JSON record: %q", line)
	}
	if back["type"] != state.EvAgentStatus || back["v"] != float64(1) {
		t.Fatalf("record lost its contract fields: %v", back)
	}
	if strings.Contains(line, "\n") {
		t.Fatalf("--json must be one record per line: %q", line)
	}
}

// A buffered writer must still see each event as it lands: the Monitor tool
// turns one stdout LINE into one notification.
func TestRunFlushesPerLine(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, agentStatus("t-1", "idle", "done", "ok"))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var sink bytes.Buffer
	w := &countingFlusher{w: &sink}
	if _, err := Run(ctx, Options{StateDir: dir, Replay: true, Until: "done", Interval: time.Millisecond}, w); err != nil {
		t.Fatal(err)
	}
	if w.flushes != 1 {
		t.Fatalf("expected one flush per emitted line, got %d", w.flushes)
	}
}

type countingFlusher struct {
	w       *bytes.Buffer
	flushes int
}

func (c *countingFlusher) Write(p []byte) (int, error) { return c.w.Write(p) }
func (c *countingFlusher) Flush() error                { c.flushes++; return nil }

// --- rendering & validation --------------------------------------------

func TestRowLabels(t *testing.T) {
	at := time.Date(2026, 8, 29, 14, 5, 0, 0, time.Local)
	cases := []struct {
		ev   state.Event
		want string
	}{
		{ev(at, state.EvAgentStatus, "t-1", map[string]string{"status": "idle", "sentinel": "done", "message": "shipped"}), "done"},
		{ev(at, state.EvAgentStatus, "t-1", map[string]string{"status": "waiting", "sentinel": "question", "question": "which?"}), "question"},
		{ev(at, state.EvAgentStatus, "t-1", map[string]string{"status": "idle", "sentinel": "none", "message": "wandered off"}), "idle"},
		{ev(at, state.EvSessionEnded, "t-1", nil), "session_ended"},
	}
	for _, c := range cases {
		if got := Label(c.ev); got != c.want {
			t.Errorf("Label(%s/%s) = %q, want %q", c.ev.Type, c.ev.Data["sentinel"], got, c.want)
		}
		row := Row(c.ev)
		if !strings.HasPrefix(row, "14:05  ") || !strings.Contains(row, "t-1") || !strings.Contains(row, c.want) {
			t.Errorf("Row = %q", row)
		}
	}
	if got := Row(ev(at, state.EvWorkspaceParked, "", nil)); !strings.Contains(got, "-") {
		t.Errorf("workspace-scoped row = %q", got)
	}
}

func TestRowDetailIsRuneSafe(t *testing.T) {
	long := strings.Repeat("é", detailCap+40)
	row := Row(ev(time.Now(), state.EvAgentStatus, "t-1", map[string]string{"sentinel": "done", "message": long}))
	if !strings.HasSuffix(row, "…") {
		t.Fatalf("long detail not truncated: %q", row)
	}
	if !utf8Valid(row) {
		t.Fatal("truncation cut a codepoint")
	}
}

func utf8Valid(s string) bool {
	for _, r := range s {
		if r == 0xFFFD {
			return false
		}
	}
	return true
}

func TestValidateRejectsTypos(t *testing.T) {
	if err := Validate([]string{"agent_status", TypeAll}, []string{"done"}, "done"); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	if err := Validate([]string{"agent_stats"}, nil, ""); err == nil {
		t.Error("a mistyped --type must be an error, not an empty stream")
	}
	if err := Validate(nil, []string{"finished"}, ""); err == nil {
		t.Error("a mistyped --sentinel must be an error")
	}
	if err := Validate(nil, nil, "DONE"); err == nil {
		t.Error("a mistyped --until must be an error")
	}
}

func TestSplit(t *testing.T) {
	got := Split(" done , question ,, ")
	if len(got) != 2 || got[0] != "done" || got[1] != "question" {
		t.Fatalf("Split = %q", got)
	}
	if Split("") != nil {
		t.Fatal("empty value must yield no constraint")
	}
}
