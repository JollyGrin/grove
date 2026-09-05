package detect

import (
	"strings"
	"testing"
)

func TestClassifyPaneOutput_SearchUI(t *testing.T) {
	output := "some stuff\n⌕ Search…\nmore stuff"
	status, hasClaude := classifyPaneOutput(output)
	if status != StatusIdle || hasClaude {
		t.Errorf("got status=%v hasClaude=%v, want idle/false", status, hasClaude)
	}
}

func TestClassifyPaneOutput_HistorySearch(t *testing.T) {
	output := "bck-search: something\nctrl+r to toggle"
	status, hasClaude := classifyPaneOutput(output)
	if status != StatusUnknown || hasClaude {
		t.Errorf("got status=%v hasClaude=%v, want unknown/false", status, hasClaude)
	}
}

func TestClassifyPaneOutput_Waiting(t *testing.T) {
	tests := []string{
		"Do you want to proceed?\n❯ Yes",
		"Press esc to cancel",
		"No, and tell Claude what to do differently",
	}
	for _, output := range tests {
		status, hasClaude := classifyPaneOutput(output)
		if status != StatusWaiting || !hasClaude {
			t.Errorf("output=%q: got status=%v hasClaude=%v, want waiting/true", output, status, hasClaude)
		}
	}
}

// grove-252: the AskUserQuestion menu markers — the shapes that produced
// the 2026-08-24 2h15m stall on p2p#691 (the menu left `agent: working`
// with no hook sentinel, since none of the pre-existing waiting patterns
// matched it). Case-insensitive, bottom-10.
func TestClassifyPaneOutput_MenuMarkers(t *testing.T) {
	tests := []string{
		"Pick an option\n1. Yes\n2. No\nEnter to select",
		"ENTER TO SELECT",
		"Type your answer\nReady to submit",
		"ready to submit",
	}
	for _, output := range tests {
		status, hasClaude := classifyPaneOutput(output)
		if status != StatusWaiting || !hasClaude {
			t.Errorf("output=%q: got status=%v hasClaude=%v, want waiting/true", output, status, hasClaude)
		}
	}
}

func TestWaitingMarker(t *testing.T) {
	tests := []struct {
		output string
		want   string
	}{
		{"Press esc to cancel", "esc_to_cancel"},
		{"No, and tell Claude what to do differently", "no_and_tell_claude"},
		{"Do you want to proceed?", "do_you_want"},
		{"Would you like to continue?", "would_you_like"},
		{"Enter to select", "enter_to_select"},
		{"Ready to submit", "ready_to_submit"},
		{"nothing interesting here", ""},
	}
	for _, tt := range tests {
		if got := WaitingMarker(tt.output); got != tt.want {
			t.Errorf("WaitingMarker(%q) = %q, want %q", tt.output, got, tt.want)
		}
	}
}

func TestClassifyPaneOutput_Busy(t *testing.T) {
	tests := []string{
		"Working on it...\nesc to interrupt",
		"Processing...\nctrl+c to interrupt",
	}
	for _, output := range tests {
		status, hasClaude := classifyPaneOutput(output)
		if status != StatusBusy || !hasClaude {
			t.Errorf("output=%q: got status=%v hasClaude=%v, want busy/true", output, status, hasClaude)
		}
	}
}

func TestClassifyPaneOutput_BusySpinner(t *testing.T) {
	// Claude Code shows ✢ (U+2722) spinner during active processing.
	// The spinner appears ~8 lines from bottom with stats line.
	var lines []string
	for i := 0; i < 10; i++ {
		lines = append(lines, "  Output from task")
	}
	lines = append(lines, "✢ Doing… (13m 17s · ↓ 20.1k tokens · thought for 288s)")
	lines = append(lines, "")
	lines = append(lines, "────────────────────────────────────────")
	lines = append(lines, "  Opus 4.6  ctx:42%  ↑3 ↓20.1k")
	lines = append(lines, "❯")

	output := strings.Join(lines, "\n")
	status, hasClaude := classifyPaneOutput(output)
	if status != StatusBusy || !hasClaude {
		t.Errorf("busy spinner: got status=%v hasClaude=%v, want busy/true", status, hasClaude)
	}
}

