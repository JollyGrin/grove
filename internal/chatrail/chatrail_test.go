package chatrail

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
