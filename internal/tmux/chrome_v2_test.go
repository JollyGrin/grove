package tmux

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// grove-317: Claude Code v2.1.282+ draws the input box as bare lines between
// two full-width ─ rules (no │ sides, no ╰ corner). These fixtures are real
// captures (isolated tmux server, 100 columns) — before this change the box
// finder saw none of them, so every relay read as landed.

// The text typed into cc2.1.283-unsent.txt (it wraps across two box lines).
const v2Unsent = "please rebase onto main and rerun the gate, then report back with the full test output and anything that looks flaky across the suites"

// The prompt submitted in cc2.1.283-running.txt / -submitted.txt.
const v2Submitted = "Reply with just the word OK and nothing else, do not use tools"

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPasteLandedV2Chrome(t *testing.T) {
	cases := []struct {
		fixture, text string
		want          bool
	}{
		{"cc2.1.283-unsent.txt", v2Unsent, false},
		{"cc2.1.283-pasted-chip.txt", "line one of the relayed answer\nline two", false},
		{"cc2.1.283-idle.txt", v2Unsent, true},
		{"cc2.1.283-running.txt", v2Submitted, true},
		{"cc2.1.283-submitted.txt", v2Submitted, true},
		// Modals share the rules but are not the input box: permissive.
		{"cc2.1.282-typed.txt", "mate tea", true},
		{"cc2.1.282-perm.txt", "touch hello.txt", true},
	}
	for _, c := range cases {
		if got := pasteLanded(fixture(t, c.fixture), c.text); got != c.want {
			t.Errorf("%s: pasteLanded = %v, want %v", c.fixture, got, c.want)
		}
	}
}

func TestInputBoxContentV2Chrome(t *testing.T) {
	cases := []struct{ fixture, want string }{
		// Prompt glyph and continuation indent stripped.
		{"cc2.1.283-unsent.txt", "please rebase onto main and rerun the gate, then report back with the full test output and\nanything that looks flaky across the suites"},
		{"cc2.1.283-pasted-chip.txt", "[Pasted text #1 +6 lines]"},
		{"cc2.1.283-submitted.txt", ""},
		// The idle box shows a dim placeholder suggestion; plain capture
		// cannot tell it from typed text, and it never matches a probe.
		{"cc2.1.283-idle.txt", `Try "refactor main.go"`},
		{"cc2.1.282-typed.txt", ""},
		{"cc2.1.282-perm.txt", ""},
	}
	for _, c := range cases {
		if got := inputBoxContent(fixture(t, c.fixture)); got != c.want {
			t.Errorf("%s: inputBoxContent = %q, want %q", c.fixture, got, c.want)
		}
	}
}

func TestOutsideInputBoxV2Chrome(t *testing.T) {
	// Unsent text inside the box is neither outside it nor consumed.
	unsent := fixture(t, "cc2.1.283-unsent.txt")
	out := outsideInputBox(unsent)
	if strings.Contains(squeeze(out), squeeze("please rebase onto main")) {
		t.Errorf("outsideInputBox kept the box body:\n%s", out)
	}
	if strings.HasSuffix(strings.TrimRight(out, "\n"), strings.Repeat("─", minRuleRunes)) {
		t.Errorf("outsideInputBox kept the box's top rule:\n%s", out)
	}
	if consumedEvidence(unsent, v2Unsent) {
		t.Error("consumedEvidence counted probe text sitting unsent in the v2 input box")
	}
	if consumedEvidence(fixture(t, "cc2.1.283-pasted-chip.txt"), "line one of the relayed answer") {
		t.Error("consumedEvidence counted a pending [Pasted text] chip as consumed")
	}
	// Submitted: the echo lives above the (now empty) box.
	for _, f := range []string{"cc2.1.283-running.txt", "cc2.1.283-submitted.txt"} {
		if !consumedEvidence(fixture(t, f), v2Submitted) {
			t.Errorf("%s: consumedEvidence missed the transcript echo above the box", f)
		}
	}
}

func TestUnboxedRangeShapes(t *testing.T) {
	rule := strings.Repeat("─", 40)
	cases := []struct {
		name    string
		capture string
		ok      bool
	}{
		{"prompt between rules", rule + "\n❯ hi\n" + rule + "\n  footer\n", true},
		{"heavy rules", strings.Repeat("━", 40) + "\n❯ hi\n" + strings.Repeat("━", 40) + "\n", true},
		{"no prompt glyph: a modal", rule + "\n Do you want to proceed?\n" + rule + "\n", false},
		{"short dividers are not rules", "────\n❯ hi\n────\n", false},
		{"rules scrolled too far up", rule + "\n❯ hi\n" + rule + "\n" + strings.Repeat("noise\n", footerSlack+1), false},
		{"lone rule", "❯ hi\n" + rule + "\n", false},
		{"adjacent rules, no body", rule + "\n" + rule + "\n", false},
	}
	for _, c := range cases {
		_, _, ok := inputBoxRange(strings.Split(c.capture, "\n"))
		if ok != c.ok {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.ok)
		}
	}
}

