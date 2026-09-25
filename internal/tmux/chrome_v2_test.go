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
