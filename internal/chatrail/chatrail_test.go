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

	"github.com/JollyGrin/grove/internal/chat"
)

var update = flag.Bool("update", false, "rewrite golden files")

var t0 = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func rows(n int) []chat.Row {
	turns := []string{"running", "waiting", "idle", "errored", "stopped", ""}
	labels := []string{"fix the flaky auth test in the gateway", "grove-486 rail", "refactor billing", "", "ünïcödé 日本語 label that is long", "plan q4 roadmap grove-12 notes"}
	var out []chat.Row
	for i := 0; i < n; i++ {
		out = append(out, chat.Row{
			N: i + 1, Turn: turns[i%len(turns)], Label: labels[i%len(labels)],
			LastActive: t0.Add(-time.Duration(i*i*7+1) * time.Minute),
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
				out := Render(rows(n), 0, w, t0)
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

func TestNamePrefersTicket(t *testing.T) {
	if got := Name(chat.Row{Label: "please look at grove-486 today"}); got != "grove-486" {
		t.Errorf("Name = %q, want the ticket", got)
	}
	if got := Name(chat.Row{Label: "no ticket here"}); got != "no ticket here" {
		t.Errorf("Name = %q, want the label", got)
	}
}

func TestOverflowReadsPlusNMore(t *testing.T) {
	out := Render(rows(9), 0, 80, t0) // 80/16 = 5 slots → 4 boxes + "+5 more"
	if !strings.Contains(out, "+5 more") {
		t.Errorf("want +5 more in:\n%s", out)
	}
}

func TestShownIsSkipped(t *testing.T) {
	out := Render(rows(2), 1, 80, t0)
	if strings.Contains(out, "● 1") {
		t.Errorf("shown chat got a box:\n%s", out)
	}
}

func TestEncodeParseRoundTrip(t *testing.T) {
	in := rows(4)
	got, shown, wrote, ok := Parse(Encode(in, 3, t0))
	if !ok || shown != 3 || !wrote.Equal(t0) || len(got) != 4 {
		t.Fatalf("Parse = %v %d %v %v", got, shown, wrote, ok)
	}
	if got[0].N != 1 || got[0].Turn != "running" || got[1].Label != "grove-486" {
		t.Errorf("rows lost: %+v", got)
	}
	if _, _, _, ok := Parse("garbage"); ok {
		t.Error("Parse accepted garbage")
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
	in := rows(2)
	for i := 0; i < 3; i++ { // three identical ticks within the heartbeat
		w.Write("grove-x", in, 0, t0.Add(time.Duration(i)*time.Second))
	}
	if len(f.calls) != 1 || f.calls[0][0] != "set-option" {
		t.Fatalf("calls = %v, want exactly one set-option", f.calls)
	}
	w.Write("grove-x", rows(3), 0, t0.Add(4*time.Second))
	if len(f.calls) != 2 {
		t.Errorf("a changed payload must write: %d calls", len(f.calls))
	}
	w.Write("grove-x", rows(3), 0, t0.Add(4*time.Second+Heartbeat))
	if len(f.calls) != 3 {
		t.Errorf("heartbeat must refresh an unchanged payload: %d calls", len(f.calls))
	}
}

func TestViewerReadsOnlyShowOptions(t *testing.T) {
	var calls [][]string
	payload := Encode(rows(2), 0, t0)
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

func sessRows(n int) []chat.Row {
	rs := rows(n)
	for i := range rs {
		rs[i].Session = fmt.Sprintf("grove-chat-x-%d", rs[i].N)
	}
	return rs
}

// Hit and Render share layout(): for every width and box count, each column
// of every drawn box answers that box, each separator answers nothing, and the
// "+N more" slot and the space past the boxes answer nothing.
func TestHitTable(t *testing.T) {
	for _, w := range []int{80, 160} {
		for _, n := range []int{1, 4, 9} {
			rs := sessRows(n)
			boxes, more, cw := layout(rs, 0, w)
			for i, b := range boxes {
				for x := i * (cw + 1); x < i*(cw+1)+cw; x++ {
					if got, ok := Hit(rs, 0, w, x); !ok || got != b.Session {
						t.Errorf("w%d n%d x%d: got %q,%v want %q", w, n, x, got, ok, b.Session)
					}
				}
				if got, ok := Hit(rs, 0, w, i*(cw+1)+cw); ok {
					t.Errorf("w%d n%d: separator after box %d hit %q", w, n, i, got)
				}
			}
			if more > 0 {
				if got, ok := Hit(rs, 0, w, len(boxes)*(cw+1)+2); ok {
					t.Errorf("w%d n%d: overflow box hit %q", w, n, got)
				}
			}
			if _, ok := Hit(rs, 0, w, w+50); ok {
				t.Errorf("w%d n%d: past the end hit", w, n)
			}
			if _, ok := Hit(rs, 0, w, -1); ok {
				t.Error("negative column hit")
			}
		}
	}
	if _, ok := Hit(nil, 0, 80, 3); ok {
		t.Error("empty rail hit")
	}
	if n := len(func() []chat.Row { b, _, _ := layout(sessRows(9), 0, 80); return b }()); n != 4 {
		t.Errorf("80 cells / 9 chats: want 4 boxes + overflow, got %d", n)
	}
}

func TestWireCarriesSession(t *testing.T) {
	got, _, _, ok := Parse(Encode(sessRows(2), 0, t0))
	if !ok || got[1].Session != "grove-chat-x-2" {
		t.Errorf("session lost in the wire format: %+v", got)
	}
}

type clickFixture struct {
	v     *Viewer
	calls [][]string
	focus []string
	buf   *bytes.Buffer
	opt   *string
}

func newClickFixture(rs []chat.Row, shown int) *clickFixture {
	f := &clickFixture{buf: &bytes.Buffer{}}
	payload := Encode(rs, shown, t0)
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
	f := newClickFixture(sessRows(3), 0)
	_, _, cw := layout(sessRows(3), 0, 80)
	_ = f.v.Click(cw+1+2, 0) // second box, first line
	if len(f.focus) != 1 || f.focus[0] != "grove-chat-x-2" {
		t.Fatalf("want focus on grove-chat-x-2, got %v", f.focus)
	}
	_ = f.v.Click(cw, 0) // separator
	_ = f.v.Click(79, 0) // past the boxes
	_ = f.v.Click(2, 2)  // below the rail
	if len(f.focus) != 1 {
		t.Errorf("separator / empty space / other line must do nothing, got %v", f.focus)
	}
	g := newClickFixture(sessRows(9), 0)
	boxes, _, gcw := layout(sessRows(9), 0, 80)
	_ = g.v.Click(len(boxes)*(gcw+1)+1, 0)
	if len(g.focus) != 0 {
		t.Errorf("+N more must do nothing, got %v", g.focus)
	}
}

func TestClickInvertsBoxBeforeSwap(t *testing.T) {
	f := newClickFixture(sessRows(2), 0)
	f.buf.Reset()
	_ = f.v.Click(1, 0)
	if !strings.Contains(f.buf.String(), "\x1b[7m") {
		t.Errorf("clicked box was not inverted: %q", f.buf.String())
	}
}

func TestClickStaleBoxMutatesNothing(t *testing.T) {
	f := newClickFixture(sessRows(2), 0)
	*f.opt = Encode(sessRows(2)[:1], 0, t0) // chat 2 closed since the draw
	f.calls = nil
	f.buf.Reset()
	_, _, cw := layout(sessRows(2), 0, 80)
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
				w.Write("grove-x", rows(1+(g+i)%3), 0, t0.Add(time.Duration(i)*time.Second))
			}
		}(g)
	}
	wg.Wait()
}
