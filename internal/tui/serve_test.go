package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/JollyGrin/grove/internal/serve"
	"github.com/JollyGrin/grove/internal/state"
)

// serveFakes swaps the injected serve plumbing for counters.
type serveFakes struct {
	script  []byte
	running bool
	starts  []string
	stops   []string
}

func fakeServe(t *testing.T, script string) *serveFakes {
	t.Helper()
	f := &serveFakes{script: []byte(script)}
	pScript, pRunning, pStart, pStop, pStatuses := ServeScript, ServeRunning, ServeStart, ServeStop, ServeStatuses
	t.Cleanup(func() {
		ServeScript, ServeRunning, ServeStart, ServeStop, ServeStatuses = pScript, pRunning, pStart, pStop, pStatuses
	})
	ServeScript = func() ([]byte, string, error) {
		if f.script == nil {
			return nil, "", fmt.Errorf("open run.sh: %w", os.ErrNotExist)
		}
		return f.script, serve.SHA(f.script), nil
	}
	ServeRunning = func(string) bool { return f.running }
	ServeStart = func(slug string) (string, error) {
		f.starts = append(f.starts, slug)
		return "http://localhost:4100", nil
	}
	ServeStop = func(slug string) (bool, error) {
		f.stops = append(f.stops, slug)
		return true, nil
	}
	ServeStatuses = func([]*state.Feature) map[string]serve.Status { return nil }
	return f
}

// serveModel is a feature board with FEATURES focused and a scratch state
// dir, so trust events are real appends.
func serveModel(t *testing.T, w, h int) Model {
	t.Helper()
	m := featureModel(t, 2, w, h)
	m.stateDir = t.TempDir()
	nm, _ := m.handleKey(fkey("tab"))
	return nm.(Model)
}