func TestClassifyPaneOutput_BusyActivityStats(t *testing.T) {
	// Activity stats without the ✢ spinner character, just the token stats.
	var lines []string
	for i := 0; i < 8; i++ {
		lines = append(lines, "  Working on changes")
	}
	lines = append(lines, "↓ 5.2k tokens · thinking")
	lines = append(lines, "────────────────────────────────────────")
	lines = append(lines, "❯")

	output := strings.Join(lines, "\n")
	status, hasClaude := classifyPaneOutput(output)
	if status != StatusBusy || !hasClaude {
		t.Errorf("activity stats: got status=%v hasClaude=%v, want busy/true", status, hasClaude)
	}
}

func TestClassifyPaneOutput_BusyActivityThought(t *testing.T) {
	// "thought for Ns" variant of activity stats.
	var lines []string
	lines = append(lines, "  Some output")
	lines = append(lines, "↓ 1.3k tokens · thought for 12s")
	lines = append(lines, "❯")

	output := strings.Join(lines, "\n")
	status, hasClaude := classifyPaneOutput(output)
	if status != StatusBusy || !hasClaude {
		t.Errorf("thought stats: got status=%v hasClaude=%v, want busy/true", status, hasClaude)
	}
}

func TestClassifyPaneOutput_Idle(t *testing.T) {
	output := "Claude Code v1.0.0\n❯ type a message\n/help for commands"
	status, hasClaude := classifyPaneOutput(output)
	if status != StatusIdle || !hasClaude {
		t.Errorf("got status=%v hasClaude=%v, want idle/true", status, hasClaude)
	}
}

func TestClassifyPaneOutput_IdleWithModelBar(t *testing.T) {
	// Claude header scrolled off but status bar shows model name + prompt.
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, "  Long output from task")
	}
	lines = append(lines, "────────────────────────────────────────")
	lines = append(lines, "  Opus 4.6  ctx:78%  ↑1 ↓5.2k")
	lines = append(lines, "❯")

	output := strings.Join(lines, "\n")
	status, hasClaude := classifyPaneOutput(output)
	if status != StatusIdle || !hasClaude {
		t.Errorf("idle with model bar: got status=%v hasClaude=%v, want idle/true", status, hasClaude)
	}
}

func TestClassifyPaneOutput_IdleWithSonnet(t *testing.T) {
	// Status bar shows "sonnet" instead of "opus".
	var lines []string
	for i := 0; i < 15; i++ {
		lines = append(lines, "  Output")
	}
	lines = append(lines, "  Sonnet 4.5  ctx:25%")
	lines = append(lines, "❯")

	output := strings.Join(lines, "\n")
	status, hasClaude := classifyPaneOutput(output)
	if status != StatusIdle || !hasClaude {
		t.Errorf("idle with sonnet bar: got status=%v hasClaude=%v, want idle/true", status, hasClaude)
	}
}

func TestClassifyPaneOutput_Unknown(t *testing.T) {
	output := "$ ls\nfile1.go  file2.go"
	status, hasClaude := classifyPaneOutput(output)
	if status != StatusUnknown || hasClaude {
		t.Errorf("got status=%v hasClaude=%v, want unknown/false", status, hasClaude)
	}
}

func TestAgentStatusString(t *testing.T) {
	tests := []struct {
		s    AgentStatus
		want string
	}{
		{StatusUnknown, "unknown"},
		{StatusIdle, "idle"},
		{StatusBusy, "busy"},
		{StatusWaiting, "waiting"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("%d.String() = %q, want %q", tt.s, got, tt.want)
		}
	}
}

// --- Bottom-line restriction tests ---
// These verify that stale indicators in scrollback don't cause false positives.

