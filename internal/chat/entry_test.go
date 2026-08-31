package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fixtureEntries is what testdata/transcript.jsonl must project to, in
// order. Every line of the fixture is here for a reason: the bookkeeping
// types, the isMeta injection, the sidechain subagent, the empty thinking
// block and the unparseable line are all present precisely because they
// must produce NOTHING, and the seq column is the proof — a projection that
// leaks one of them shifts every later seq and breaks `--since`.
var fixtureEntries = []Entry{
	{Seq: 1, Role: "user", Kind: EntryText, Text: "triage the artgen backlog"},
	{Seq: 2, Role: "assistant", Kind: EntryThinking, Text: "three open tickets, one is stale"},
	{Seq: 3, Role: "assistant", Kind: EntryText, Text: "Looking at the backlog now."},
	{Seq: 4, Role: "assistant", Kind: EntryToolUse, Tool: "Bash", Text: `{"command":"gv ls --json","description":"list tasks"}`},
	{Seq: 5, Role: "user", Kind: EntryToolResult, Tool: "Bash", Text: "grove-90  working\ngrove-91  review"},
	{Seq: 6, Role: "assistant", Kind: EntryText, Text: "Two tickets are live; grove-91 is waiting on review."},
	{Seq: 7, Role: "user", Kind: EntryToolResult, Tool: "", Text: "an unpaired result"},
	{Seq: 8, Role: "assistant", Kind: EntryText, Text: "no timestamp on this one"},
}

func TestProjectorFixture(t *testing.T) {
	got := projectFile(t, "testdata/transcript.jsonl")
	if len(got) != len(fixtureEntries) {
		t.Fatalf("got %d entries, want %d:\n%s", len(got), len(fixtureEntries), dump(got))
	}
	for i, want := range fixtureEntries {
		g := got[i]
		if g.Seq != want.Seq || g.Role != want.Role || g.Kind != want.Kind || g.Text != want.Text || g.Tool != want.Tool {
			t.Errorf("entry %d = %+v, want %+v", i, g, want)
		}
	}
}

// The timestamp is a pointer so an entry without one emits JSON null rather
// than the year 1 dressed up as a real time.
func TestProjectorTimestamps(t *testing.T) {
	got := projectFile(t, "testdata/transcript.jsonl")
	if got[0].Ts == nil {
		t.Fatal("entry 1 lost its timestamp")
	}
	if want := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC); !got[0].Ts.Equal(want) {
		t.Errorf("entry 1 ts = %v, want %v", got[0].Ts, want)
	}
	last := got[len(got)-1]
	if last.Ts != nil {
		t.Errorf("a line with no timestamp must project ts nil, got %v", last.Ts)
	}
	b, err := json.Marshal(last)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"ts":null`) {
		t.Errorf("a timestampless entry must marshal ts null, got %s", b)
	}
}

func TestProjectorLines(t *testing.T) {
	cases := []struct {
		name string
		line string
		want []Entry
	}{
		{"empty", "", nil},
		{"blank line", "   ", nil},
		{"not json", "hello", nil},
		{"half a line (writer mid-append)", `{"type":"assistant","message":{"role":"assi`, nil},
		{"bookkeeping type", `{"type":"mode","mode":"normal"}`, nil},
		{"summary type", `{"type":"summary","summary":"a chat"}`, nil},
		{"meta user line", `{"type":"user","isMeta":true,"message":{"role":"user","content":"boilerplate"}}`, nil},
		{"sidechain assistant", `{"type":"user","isSidechain":true,"message":{"role":"user","content":"subagent"}}`, nil},
		{"empty content", `{"type":"user","message":{"role":"user","content":""}}`, nil},
		{
			"plain string content",
			`{"type":"user","message":{"role":"user","content":"ship it"}}`,
			[]Entry{{Seq: 1, Role: "user", Kind: EntryText, Text: "ship it"}},
		},
		{
			"tool_use with no input keeps its row",
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"ListAgents"}]}}`,
			[]Entry{{Seq: 1, Role: "assistant", Kind: EntryToolUse, Tool: "ListAgents", Text: ""}},
		},
		{
			"redacted thinking is dropped, not emitted blank",
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"","signature":"x"}]}}`,
			nil,
		},
		{
			"role falls back to the line type",
			`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`,
			[]Entry{{Seq: 1, Role: "assistant", Kind: EntryText, Text: "hi"}},
		},
		{
			"unknown block types are skipped",
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"server_tool_use","id":"s1"},{"type":"text","text":"after"}]}}`,
			[]Entry{{Seq: 1, Role: "assistant", Kind: EntryText, Text: "after"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NewProjector().Line([]byte(tc.line))
			if len(got) != len(tc.want) {
				t.Fatalf("got %d entries %v, want %d", len(got), got, len(tc.want))
			}
			for i, want := range tc.want {
				if got[i].Seq != want.Seq || got[i].Role != want.Role || got[i].Kind != want.Kind || got[i].Text != want.Text || got[i].Tool != want.Tool {
					t.Errorf("entry %d = %+v, want %+v", i, got[i], want)
				}
			}
		})
	}
}

// A tool_result names only an opaque toolu_… id; the tool NAME comes from
// the tool_use it answers, which the projector remembers across lines.
func TestProjectorPairsToolResultsToNames(t *testing.T) {
	p := NewProjector()
	p.Line([]byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tA","name":"Read","input":{"file":"x"}},{"type":"tool_use","id":"tB","name":"Grep","input":{}}]}}`))
	got := p.Line([]byte(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tB","content":"3 matches"},{"type":"tool_result","tool_use_id":"tA","content":"file body"}]}}`))
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	if got[0].Tool != "Grep" || got[1].Tool != "Read" {
		t.Errorf("results paired to %q/%q, want Grep/Read", got[0].Tool, got[1].Tool)
	}
	if got[0].Seq != 3 || got[1].Seq != 4 {
		t.Errorf("seq = %d,%d — must keep counting past the tool_use blocks", got[0].Seq, got[1].Seq)
	}
}

// A tool_result can run to tens of KB. It is emitted whole: the design's
// tool row is "collapsed to one line, EXPANDABLE", and a client has no
// second place to get the rest from.
func TestProjectorDoesNotTruncateToolResults(t *testing.T) {
	body := strings.Repeat("x", 200_000)
	p := NewProjector()
	p.Line([]byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}`))
	got := p.Line([]byte(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"` + body + `"}]}}`))
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
	if len(got[0].Text) != len(body) {
		t.Errorf("tool_result text = %d bytes, want %d (no truncation)", len(got[0].Text), len(body))
	}
}

