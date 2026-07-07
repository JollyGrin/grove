package ledger

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/cost"
)

func row(ticket string, at time.Time, usd float64) Row {
	return Row{
		Time: at, Ticket: ticket, Title: "Title of " + ticket,
		Desc: "a short description", Repo: "dummy", Branch: ticket + "-branch",
		Outcome: "open", Input: 100, Output: 200, CacheCreate: 300, CacheRead: 400,
		Turns: 5, USD: usd,
	}
}

func TestAppendReadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	r := row("task-001", at, 1.25)
	// Commas, quotes, and newlines in free-text fields must survive CSV.
	r.Title = `He said "hi", twice`
	r.Desc = "line one\nline two, with comma"
	if err := Append(dir, r); err != nil {
		t.Fatal(err)
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("rows = %d, want 1", len(got))
	}
	g := got[0]
	if !g.Time.Equal(at) || g.Ticket != "task-001" || g.Title != r.Title || g.Desc != r.Desc {
		t.Errorf("roundtrip mismatch: %+v", g)
	}
	if g.Input != 100 || g.Output != 200 || g.CacheCreate != 300 || g.CacheRead != 400 || g.Turns != 5 || g.USD != 1.25 {
		t.Errorf("numeric roundtrip mismatch: %+v", g)
	}
	if g.Repo != "dummy" || g.Branch != "task-001-branch" || g.Outcome != "open" {
		t.Errorf("meta roundtrip mismatch: %+v", g)
	}
}

func TestAppendWritesHeaderOnce(t *testing.T) {
	dir := t.TempDir()
	at := time.Now()
	if err := Append(dir, row("a", at, 1)); err != nil {
		t.Fatal(err)
	}
	if err := Append(dir, row("b", at, 2)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3 (header + 2 rows):\n%s", len(lines), raw)
	}
	if !strings.HasPrefix(lines[0], "time,ticket,title") {
		t.Errorf("missing header: %q", lines[0])
	}
}

func TestReadMissingFile(t *testing.T) {
	got, err := Read(t.TempDir())
	if err != nil || len(got) != 0 {
		t.Fatalf("missing file: got %v rows, err %v — want 0, nil", len(got), err)
	}
}

func TestReadSkipsGarbageRows(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, row("a", time.Now(), 1)); err != nil {
		t.Fatal(err)
	}
	// A torn/garbage line must not poison the rest of the file.
	f, _ := os.OpenFile(Path(dir), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("garbage,line\n")
	f.Close()
	if err := Append(dir, row("b", time.Now(), 2)); err != nil {
		t.Fatal(err)
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Ticket != "a" || got[1].Ticket != "b" {
		t.Fatalf("got %+v, want rows a and b", got)
	}
}

func TestConcurrentAppends(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = Append(dir, row("t", time.Now(), 1))
		}()
	}
	wg.Wait()
	got, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 20 {
		t.Fatalf("rows = %d, want 20 (torn concurrent writes?)", len(got))
	}
}

func TestLatest(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	rows := []Row{
		row("a", t0, 1),
		row("b", t0.Add(time.Hour), 2),
		row("a", t0.Add(2*time.Hour), 3),
	}
	got := Latest(rows)
	if len(got) != 2 {
		t.Fatalf("tickets = %d, want 2", len(got))
	}
	if got["a"].USD != 3 || got["b"].USD != 2 {
		t.Errorf("latest wrong: a=%v b=%v", got["a"].USD, got["b"].USD)
	}
}

func TestSnip(t *testing.T) {
	in := "  first line\nsecond\t line\n\nthird  "
	if got := Snip(in, 200); got != "first line second line third" {
		t.Errorf("Snip collapse = %q", got)
	}
	long := strings.Repeat("ab ", 100)
	got := Snip(long, 20)
	if len([]rune(got)) > 20 {
		t.Errorf("Snip len = %d, want <= 20", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("Snip truncation must be visible: %q", got)
	}
	if Snip("short", 20) != "short" {
		t.Errorf("Snip must not touch short strings")
	}
}

func TestRecordingToggle(t *testing.T) {
	dir := t.TempDir()
	// Absent file: config default wins.
	if Enabled(dir, false) {
		t.Error("absent + default off should be off")
	}
	if !Enabled(dir, true) {
		t.Error("absent + default on should be on")
	}
	if err := SetRecording(dir, true); err != nil {
		t.Fatal(err)
	}
	if !Enabled(dir, false) {
		t.Error("explicit on must override default off")
	}
	if err := SetRecording(dir, false); err != nil {
		t.Fatal(err)
	}
	if Enabled(dir, true) {
		t.Error("explicit off must override default on")
	}
}

func TestOutcome(t *testing.T) {
	cases := map[string]string{
		"MERGED": "merged", "CLOSED": "closed", "OPEN": "open", "": "none",
	}
	for in, want := range cases {
		if got := Outcome(in); got != want {
			t.Errorf("Outcome(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDeltaPoints(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	rows := []Row{
		row("a", t0, 1.0),
		row("a", t0.Add(time.Hour), 2.5),   // +1.5
		row("a", t0.Add(2*time.Hour), 2.5), // no growth → no point
		row("b", t0.Add(time.Hour), 4.0),
		row("live", t0, 9.0), // excluded: has live transcripts
	}
	pts := DeltaPoints(rows, map[string]bool{"live": true})
	var sum float64
	for _, p := range pts {
		sum += p.USD
	}
	if want := 1.0 + 1.5 + 4.0; sum != want {
		t.Errorf("delta sum = %v, want %v (points %+v)", sum, want, pts)
	}
	for _, p := range pts {
		if p.USD == 0 {
			t.Errorf("zero-USD point emitted: %+v", p)
		}
	}
	var _ []cost.Point = pts // points must be chartable by cost.Buckets
}