func trustEvents(t *testing.T, sd string) []state.Event {
	t.Helper()
	evs, err := state.ReadEvents(sd, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []state.Event
	for _, e := range evs {
		if e.Type == state.EvRunScriptTrusted {
			out = append(out, e)
		}
	}
	return out
}

func press(t *testing.T, m Model, k tea.KeyMsg) (Model, tea.Cmd) {
	t.Helper()
	nm, cmd := m.handleKey(k)
	return nm.(Model), cmd
}

var escKey = tea.KeyMsg{Type: tea.KeyEsc}

const script = "#!/bin/sh\nset -eu\ncd \"$GROVE_WORKTREE\"\necho GROVE_READY http://localhost:$GROVE_PORT\n"

func TestServeUntrustedOpensModalRunsNothing(t *testing.T) {
	f := fakeServe(t, script)
	m := serveModel(t, 120, 40)
	m, cmd := press(t, m, key("s"))
	if m.mode != modeServeReview || m.review == nil {
		t.Fatalf("untrusted run.sh: mode=%d, want the review modal", m.mode)
	}
	if cmd != nil || len(f.starts) != 0 {
		t.Fatalf("the modal must run nothing (cmd=%v starts=%v)", cmd, f.starts)
	}
	if m.review.slug != "train-0" || m.review.sha != serve.SHA([]byte(script)) {
		t.Errorf("review = %+v, want train-0 at the script's sha", m.review)
	}
	out := m.View()
	for _, want := range []string{serve.SHA([]byte(script)), "GROVE_READY", "trust & run", "esc"} {
		if !strings.Contains(out, want) {
			t.Errorf("modal lacks %q", want)
		}
	}

	// esc cancels: no event, nothing started.
	m, cmd = press(t, m, escKey)
	if m.mode != modeList || m.review != nil || cmd != nil {
		t.Fatalf("esc: mode=%d cmd=%v, want back to the list with nothing queued", m.mode, cmd)
	}
	if n := len(trustEvents(t, m.stateDir)); n != 0 || len(f.starts) != 0 {
		t.Fatalf("esc appended %d trust event(s), started %v", n, f.starts)
	}

	// Other keys inside the modal never fall through to list keys.
	m, _ = press(t, m, key("s"))
	m, cmd = press(t, m, key("d"))
	if m.mode != modeServeReview || cmd != nil {
		t.Fatalf("d inside the modal: mode=%d cmd=%v", m.mode, cmd)
	}

	// y appends exactly one trust event for the reviewed sha and serves once.
	m, cmd = press(t, m, key("y"))
	if m.mode != modeList || cmd == nil {
		t.Fatalf("y: mode=%d cmd=%v, want list + a start", m.mode, cmd)
	}
	evs := trustEvents(t, m.stateDir)
	if len(evs) != 1 || evs[0].Data["sha256"] != serve.SHA([]byte(script)) {
		t.Fatalf("trust events = %+v, want one for the reviewed sha", evs)
	}
	msg := cmd()
	if len(f.starts) != 1 || f.starts[0] != "train-0" {
		t.Fatalf("starts = %v, want one for train-0", f.starts)
	}
	nm, _ := m.Update(msg)
	if fl := nm.(Model).flash; !strings.Contains(fl, "http://localhost:4100") {
		t.Errorf("READY value not in the status line: %q", fl)
	}
}

func TestServeTrustedStartsWithoutModal(t *testing.T) {
	f := fakeServe(t, script)
	m := serveModel(t, 120, 40)
	if err := state.Append(m.stateDir, state.Event{Type: state.EvRunScriptTrusted, Data: map[string]string{"sha256": serve.SHA([]byte(script))}}); err != nil {
		t.Fatal(err)
	}
	m, cmd := press(t, m, key("s"))
	if m.mode != modeList || m.review != nil || cmd == nil {
		t.Fatalf("trusted: mode=%d cmd=%v, want a straight start", m.mode, cmd)
	}
	cmd()
	if len(f.starts) != 1 || len(trustEvents(t, m.stateDir)) != 1 {
		t.Fatalf("trusted start: starts=%v trust events=%d (no new one)", f.starts, len(trustEvents(t, m.stateDir)))
	}

	// An edited script is untrusted again: the modal, not a start.
	f.script = []byte(script + "rm -rf ~\n")
	m, cmd = press(t, m, key("s"))
	if m.mode != modeServeReview || cmd != nil {
		t.Fatalf("changed script: mode=%d cmd=%v, want the review modal", m.mode, cmd)
	}
}

func TestServeRunningOffersStop(t *testing.T) {
	f := fakeServe(t, script)
	f.running = true
	m := serveModel(t, 120, 40)
	m, cmd := press(t, m, key("s"))
	if m.mode != modeServeStop || cmd != nil || !strings.Contains(m.View(), "stop ▶ train-0?") {
		t.Fatalf("running: mode=%d cmd=%v, want the stop confirmation", m.mode, cmd)
	}
	m, cmd = press(t, m, key("n"))
	if m.mode != modeList || cmd != nil || len(f.stops) != 0 {
		t.Fatal("any key but y backs out of the stop")
	}
	m, _ = press(t, m, key("s"))
	_, cmd = press(t, m, key("y"))
	if cmd == nil {
		t.Fatal("y should stop")
	}
	cmd()
	if len(f.stops) != 1 || len(f.starts) != 0 {
		t.Fatalf("stops=%v starts=%v", f.stops, f.starts)
	}
}

func TestServeNoScriptPointsAtInit(t *testing.T) {
	fakeServe(t, "")
	ServeScript = func() ([]byte, string, error) { return nil, "", fmt.Errorf("x: %w", os.ErrNotExist) }
	m, cmd := press(t, serveModel(t, 120, 40), key("s"))
	if m.mode != modeList || cmd != nil || !strings.Contains(m.flash, "gv serve init") {
		t.Fatalf("no run.sh: mode=%d flash=%q", m.mode, m.flash)
	}
}

// s with AGENTS focused never serves; with no feature open it is inert.
func TestServeKeyNeedsFeatureFocus(t *testing.T) {
	f := fakeServe(t, script)
	m := featureModel(t, 2, 120, 40)
	m.stateDir = t.TempDir()
	m, cmd := press(t, m, key("s"))
	if m.mode != modeList || cmd != nil || !strings.Contains(m.flash, "tab to FEATURES") {
		t.Fatalf("AGENTS focus: mode=%d flash=%q", m.mode, m.flash)
	}
	none := featureModel(t, 0, 120, 40)
	before := none.View()
	none, cmd = press(t, none, key("s"))
	if cmd != nil || none.flash != "" || none.View() != before {
		t.Fatal("no feature open: s must change nothing")
	}
	if len(f.starts) != 0 {
		t.Fatal("nothing may start")
	}
}

func TestServeModalFitsSmallSizes(t *testing.T) {
	fakeServe(t, "")
	long := strings.Repeat("echo a-very-long-line-that-will-not-fit-in-a-narrow-pane-at-all-anywhere\n", 80)
	for _, sz := range [][2]int{{120, 40}, {80, 24}, {40, 20}, {30, 10}, {20, 6}, {12, 4}} {
		ServeScript = func() ([]byte, string, error) { return []byte(long), serve.SHA([]byte(long)), nil }
		m := serveModel(t, sz[0], sz[1])
		m, _ = press(t, m, key("s"))
		if m.mode != modeServeReview {
			t.Fatalf("%v: no modal", sz)
		}
		for i := 0; i < 3; i++ {
			m, _ = press(t, m, key("G"))
			out := m.View()
			if h := lipgloss.Height(out); h > sz[1] {
				t.Errorf("%dx%d: modal is %d rows tall", sz[0], sz[1], h)
			}
			for n, ln := range strings.Split(out, "\n") {
				if w := lipgloss.Width(ln); w > sz[0] {
					t.Errorf("%dx%d line %d is %d wide", sz[0], sz[1], n, w)
				}
			}
			m, _ = press(t, m, key("g"))
		}
	}
}

func TestServeModalScrolls(t *testing.T) {
	fakeServe(t, "")
	var b strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&b, "echo line-%d\n", i)
	}
	ServeScript = func() ([]byte, string, error) { return []byte(b.String()), serve.SHA([]byte(b.String())), nil }
	m, _ := press(t, serveModel(t, 80, 24), key("s"))
	if strings.Contains(m.View(), "line-100") {
		t.Fatal("the last line should be below the fold at first")
	}
	m, _ = press(t, m, key("G"))
	if !strings.Contains(m.View(), "line-100") || strings.Contains(m.View(), "echo line-1\n") {
		t.Fatal("G should scroll to the end")
	}
	page := m.reviewWindow()
	if m.review.scroll != 100-page {
		t.Errorf("scroll = %d, want clamped at %d", m.review.scroll, 100-page)
	}
	m, _ = press(t, m, key("j"))
	if m.review.scroll != 100-page {
		t.Error("j past the end must stay clamped")
	}
}