// --- Tail ---

func TestTailReproducesTheTranscriptInOrder(t *testing.T) {
	var buf bytes.Buffer
	if err := Tail(context.Background(), "testdata/transcript.jsonl", TailOptions{}, &buf); err != nil {
		t.Fatal(err)
	}
	got := decode(t, buf.String())
	if len(got) != len(fixtureEntries) {
		t.Fatalf("tail emitted %d entries, want %d:\n%s", len(got), len(fixtureEntries), buf.String())
	}
	for i, want := range fixtureEntries {
		if got[i].Seq != want.Seq || got[i].Kind != want.Kind || got[i].Text != want.Text {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want)
		}
	}
}

func TestTailSinceResumes(t *testing.T) {
	for _, since := range []int{0, 1, 5, 8, 99} {
		var buf bytes.Buffer
		if err := Tail(context.Background(), "testdata/transcript.jsonl", TailOptions{Since: since}, &buf); err != nil {
			t.Fatal(err)
		}
		got := decode(t, buf.String())
		want := len(fixtureEntries) - since
		if want < 0 {
			want = 0
		}
		if len(got) != want {
			t.Fatalf("--since %d emitted %d entries, want %d", since, len(got), want)
		}
		if want > 0 && got[0].Seq != since+1 {
			t.Errorf("--since %d resumed at seq %d, want %d", since, got[0].Seq, since+1)
		}
	}
}

func TestTailMissingFileIsAnErrorWithoutFollow(t *testing.T) {
	err := Tail(context.Background(), filepath.Join(t.TempDir(), "nope.jsonl"), TailOptions{}, &bytes.Buffer{})
	if !os.IsNotExist(err) {
		t.Fatalf("err = %v, want a not-exist error", err)
	}
}

// The acceptance bar: an appended entry shows up within ~1s. Also the
// partial-line rule — a line without its terminator is the writer
// mid-append and must not be projected until it is whole.
func TestTailFollowEmitsAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"user","message":{"role":"user","content":"first"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := &syncBuf{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Tail(ctx, path, TailOptions{Follow: true, Poll: 20 * time.Millisecond}, w) }()

	waitFor(t, w, 1, "the existing entry")
	appendLine(t, path, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"second"}]}}`+"\n")
	waitFor(t, w, 2, "the appended entry")

	// Half a line: nothing may be emitted until the writer finishes it.
	appendLine(t, path, `{"type":"user","message":{"role":"user","con`)
	time.Sleep(100 * time.Millisecond)
	if got := decode(t, w.String()); len(got) != 2 {
		t.Fatalf("a partial line was projected: %d entries\n%s", len(got), w.String())
	}
	appendLine(t, path, `tent":"third"}}`+"\n")
	waitFor(t, w, 3, "the completed line")

	got := decode(t, w.String())
	for i, want := range []string{"first", "second", "third"} {
		if got[i].Text != want || got[i].Seq != i+1 {
			t.Errorf("entry %d = %+v, want seq %d %q", i, got[i], i+1, want)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("follow returned %v", err)
	}
}

// A --follow on a chat that has not written its first line yet WAITS (the
// chat is booting) rather than erroring — the streaming twin of `gv chat
// ls` reporting session_id: null.
func TestTailFollowWaitsForAMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "later.jsonl")
	w := &syncBuf{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Tail(ctx, path, TailOptions{Follow: true, Poll: 20 * time.Millisecond}, w) }()
	time.Sleep(60 * time.Millisecond)
	if err := os.WriteFile(path, []byte(`{"type":"user","message":{"role":"user","content":"born late"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, w, 1, "the first line of a chat that booted late")
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("follow returned %v", err)
	}
}

// --- helpers ---

type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitFor(t *testing.T, w *syncBuf, n int, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(decode(t, w.String())) >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s (%d entries)\n%s", what, n, w.String())
}

func appendLine(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func decode(t *testing.T, out string) []Entry {
	t.Helper()
	var entries []Entry
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("tail emitted a line that is not JSON: %q (%v)", line, err)
		}
		entries = append(entries, e)
	}
	return entries
}

func projectFile(t *testing.T, path string) []Entry {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p := NewProjector()
	var out []Entry
	for _, line := range strings.Split(string(b), "\n") {
		out = append(out, p.Line([]byte(line))...)
	}
	return out
}

func dump(entries []Entry) string {
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e.Kind + " " + e.Role + " " + e.Tool + " " + strings.SplitN(e.Text, "\n", 2)[0] + "\n")
	}
	return b.String()
}
