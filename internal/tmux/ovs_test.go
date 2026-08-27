package tmux

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPickPane(t *testing.T) {
	cases := []struct {
		name  string
		panes []PaneInfo
		want  int
	}{
		{"claude in pane 1 of 2", []PaneInfo{{0, "zsh"}, {1, "claude"}}, 1},
		{"claude alone in pane 0", []PaneInfo{{0, "claude"}}, 0},
		{"single shell pane", []PaneInfo{{0, "zsh"}}, 0},
		{"no claude, two panes → highest", []PaneInfo{{0, "zsh"}, {1, "bash"}}, 1},
		// The DEV-4761-class regression: a node dev-server in pane 0 must
		// lose to claude in pane 1 (highest-index claude-ish wins).
		{"node dev-server pane 0, claude pane 1", []PaneInfo{{0, "node"}, {1, "claude"}}, 1},
		{"two claude-ish panes → highest", []PaneInfo{{0, "node"}, {1, "node"}}, 1},
		{"node counts as claude-ish", []PaneInfo{{0, "zsh"}, {1, "node"}, {2, "zsh"}}, 1},
		{"bun counts as claude-ish", []PaneInfo{{0, "bun"}, {1, "zsh"}}, 0},
		// Claude Code sets its process title to its version string
		// (observed live: pane_current_command = "2.1.197").
		{"version-string title counts as claude-ish", []PaneInfo{{0, "2.1.197"}}, 0},
		{"version title in pane 0, shell pane 1", []PaneInfo{{0, "2.1.197"}, {1, "zsh"}}, 0},
		{"empty list", nil, 0},
		// Renumbered panes (pane 0 closed): indexes need not start at 0.
		{"non-zero single pane", []PaneInfo{{1, "claude"}}, 1},
	}
	for _, c := range cases {
		if got := pickPane(c.panes); got != c.want {
			t.Errorf("%s: pickPane = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestParsePaneList(t *testing.T) {
	out := "0 zsh\n1 claude\n"
	panes := parsePaneList(out)
	if len(panes) != 2 || panes[0].Index != 0 || panes[0].Command != "zsh" ||
		panes[1].Index != 1 || panes[1].Command != "claude" {
		t.Errorf("parsePaneList = %+v", panes)
	}
	if got := parsePaneList("garbage\n\n"); len(got) != 0 {
		t.Errorf("garbage should parse to empty, got %+v", got)
	}
}

// --- grove-144: paste → Enter race, verify the submit actually landed ---

// box renders a fake Claude input box holding the given lines.
func box(lines ...string) string {
	var b strings.Builder
	b.WriteString("╭──────────────────────────────────────╮\n")
	for _, l := range lines {
		b.WriteString("│ " + l + "\n")
	}
	b.WriteString("╰──────────────────────────────────────╯\n")
	b.WriteString("  ? for shortcuts\n")
	return b.String()
}

func TestPasteLanded(t *testing.T) {
	const text = "please rebase onto main and rerun the gate"
	cases := []struct {
		name    string
		capture string
		text    string
		want    bool
	}{
		// Permissive by default: nothing readable as an input box counts as
		// landed, so a chrome change can never strand a delivered answer.
		{"empty capture", "", text, true},
		{"plain shell pane", "$ ls\nREADME.md\n$ ", text, true},
		{"no box, text echoed by a shell stub", "hello world\n$ ", "hello world", true},
		// The discriminator: submitted text lives in the transcript ABOVE an
		// empty input box.
		{"submitted: transcript above, empty box", "> " + text + "\n\n" + box("> "), text, true},
		{"submitted: agent already working", "> " + text + "\n✳ Thinking…\n" + box("> "), text, true},
		// The grove-144 failure: Enter was swallowed, text still in the box.
		{"unsent: text still in the box", box("> " + text), text, false},
		{"unsent: pasted-text chip", box("> [Pasted text #1 +12 lines]"), text, false},
		{"unsent: chip with no space", box("> [Pastedtext]"), text, false},
		// Wrapping inside a narrow box must not hide the text (squeeze drops
		// whitespace on both sides, so word- and mid-word wraps both match).
		{"unsent: text wrapped across box lines", box("> please rebase onto main and", "rerun the gate"), text, false},
		{"unsent: mid-word wrap", box("> please rebase onto main a", "nd rerun the gate"), text, false},
		// A box whose top border scrolled off the capture still counts.
		{"unsent: top border scrolled off", "│ > " + text + "\n╰────────╯\n", text, false},
		// Long text: only the probe prefix has to be visible.
		{"unsent: only the head of a long paste visible", box("> " + text + "…"), text + " and then some more prose that wrapped away", false},
		// An unrelated box (permission dialog) is not our text.
		{"other box: permission dialog", box("Do you want to allow this?", "1. Yes  2. No"), text, true},
		// A box buried under a long footer is not the input box.
		{"box too far above the bottom", box("> "+text) + strings.Repeat("noise\n", 8), text, true},
		// Degenerate inputs.
		{"empty text", box("> "), "", true},
	}
	for _, c := range cases {
		if got := pasteLanded(c.capture, c.text); got != c.want {
			t.Errorf("%s: pasteLanded = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestInputBoxContent(t *testing.T) {
	if got := inputBoxContent("no box here\n"); got != "" {
		t.Errorf("shell pane should have no box, got %q", got)
	}
	got := inputBoxContent("transcript line\n" + box("> hi"))
	if !strings.Contains(got, "> hi") || strings.Contains(got, "transcript line") {
		t.Errorf("inputBoxContent = %q, want just the box body", got)
	}
	// Two boxes: the bottom-most one is the input box.
	two := box("older dialog") + box("> newer")
	if got := inputBoxContent(two); strings.Contains(got, "older dialog") {
		t.Errorf("inputBoxContent picked the wrong box: %q", got)
	}
}

// verifySubmit is the retry ladder: check → one retry Enter → check → error.
func TestVerifySubmit(t *testing.T) {
	const text = "ship it"
	unsent, sent := box("> "+text), box("> ")

	cases := []struct {
		name       string
		captures   []string
		captureErr error
		enterErr   error
		wantEnters int
		wantErr    bool
	}{
		{"landed first look: no retry", []string{sent}, nil, nil, 0, false},
		{"landed after one retry Enter", []string{unsent, sent}, nil, nil, 1, false},
		{"never landed: one retry, then an error", []string{unsent, unsent}, nil, nil, 1, true},
		{"unreadable pane counts as landed", []string{""}, nil, nil, 0, false},
		{"capture failure counts as landed", []string{unsent}, errors.New("no pane"), nil, 0, false},
		{"retry Enter failure surfaces", []string{unsent, unsent}, nil, errors.New("no such pane"), 1, true},
	}
	for _, c := range cases {
		i, enters, settles := 0, 0, 0
		err := verifySubmit("%9", text,
			func() (string, error) {
				out := c.captures[min(i, len(c.captures)-1)]
				i++
				return out, c.captureErr
			},
			func() error { enters++; return c.enterErr },
			func() { settles++ },
		)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", c.name, err, c.wantErr)
		}
		if enters != c.wantEnters {
			t.Errorf("%s: %d retry Enters, want %d", c.name, enters, c.wantEnters)
		}
		if settles == 0 {
			t.Errorf("%s: never settled before scraping the pane", c.name)
		}
	}
}

// The failure message has to tell the operator what to do — the whole point
// of grove-144 is that a silent success was worse than a loud failure.
func TestVerifySubmitErrorIsActionable(t *testing.T) {
	err := verifySubmit("grove-x:1.1", "hello there",
		func() (string, error) { return box("> hello there"), nil },
		func() error { return nil },
		func() {})
	if err == nil {
		t.Fatal("an unsent paste must be an error")
	}
	for _, want := range []string{"grove-x:1.1", "nothing was recorded", "send-keys"} {
		if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(want)) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestClaudePaneNonexistentWindow(t *testing.T) {
	// Missing window → fall back to pane 1 (the historical layout), so
	// callers behave exactly as before this helper existed.
	if got := ClaudePane("pr-test-nonexistent-session-xyz", "nope"); got != 1 {
		t.Errorf("ClaudePane fallback = %d, want 1", got)
	}
}

// --- grove-186: delivery confirmation (compact guard + consumption scrape) ---

// The pre-send guard: a pane mid-/compact swallows a paste outright, so
// PasteText must wait it out and — if it never clears — return an error
// BEFORE load-buffer, meaning nothing was sent and the caller records no
// event (the grove-144 contract, reused).
func TestWaitOutCompact(t *testing.T) {
	compacting := "✻ Compacting conversation… (34%)\n" + box("> ")
	idle := box("> ")

	// Shrink the budget so the loop is small and explicit — these are
	// package vars precisely so tests can do this.
	oldPoll, oldMax := compactPoll, compactMax
	compactPoll, compactMax = time.Millisecond, 3*time.Millisecond
	defer func() { compactPoll, compactMax = oldPoll, oldMax }()
	wantPolls := polls(compactMax, compactPoll) // 3 waits ⇒ up to 4 looks

	cases := []struct {
		name        string
		captures    []string
		captureErr  error
		wantErr     bool
		wantSettles int
	}{
		{"idle pane: proceeds on the first look", []string{idle}, nil, false, 0},
		{"compact clears on the third look", []string{compacting, compacting, idle}, nil, false, 2},
		{"never clears: error after the full budget", []string{compacting}, nil, true, wantPolls},
		// Permissive, like every other scrape here: an unreadable pane must
		// never block a relay on the scraper's own uncertainty.
		{"unreadable pane proceeds", []string{""}, errors.New("no pane"), false, 0},
	}
	for _, c := range cases {
		i, settles := 0, 0
		err := waitOutCompact("%9",
			func() (string, error) {
				out := c.captures[min(i, len(c.captures)-1)]
				i++
				return out, c.captureErr
			},
			func() { settles++ },
		)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", c.name, err, c.wantErr)
		}
		if settles != c.wantSettles {
			t.Errorf("%s: %d waits, want %d", c.name, settles, c.wantSettles)
		}
		if c.wantErr && err != nil {
			for _, want := range []string{"mid-compact", "nothing sent"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%s: error %q missing %q", c.name, err, want)
				}
			}
		}
	}
}

// The post-submit scrape. No evidence is a WARNING, never an error: the
// submit was already verified, so the caller still records its event.
func TestAwaitConsumption(t *testing.T) {
	const text = "please rebase onto main and rerun the gate"
	echoed := "> " + text + "\n\n" + box("> ")
	running := box("> ") + "\n  esc to interrupt\n"
	nothing := box("> ") // booting/empty pane: submitted-looking, but no uptake

	oldPoll, oldMax := consumePoll, consumeMax
	consumePoll, consumeMax = time.Millisecond, 3*time.Millisecond
	defer func() { consumePoll, consumeMax = oldPoll, oldMax }()

	cases := []struct {
		name       string
		captures   []string
		captureErr error
		wantWarn   bool
	}{
		{"echoed into the transcript above the box", []string{echoed}, nil, false},
		{"a turn is visibly running", []string{running}, nil, false},
		{"evidence arrives on a later poll", []string{nothing, nothing, echoed}, nil, false},
		{"no evidence within the budget: warn", []string{nothing}, nil, true},
		{"unreadable pane is not a warning", []string{""}, errors.New("no pane"), false},
	}
	for _, c := range cases {
		i := 0
		warn := awaitConsumption("%9", text,
			func() (string, error) {
				out := c.captures[min(i, len(c.captures)-1)]
				i++
				return out, c.captureErr
			},
			func() {},
		)
		if (warn != "") != c.wantWarn {
			t.Errorf("%s: warn = %q, wantWarn %v", c.name, warn, c.wantWarn)
		}
		if c.wantWarn && warn != "" {
			for _, want := range []string{"no consumption evidence", "capture-pane", "%9"} {
				if !strings.Contains(warn, want) {
					t.Errorf("%s: warning %q missing %q", c.name, warn, want)
				}
			}
		}
	}
}

// consumedEvidence's discriminator: text sitting IN the input box is the
// UNSENT case (verifySubmit's business), not evidence of uptake. Only text
// echoed outside the box, or a running turn, counts.
func TestConsumedEvidence(t *testing.T) {
	const text = "please rebase onto main and rerun the gate"
	cases := []struct {
		name    string
		capture string
		want    bool
	}{
		{"echoed above an empty box", "> " + text + "\n\n" + box("> "), true},
		{"still sitting in the input box", box("> " + text), false},
		{"running-turn marker, box empty", box("> ") + "\n  esc to interrupt\n", true},
		{"marker is matched case-insensitively", box("> ") + "\n  Esc to interrupt\n", true},
		{"plain shell pane echoed the line", "$ " + text + "\nbash: command not found\n$ ", true},
		{"booting pane, nothing at all", box("> "), false},
		{"empty capture", "", false},
		// Wrapping in the transcript must not hide the echo (squeeze drops
		// whitespace on both sides), which is how a narrow pane renders it.
		{"echo wrapped across lines", "> please rebase onto\nmain and rerun the gate\n\n" + box("> "), true},
		// Degenerate: nothing distinctive to look for.
		{"empty text", box("> "), true},
	}
	for _, c := range cases {
		probe := text
		if c.name == "empty text" {
			probe = ""
		}
		if got := consumedEvidence(c.capture, probe); got != c.want {
			t.Errorf("%s: consumedEvidence = %v, want %v", c.name, got, c.want)
		}
	}
}

// outsideInputBox is the "above the box" half of the split; with no box
// visible the whole capture is outside (plain shell panes, new chrome).
func TestOutsideInputBox(t *testing.T) {
	got := outsideInputBox("transcript line\n" + box("> hi"))
	if !strings.Contains(got, "transcript line") || strings.Contains(got, "> hi") {
		t.Errorf("outsideInputBox = %q, want just the transcript", got)
	}
	if got := outsideInputBox("no box here\n$ "); got != "no box here\n$ " {
		t.Errorf("no box: outsideInputBox = %q, want the whole capture", got)
	}
}

func TestPolls(t *testing.T) {
	cases := []struct {
		max, poll time.Duration
		want      int
	}{
		{30 * time.Second, 2 * time.Second, 15},
		{15 * time.Second, 3 * time.Second, 5},
		{time.Second, 2 * time.Second, 1}, // max below poll still loops once
		{time.Second, 0, 1},               // degenerate poll never divides by zero
	}
	for _, c := range cases {
		if got := polls(c.max, c.poll); got != c.want {
			t.Errorf("polls(%s, %s) = %d, want %d", c.max, c.poll, got, c.want)
		}
	}
}
