package chatrail

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite golden files")

var t0 = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func chips(n int) []Chip {
	turns := []string{"running", "waiting", "idle", "errored", "stopped", ""}
	titles := []string{"fix the flaky auth test in the gateway", "grove-486 rail", "refactor billing", "", "ünïcödé 日本語 label that is long", "plan q4 roadmap grove-12 notes"}
	var out []Chip
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("chat-%d", i)
		if i == 0 {
			name = "cockpit·1"
		}
		out = append(out, Chip{
			Name: name, Turn: turns[i%len(turns)], Title: titles[i%len(titles)],
			At:      t0.Add(-time.Duration(i*i*7+1) * time.Minute),
			Session: fmt.Sprintf("grove-chat-x-%d", i), Shown: i == 0,
		})
	}
	return out
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSuffix(string(want), "\n") != got {
		t.Errorf("%s mismatch:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestRenderGoldens(t *testing.T) {
	for _, n := range []int{0, 1, 4, 9} {
		for _, w := range []int{80, 160} {
			t.Run(fmt.Sprintf("%dboxes-%d", n, w), func(t *testing.T) {
				out := Render(chips(n), w, t0)
				for _, l := range strings.Split(out, "\n") {
					if cells(l) > w {
						t.Errorf("line %q is %d cells, over %d", l, cells(l), w)
					}
				}
				golden(t, fmt.Sprintf("boxes%d-w%d", n, w), out)
			})
		}
	}
}

func TestGlyphPerTurn(t *testing.T) {
	want := map[string]string{"running": "●", "waiting": "?", "idle": "✓", "errored": "!", "stopped": "·", "": "·", "unknown": "·"}
	for turn, g := range want {
		if got := Glyph(turn); got != g {
			t.Errorf("Glyph(%q) = %q, want %q", turn, got, g)
		}
	}
}

// grove-510: Claude's session title wins, then a ticket id in the label,
// then the label (the first prompt). The last message is never a source.
func TestTitleFallbacks(t *testing.T) {
	for _, c := range []struct{ pane, label, want string }{
		{"Hidden chat panes title chips", "please look at grove-486 today", "Hidden chat panes title chips"},
		{"", "please look at grove-486 today", "grove-486"},
		{"", "no  ticket\nhere", "no ticket here"},
		{"", "", ""},
	} {
		if got := Title(c.pane, c.label); got != c.want {
			t.Errorf("Title(%q, %q) = %q, want %q", c.pane, c.label, got, c.want)
		}
	}
}

func TestPaneTitleStripsGlyph(t *testing.T) {
	for raw, want := range map[string]string{
		"✳ Hidden chat panes title chips": "Hidden chat panes title chips",
		"⠐ Fix the rail":                  "Fix the rail",
		"✳ Claude Code":                   "", // the placeholder, not a title
		"Deans-MacBook-Pro.local":         "", // tmux's default: the hostname
		"zsh ~/git":                       "", // a shell's, no status glyph
		"✳":                               "",
		"":                                "",
	} {
		if got := PaneTitle(raw); got != want {
			t.Errorf("PaneTitle(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestOverflowReadsPlusNMore(t *testing.T) {
	out := Render(chips(9), 80, t0) // 80/16 = 5 slots → 4 boxes + "+5 more"
	if !strings.Contains(out, "+5 more") {
		t.Errorf("want +5 more in:\n%s", out)
	}
}

// grove-510: the shown chat keeps its box — highlighted, not omitted — and
// the rail's chip count is the chat count.
func TestLayoutKeepsShownChip(t *testing.T) {
	for _, c := range []struct {
		name      string
		in        []Chip
		width     int
		wantBoxes []string
		wantMore  int
	}{
		{"shown first", chips(3), 80, []string{"cockpit·1", "chat-1", "chat-2"}, 0},
		{"shown last", func() []Chip {
			cs := chips(3)
			cs[0].Shown, cs[2].Shown = false, true
			return cs
		}(), 80, []string{"cockpit·1", "chat-1", "chat-2"}, 0},
		{"shown past the fold takes the last slot", func() []Chip {
			cs := chips(9)
			cs[0].Shown, cs[7].Shown = false, true
			return cs
		}(), 80, []string{"cockpit·1", "chat-1", "chat-2", "chat-7"}, 5},
		{"only the shown chat", chips(1), 80, []string{"cockpit·1"}, 0},
		{"none", nil, 80, nil, 0},
	} {
		boxes, more, _ := layout(c.in, c.width)
		var names []string
		for _, b := range boxes {
			names = append(names, b.Name)
		}
		if strings.Join(names, ",") != strings.Join(c.wantBoxes, ",") || more != c.wantMore {
			t.Errorf("%s: boxes %v +%d, want %v +%d", c.name, names, more, c.wantBoxes, c.wantMore)
		}
	}
	out := Render(chips(2), 80, t0)
	if !strings.Contains(out, "▣ cockpit·1") || !strings.Contains(out, "\x1b[1m") {
		t.Errorf("shown chat must be drawn, highlighted:\n%q", out)
	}
	if !strings.Contains(out, "○ chat-1") {
		t.Errorf("off-screen chat must be drawn with ○:\n%q", out)
	}
}

func TestEncodeParseRoundTrip(t *testing.T) {
	in := chips(4)
	in[3].Session = "" // a remote chip
	in[2].At = time.Time{}
	got, wrote, ok := Parse(Encode(in, t0))
	if !ok || !wrote.Equal(t0) || len(got) != 4 {
		t.Fatalf("Parse = %v %v %v", got, wrote, ok)
	}
	for i := range in {
		if !got[i].At.Equal(in[i].At) {
			t.Errorf("chip %d at %v, want %v", i, got[i].At, in[i].At)
		}
		got[i].At = in[i].At
		if got[i] != in[i] {
			t.Errorf("chip %d = %+v, want %+v", i, got[i], in[i])
		}
	}
	if _, _, ok := Parse("garbage"); ok {
		t.Error("Parse accepted garbage")
	}
	if _, _, ok := Parse("v1\t1\t0\n1\tidle\t0\tx\ts"); ok {
		t.Error("Parse accepted a v1 payload")
	}
}

type fakeRun struct{ calls [][]string }

func (f *fakeRun) run(args ...string) (string, error) {
	f.calls = append(f.calls, args)
	return "", nil
}

func TestWriterWritesOnlyOnChange(t *testing.T) {
	f := &fakeRun{}
	w := &Writer{Run: f.run}
	in := chips(2)
	for i := 0; i < 3; i++ { // three identical ticks within the heartbeat
		w.Write("grove-x", in, t0.Add(time.Duration(i)*time.Second), false)
	}
	if len(f.calls) != 1 || f.calls[0][0] != "set-option" {
		t.Fatalf("calls = %v, want exactly one set-option", f.calls)
	}
	w.Write("grove-x", chips(3), t0.Add(4*time.Second), false)
	if len(f.calls) != 2 {
		t.Errorf("a changed payload must write: %d calls", len(f.calls))
	}
	w.Write("grove-x", chips(3), t0.Add(4*time.Second+Heartbeat), false)
	if len(f.calls) != 3 {
		t.Errorf("heartbeat must refresh an unchanged payload: %d calls", len(f.calls))
	}
}

// grove-510: a move writes even an unchanged payload — another process may
// have written the option since this Writer last did.
func TestWriterForceWritesUnchanged(t *testing.T) {
	f := &fakeRun{}
	w := &Writer{Run: f.run}
	for i := 0; i < 6; i++ {
		w.Write("grove-x", chips(2), t0.Add(time.Duration(i)*time.Second), true)
	}
	if len(f.calls) != 6 {
		t.Errorf("every forced write must reach tmux: %d calls", len(f.calls))
	}
}

func TestViewerReadsOnlyShowOptions(t *testing.T) {
	var calls [][]string
	payload := Encode(chips(2), t0)
	var buf bytes.Buffer
	now := t0
	v := &Viewer{
		Session: "grove-x", Out: &buf, Width: func() int { return 80 }, Now: func() time.Time { return now },
		Run: func(args ...string) (string, error) { calls = append(calls, args); return payload, nil },
	}
	for i := 0; i < 3; i++ {
		if err := v.Step(); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range calls {
		if c[0] != "show-options" {
			t.Errorf("rail ran %v; only show-options is allowed", c)
		}
	}
	if len(calls) != 3 {
		t.Errorf("want one command per tick, got %d", len(calls))
	}
	first := buf.Len()
	if first == 0 || !strings.Contains(buf.String(), "grove-486") {
		t.Errorf("nothing drawn: %q", buf.String())
	}
	if err := v.Step(); err != nil || buf.Len() != first {
		t.Error("an unchanged picture must not redraw")
	}
	now = t0.Add(StaleAfter + time.Second)
	buf.Reset()
	_ = v.Step()
	if !strings.Contains(buf.String(), "· stale") {
		t.Errorf("want stale mark after %v: %q", StaleAfter, buf.String())
	}
}

// Hit and Render share layout(): for every width and box count, each column
// of every drawn off-screen box answers that box, the shown box and each
// separator answer nothing, and the "+N more" slot and the space past the
// boxes answer nothing.
func TestHitTable(t *testing.T) {
	for _, w := range []int{80, 160} {
		for _, n := range []int{1, 4, 9} {
			cs := chips(n)
			boxes, more, cw := layout(cs, w)
			for i, b := range boxes {
				for x := i * (cw + 1); x < i*(cw+1)+cw; x++ {
					got, ok := Hit(cs, w, x)
					if b.Shown {
						if ok {
							t.Errorf("w%d n%d x%d: the shown chip hit %q", w, n, x, got)
						}
						continue
					}
					if !ok || got != b.Session {
						t.Errorf("w%d n%d x%d: got %q,%v want %q", w, n, x, got, ok, b.Session)
					}
				}
				if got, ok := Hit(cs, w, i*(cw+1)+cw); ok {
					t.Errorf("w%d n%d: separator after box %d hit %q", w, n, i, got)
				}
			}
			if more > 0 {
				if got, ok := Hit(cs, w, len(boxes)*(cw+1)+2); ok {
					t.Errorf("w%d n%d: overflow box hit %q", w, n, got)
				}
			}
			if _, ok := Hit(cs, w, w+50); ok {
				t.Errorf("w%d n%d: past the end hit", w, n)
			}
			if _, ok := Hit(cs, w, -1); ok {
				t.Error("negative column hit")
			}
		}
	}
	if _, ok := Hit(nil, 80, 3); ok {
		t.Error("empty rail hit")
	}
	remote := chips(2)
	remote[1].Session = ""
	if _, ok := Hit(remote, 80, 41); ok {
		t.Error("a chip without a session (remote) must not be clickable")
	}
	if b, _, _ := layout(chips(9), 80); len(b) != 4 {
		t.Errorf("80 cells / 9 chats: want 4 boxes + overflow, got %d", len(b))
	}
}

type clickFixture struct {
	v     *Viewer
	calls [][]string
	focus []string
	buf   *bytes.Buffer
	opt   *string
}

func newClickFixture(cs []Chip) *clickFixture {
	f := &clickFixture{buf: &bytes.Buffer{}}
	payload := Encode(cs, t0)
	f.opt = &payload
	f.v = &Viewer{
		Session: "grove-x", Out: f.buf, Width: func() int { return 80 }, Now: func() time.Time { return t0 },
		Run:   func(args ...string) (string, error) { f.calls = append(f.calls, args); return *f.opt, nil },
		Focus: func(s string) error { f.focus = append(f.focus, s); return nil },
	}
	_ = f.v.Step()
	return f
}

func TestClickFocusesBoxAndIgnoresTheRest(t *testing.T) {
	f := newClickFixture(chips(3))
	_, _, cw := layout(chips(3), 80)
	_ = f.v.Click(2*(cw+1)+2, 0) // third box, first line
	if len(f.focus) != 1 || f.focus[0] != "grove-chat-x-2" {
		t.Fatalf("want focus on grove-chat-x-2, got %v", f.focus)
	}
	_ = f.v.Click(2, 0)  // the shown chat's own box: a no-op
	_ = f.v.Click(cw, 0) // separator
	_ = f.v.Click(79, 0) // past the boxes
	_ = f.v.Click(2, 2)  // below the rail
	if len(f.focus) != 1 {
		t.Errorf("shown box / separator / empty space / other line must do nothing, got %v", f.focus)
	}
	g := newClickFixture(chips(9))
	boxes, _, gcw := layout(chips(9), 80)
	_ = g.v.Click(len(boxes)*(gcw+1)+1, 0)
	if len(g.focus) != 0 {
		t.Errorf("+N more must do nothing, got %v", g.focus)
	}
}

func TestClickInvertsBoxBeforeSwap(t *testing.T) {
	f := newClickFixture(chips(2))
	_, _, cw := layout(chips(2), 80)
	f.buf.Reset()
	_ = f.v.Click(cw+2, 0)
	if !strings.Contains(f.buf.String(), "\x1b[7m") {
		t.Errorf("clicked box was not inverted: %q", f.buf.String())
	}
}

func TestClickStaleBoxMutatesNothing(t *testing.T) {
	f := newClickFixture(chips(2))
	*f.opt = Encode(chips(2)[:1], t0) // chat-1 closed since the draw
	f.calls = nil
	f.buf.Reset()
	_, _, cw := layout(chips(2), 80)
	_ = f.v.Click(cw+1+1, 0)
	if len(f.focus) != 0 {
		t.Errorf("stale box ran focus: %v", f.focus)
	}
	for _, c := range f.calls {
		if c[0] != "show-options" {
			t.Errorf("stale click ran %v", c)
		}
	}
	if !strings.Contains(f.buf.String(), "gone") {
		t.Errorf("no gone flash: %q", f.buf.String())
	}
	_ = f.v.Step()
	if strings.Contains(f.v.prev, "gone") {
		t.Error("the flash must last one tick")
	}
}

func TestReadClicks(t *testing.T) {
	in := "x\x1b[<0;10;1M" + // press: ignored
		"\x1b[<0;10;1m" + // release: click (9,0)
		"\x1b[<64;5;1M\x1b[<2;5;1m\x1b[<4;5;1m" + // wheel, right, shift-left: ignored
		"\x1b[A\x1b\x1b[<0;3;2m" // arrow, stray esc, then click (2,1)
	var got [][2]int
	ReadClicks(strings.NewReader(in), func(x, y int) { got = append(got, [2]int{x, y}) })
	if len(got) != 2 || got[0] != [2]int{9, 0} || got[1] != [2]int{2, 1} {
		t.Errorf("got %v", got)
	}
}

// Overlapping refresh and focus commands share one Writer; run under -race.
func TestWriterConcurrentWrites(t *testing.T) {
	w := &Writer{Run: func(args ...string) (string, error) { return "", nil }}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				w.Write("grove-x", chips(1+(g+i)%3), t0.Add(time.Duration(i)*time.Second), i%2 == 0)
			}
		}(g)
	}
	wg.Wait()
}

// drawSink hands each viewer write to the test as it happens.
type drawSink chan string

func (d drawSink) Write(p []byte) (int, error) { d <- string(p); return len(p), nil }

// grove-521: the rail redraws when a writer wakes it, not on its next tick —
// and without a wake or a tick it reads nothing at all.
func TestViewerLoopRedrawsOnWake(t *testing.T) {
	var mu sync.Mutex
	payload := Encode(chips(2), t0)
	set := func(s string) { mu.Lock(); payload = s; mu.Unlock() }
	sink := make(drawSink, 8)
	v := &Viewer{
		Session: "grove-x", Out: sink, Width: func() int { return 80 }, Now: func() time.Time { return t0 },
		Run: func(args ...string) (string, error) { mu.Lock(); defer mu.Unlock(); return payload, nil },
	}
	tick := make(chan time.Time)
	wake := make(chan os.Signal, 1)
	done := make(chan struct{})
	ended := make(chan struct{})
	go func() { v.Loop(tick, wake, nil, done); close(ended) }()
	next := func(what string) string {
		t.Helper()
		select {
		case s := <-sink:
			return s
		case <-time.After(time.Second):
			t.Fatalf("no redraw after %s", what)
		}
		return ""
	}
	if first := next("start"); strings.Contains(first, "chat-2") {
		t.Fatalf("first picture already has the third chip: %q", first)
	}

	set(Encode(chips(3), t0))
	select {
	case s := <-sink:
		t.Fatalf("redrew with no wake and no tick: %q", s)
	case <-time.After(50 * time.Millisecond):
	}
	wake <- os.Interrupt // any signal value: the channel is the wake
	if s := next("wake"); !strings.Contains(s, "chat-2") {
		t.Errorf("the wake's redraw lacks the new chip: %q", s)
	}

	set(Encode(chips(4), t0))
	tick <- t0 // the heartbeat still redraws on its own
	if s := next("tick"); !strings.Contains(s, "chat-3") {
		t.Errorf("the tick's redraw lacks the new chip: %q", s)
	}
	close(done)
	<-ended
}