// Relays to a worker that is MID-TURN (the orchestrator nudges busy workers
// constantly). Real v2.1.283 captures through the real PasteText path: a
// message submitted during a running turn is QUEUED, and Claude draws it
// ABOVE the rules as a transcript-style "❯ <text>" + "ctrl+x ctrl+s to send
// now", while the box shows the placeholder "Press up to edit queued
// messages". So the queued message reads as landed (accepted) AND as
// consumed — no false "never submitted", no spurious uptake warning — while
// text pasted mid-turn without its Enter still sits in the box and is
// caught. No "esc to interrupt" appears anywhere in these captures.
func TestMidTurnRelayV2Chrome(t *testing.T) {
	const (
		queued   = "after that also say the queued-probe-marker word BANANA"
		chip     = "queued multi line answer one\nline two of it\nline three\nline four\nline five\nline six"
		unsentMT = "this relay never got its enter while the worker was busy"
	)
	busy := fixture(t, "cc2.1.283-busy.txt")
	if strings.Contains(strings.ToLower(busy), runningTurnMarker) {
		t.Fatalf("fixture drift: v2.1.283 busy capture shows %q", runningTurnMarker)
	}
	cases := []struct {
		fixture, text    string
		landed, consumed bool
	}{
		{"cc2.1.283-busy-queued.txt", queued, true, true},
		{"cc2.1.283-busy-queued-chip.txt", chip, true, true},
		// The guard still bites mid-turn: a paste whose Enter was lost.
		{"cc2.1.283-busy-unsent.txt", unsentMT, false, false},
		// Relaying into a busy pane before anything was pasted: the box is
		// empty, nothing of ours is outside it.
		{"cc2.1.283-busy.txt", unsentMT, true, false},
	}
	for _, c := range cases {
		capture := fixture(t, c.fixture)
		if got := pasteLanded(capture, c.text); got != c.landed {
			t.Errorf("%s: pasteLanded = %v, want %v", c.fixture, got, c.landed)
		}
		if got := consumedEvidence(capture, c.text); got != c.consumed {
			t.Errorf("%s: consumedEvidence = %v, want %v", c.fixture, got, c.consumed)
		}
	}
}

// Claude Code v2.1.283 draws a dim (SGR 2) ghost prompt suggestion in the
// idle box. cc2.1.283-ghost-e.txt is a real `capture-pane -e` of one
// ("❯\u00a0\x1b[2mcommit notes.txt\x1b[0m"): plain, it reads as typed text.
func TestGhostSuggestionV2Chrome(t *testing.T) {
	ghost := fixture(t, "cc2.1.283-ghost-e.txt")
	if !strings.Contains(ghost, "❯\u00a0\x1b[2mcommit notes.txt\x1b[0m") {
		t.Fatal("fixture drift: no dim ghost in the input box")
	}
	if got := inputBoxContent(dropDim(ghost)); got != "" {
		t.Errorf("dim ghost should read as an empty box, got %q", got)
	}
	// The false alarm the ghost caused: a short relay matching its prefix.
	if !pasteLanded(ghost, "commit") {
		t.Error("a ghost suggestion matching the relay made pasteLanded report unsent")
	}
	// Typed (non-dim) text after the ghost vanished is still caught.
	typed := strings.Replace(ghost, "\x1b[2mcommit notes.txt\x1b[0m", "commit", 1)
	if typed == ghost {
		t.Fatal("fixture drift: ghost sequence not found")
	}
	if pasteLanded(typed, "commit") {
		t.Error("typed text in a styled capture must still read as unsent")
	}
}

func TestDropDim(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain passes through", "❯ hi\n", "❯ hi\n"},
		{"dim run dropped, rest kept", "❯ \x1b[2mghost\x1b[0m tail", "❯  tail"},
		{"22 ends dim", "\x1b[2ma\x1b[22mb", "b"},
		{"combined params", "\x1b[1;2ma\x1b[0mb", "b"},
		{"256-colour 2 is not dim", "\x1b[38;5;2mgreen\x1b[39m", "green"},
		{"truecolour 2 is not dim", "\x1b[38;2;2;2;2mx\x1b[0m", "x"},
		{"dim carries across lines, newline kept", "\x1b[2ma\nb\x1b[0mc", "\nc"},
		{"OSC hyperlink dropped", "\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\", "link"},
	}
	for _, c := range cases {
		if got := dropDim(c.in); got != c.want {
			t.Errorf("%s: dropDim = %q, want %q", c.name, got, c.want)
		}
	}
}