// The script under review can never drive the terminal: escapes and bidi
// controls render visibly.
func TestReviewLinesSanitize(t *testing.T) {
	got := reviewLines([]byte("a\x1b[2Jb\n\tc\r\nd‮e\xff\n"))
	want := []string{"a^[[2Jb", "    c^M", "d<U+202E>e\\x??"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("reviewLines = %q, want %q", got, want)
	}
}

func TestServeLabelOnRail(t *testing.T) {
	cases := []struct {
		st   serve.Status
		ok   bool
		want string
	}{
		{serve.Status{}, false, "serve –"},
		{serve.Status{State: serve.StateNone}, true, "serve –"},
		{serve.Status{State: serve.StateUntrusted}, true, "serve untrusted"},
		{serve.Status{State: serve.StateStopped, Port: 4100}, true, "serve stopped"},
		{serve.Status{State: serve.StateRunning, Port: 4100}, true, "serve ▶ :4100"},
		{serve.Status{State: serve.StateRunning, Port: 4100, URL: "http://localhost:4100", Behind: true}, true, "serve ▶ http://localhost:4100 (behind tip)"},
	}
	for _, c := range cases {
		if got := serveLabel(c.st, c.ok); got != c.want {
			t.Errorf("serveLabel(%+v) = %q, want %q", c.st, got, c.want)
		}
	}
	m := featureModel(t, 1, 120, 40)
	nm, _ := m.Update(serveStatusMsg{serves: map[string]serve.Status{"train-0": {State: serve.StateRunning, Port: 4100, URL: "http://localhost:4100"}}})
	if out := nm.(Model).View(); !strings.Contains(out, "train-0  1/3  ↓2 main  serve ▶ http://localhost:4100  est $2.00") {
		t.Errorf("rail title lacks the running serve:\n%s", out)
	}
}

func TestHelpListsServe(t *testing.T) {
	m := featureModel(t, 1, 200, 60)
	m.mode = modeHelp
	if !strings.Contains(m.View(), "serve (FEATURES focused, or in the lens)") {
		t.Error("help screen lacks s")
	}
}

// grove-381 × grove-378: s in the lens runs the same gate and every
// answer returns to the lens; SERVE renders the cockpit's serve status.
func TestServeFromLens(t *testing.T) {
	f := fakeServe(t, script)
	m := lensModel(t, 120, 59)
	m.stateDir = t.TempDir()

	m, cmd := press(t, m, key("s"))
	if m.mode != modeServeReview || cmd != nil || m.review.slug != "train-0" {
		t.Fatalf("s in the lens: mode=%d cmd=%v, want the review modal for train-0", m.mode, cmd)
	}
	m, _ = press(t, m, escKey)
	if m.mode != modeLens || len(trustEvents(t, m.stateDir)) != 0 {
		t.Fatalf("esc from a lens-opened modal: mode=%d, want back in the lens, no event", m.mode)
	}
	m, _ = press(t, m, key("s"))
	m, cmd = press(t, m, key("y"))
	if m.mode != modeLens || cmd == nil || len(trustEvents(t, m.stateDir)) != 1 {
		t.Fatalf("y from the lens: mode=%d cmd=%v", m.mode, cmd)
	}
	cmd()
	if len(f.starts) != 1 {
		t.Fatalf("starts = %v, want one", f.starts)
	}

	nm, _ := m.Update(serveStatusMsg{serves: map[string]serve.Status{"train-0": {State: serve.StateRunning, Port: 4100, URL: "http://localhost:4100"}}})
	m = nm.(Model)
	if out := m.View(); !strings.Contains(out, "running :4100  http://localhost:4100") {
		t.Errorf("lens SERVE lacks the running serve:\n%s", out)
	}

	f.running = true
	m, _ = press(t, m, key("s"))
	out := m.View()
	if m.mode != modeServeStop || !strings.Contains(out, "stop ▶ train-0?") || !strings.Contains(out, "SERVE") {
		t.Fatalf("running, from the lens: mode=%d, want the stop confirm on the lens:\n%s", m.mode, out)
	}
	if h := lipgloss.Height(out); h > 59 {
		t.Errorf("lens stop frame is %d rows", h)
	}
	m, _ = press(t, m, key("n"))
	if m.mode != modeLens || len(f.stops) != 0 {
		t.Fatalf("cancelled stop: mode=%d stops=%v", m.mode, f.stops)
	}
}