func TestClassifyPaneOutput_StaleBusyAboveIdle(t *testing.T) {
	// Claude was busy, finished, now idle. "esc to interrupt" visible higher up.
	var lines []string
	lines = append(lines, "Working on the task...")
	lines = append(lines, "Reading file.go...")
	lines = append(lines, "esc to interrupt") // stale busy indicator
	for i := 0; i < 12; i++ {
		lines = append(lines, "  Output line from claude")
	}
	lines = append(lines, "Claude Code v1.0.0")
	lines = append(lines, "❯ type a message")
	lines = append(lines, "/help for commands")

	output := strings.Join(lines, "\n")
	status, hasClaude := classifyPaneOutput(output)
	if status != StatusIdle || !hasClaude {
		t.Errorf("stale busy above idle: got status=%v hasClaude=%v, want idle/true", status, hasClaude)
	}
}

func TestClassifyPaneOutput_StaleWaitingAboveBusy(t *testing.T) {
	// Claude asked "do you want", user said yes, now Claude is busy.
	// "Do you want" visible higher up in scrollback.
	var lines []string
	lines = append(lines, "Do you want to apply this edit?")
	lines = append(lines, "Applied edit to file.go")
	for i := 0; i < 12; i++ {
		lines = append(lines, "  Processing changes...")
	}
	lines = append(lines, "esc to interrupt")

	output := strings.Join(lines, "\n")
	status, hasClaude := classifyPaneOutput(output)
	if status != StatusBusy || !hasClaude {
		t.Errorf("stale waiting above busy: got status=%v hasClaude=%v, want busy/true", status, hasClaude)
	}
}

func TestClassifyPaneOutput_StaleWaitingAboveIdle(t *testing.T) {
	// Permission prompt answered long ago, now idle.
	var lines []string
	lines = append(lines, "Do you want to proceed?")
	lines = append(lines, "Proceeding with changes...")
	for i := 0; i < 10; i++ {
		lines = append(lines, "  Completed step")
	}
	lines = append(lines, "Claude Code v1.0.0")
	lines = append(lines, "❯ type a message")

	output := strings.Join(lines, "\n")
	status, hasClaude := classifyPaneOutput(output)
	if status != StatusIdle || !hasClaude {
		t.Errorf("stale waiting above idle: got status=%v hasClaude=%v, want idle/true", status, hasClaude)
	}
}

func TestClassifyPaneOutput_StaleEscCancelAboveIdle(t *testing.T) {
	// "esc to cancel" from an old text input, now idle.
	var lines []string
	lines = append(lines, "Enter file path:")
	lines = append(lines, "esc to cancel")
	for i := 0; i < 12; i++ {
		lines = append(lines, "  Some output")
	}
	lines = append(lines, "Claude Code v1.0.0")
	lines = append(lines, "❯")
	lines = append(lines, "/help for commands")

	output := strings.Join(lines, "\n")
	status, hasClaude := classifyPaneOutput(output)
	if status != StatusIdle || !hasClaude {
		t.Errorf("stale esc-to-cancel above idle: got status=%v hasClaude=%v, want idle/true", status, hasClaude)
	}
}

func TestClassifyPaneOutput_SpinnerAboveTallChrome(t *testing.T) {
	// ovs divergence from parkranger: a spinner ABOVE the bottom-15 window
	// is the NORMAL busy state under current Claude Code — the input box,
	// dividers, tip line, and status bar push the live spinner >15 lines up
	// (field-tested 2026-06-10, DEV-4546 read idle mid-build). The spinner
	// is transient UI that vanishes when the turn ends, so it cannot go
	// stale within the bottom-30-line capture: spinner anywhere = busy.
	var lines []string
	lines = append(lines, "✽ Whirlpooling… (12m 35s · ↓ 47.1k tokens · thinking with high effort)")
	for i := 0; i < 20; i++ {
		lines = append(lines, "  Task output")
	}
	lines = append(lines, "Claude Code v1.0.0")
	lines = append(lines, "❯ type a message")
	lines = append(lines, "/help for commands")

	output := strings.Join(lines, "\n")
	status, hasClaude := classifyPaneOutput(output)
	if status != StatusBusy || !hasClaude {
		t.Errorf("spinner above tall chrome: got status=%v hasClaude=%v, want busy/true", status, hasClaude)
	}
}

func TestClassifyPaneOutput_ActiveWaitingOverStalebusy(t *testing.T) {
	// "esc to interrupt" visible from before, but now there's an active permission prompt.
	// Waiting should win (higher priority + at bottom).
	var lines []string
	lines = append(lines, "esc to interrupt") // stale
	for i := 0; i < 8; i++ {
		lines = append(lines, "  Some output")
	}
	lines = append(lines, "Do you want to apply this edit?")
	lines = append(lines, "  ❯ Yes")
	lines = append(lines, "    No, and tell Claude what to do differently")

	output := strings.Join(lines, "\n")
	status, hasClaude := classifyPaneOutput(output)
	if status != StatusWaiting || !hasClaude {
		t.Errorf("active waiting over stale busy: got status=%v hasClaude=%v, want waiting/true", status, hasClaude)
	}
}

func TestClassifyPaneOutput_IdleWithoutHeader(t *testing.T) {
	// Claude header scrolled off-screen, but prompt and hints visible at bottom.
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, "  Long output from a task")
	}
	lines = append(lines, "❯")
	lines = append(lines, "/help for commands  shift+enter for newline")

	output := strings.Join(lines, "\n")
	status, hasClaude := classifyPaneOutput(output)
	if status != StatusIdle || !hasClaude {
		t.Errorf("idle without header: got status=%v hasClaude=%v, want idle/true", status, hasClaude)
	}
}

func TestClassifyPaneOutput_TrailingBlanks(t *testing.T) {
	// tmux capture-pane may include trailing blank lines
	output := "Claude Code v1.0.0\n❯ type a message\n/help for commands\n\n\n\n"
	status, hasClaude := classifyPaneOutput(output)
	if status != StatusIdle || !hasClaude {
		t.Errorf("trailing blanks: got status=%v hasClaude=%v, want idle/true", status, hasClaude)
	}
}

func TestClassifyPaneOutput_EmptyOutput(t *testing.T) {
	status, hasClaude := classifyPaneOutput("")
	if status != StatusUnknown || hasClaude {
		t.Errorf("empty: got status=%v hasClaude=%v, want unknown/false", status, hasClaude)
	}
}

func TestClassifyPaneOutput_OnlyBlanks(t *testing.T) {
	status, hasClaude := classifyPaneOutput("\n\n   \n\n")
	if status != StatusUnknown || hasClaude {
		t.Errorf("only blanks: got status=%v hasClaude=%v, want unknown/false", status, hasClaude)
	}
}

func TestBottomN(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e"}

	got := bottomN(lines, 3)
	if len(got) != 3 || got[0] != "c" || got[1] != "d" || got[2] != "e" {
		t.Errorf("bottomN(5, 3) = %v, want [c d e]", got)
	}

	got = bottomN(lines, 10)
	if len(got) != 5 {
		t.Errorf("bottomN(5, 10) = %v, want all 5", got)
	}

	got = bottomN(nil, 3)
	if len(got) != 0 {
		t.Errorf("bottomN(nil, 3) = %v, want empty", got)
	}
}

func TestBusyActivityRe(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"↓ 20.1k tokens · thought for 288s", true},
		{"↓ 5.2k tokens · thinking", true},
		{"↓ 1.3k tokens · thought for 12s", true},
		{"100 tokens · thinking", true},
		{"3k tokens · thought", true},
		{"no match here", false},
		{"tokens without number", false},
	}
	for _, tt := range tests {
		got := busyActivityRe.MatchString(tt.input)
		if got != tt.want {
			t.Errorf("busyActivityRe.MatchString(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
