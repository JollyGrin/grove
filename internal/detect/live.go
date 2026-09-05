package detect

import (
	"crypto/sha256"
	"regexp"
	"strconv"
	"strings"

	"github.com/JollyGrin/grove/internal/tmux"
)

// AgentStatus represents the detected state of a Claude agent in a tmux pane.
type AgentStatus int

const (
	StatusUnknown AgentStatus = iota
	StatusIdle
	StatusBusy
	StatusWaiting
)

func (s AgentStatus) String() string {
	switch s {
	case StatusIdle:
		return "idle"
	case StatusBusy:
		return "busy"
	case StatusWaiting:
		return "waiting"
	default:
		return "unknown"
	}
}

// busyActivityRe matches Claude's activity stats line, e.g.
// "↓ 20.1k tokens · thought for 288s" or "tokens · thinking"
var busyActivityRe = regexp.MustCompile(`(?i)\d+\.?\d*k?\s+tokens?\s*·\s*(?:thinking|thought)`)

// LiveInfo describes the live state of a tmux session's Claude pane.
type LiveInfo struct {
	Exists      bool        // tmux session exists
	HasClaude   bool        // Claude UI detected in pane
	Status      AgentStatus // idle/busy/waiting
	PaneContent string      // raw captured pane text (for preview)
}

// Detector wraps stateful agent detection with hash-based change tracking.
// Create one per worktree and reuse across polls.
type Detector struct {
	prevHash   [32]byte
	prevStatus AgentStatus
	hasHistory bool
	paneIdx    int
	paneKnown  bool
}

// Detect checks the tmux window for a running Claude agent, using hash-based
// change detection to upgrade Unknown→Busy when pane content is changing.
// The claude pane is resolved (not assumed at .1 — windows can lose their
// split) and the index cached; it re-resolves when the capture comes back
// empty or classification loses sight of claude (stale index after a pane
// close renumbers panes).
func (d *Detector) Detect(sessionName, windowName string) LiveInfo {
	if !tmux.WindowExists(sessionName, windowName) {
		return LiveInfo{}
	}

	if !d.paneKnown {
		d.paneIdx = tmux.ClaudePane(sessionName, windowName)
		d.paneKnown = true
	}
	target := sessionName + ":" + windowName + "." + strconv.Itoa(d.paneIdx)
	output, err := tmux.CapturePaneBottom(target, 30)
	if err != nil || output == "" {
		d.paneKnown = false
		return LiveInfo{Exists: true}
	}

	status, hasClaude := classifyPaneOutput(output)
	if !hasClaude {
		d.paneKnown = false // re-resolve next poll in case panes renumbered
	}

	// Hash-based change detection: if pattern detection is inconclusive
	// but the pane content changed, the agent is likely streaming output.
	hash := sha256.Sum256([]byte(output))
	if status == StatusUnknown && d.hasHistory && hash != d.prevHash {
		status = StatusBusy
		hasClaude = true
	}

	d.prevHash = hash
	d.prevStatus = status
	d.hasHistory = true

	return LiveInfo{
		Exists:      true,
		HasClaude:   hasClaude,
		Status:      status,
		PaneContent: output,
	}
}

// DetectLive checks the tmux window for a running Claude agent.
// Captures the bottom 30 lines of the resolved claude pane (usually .1,
// but a window that lost its split runs claude in its only pane).
// This is the stateless version — prefer Detector for repeated polling.
func DetectLive(sessionName, windowName string) LiveInfo {
	if !tmux.WindowExists(sessionName, windowName) {
		return LiveInfo{}
	}

	target := sessionName + ":" + windowName + "." + strconv.Itoa(tmux.ClaudePane(sessionName, windowName))
	output, err := tmux.CapturePaneBottom(target, 30)
	if err != nil || output == "" {
		return LiveInfo{Exists: true}
	}

	status, hasClaude := classifyPaneOutput(output)
	return LiveInfo{
		Exists:      true,
		HasClaude:   hasClaude,
		Status:      status,
		PaneContent: output,
	}
}

// captureBottomKnown is the capture seam for DetectLiveFrom: tests swap it
// to serve canned pane content and count captures without a tmux server.
var captureBottomKnown = tmux.CapturePaneBottomKnown

// DetectLiveFrom is DetectLive fed from a per-tick tmux.SessionSnapshot
// (grove-149): window existence, claude-pane resolution, and pane height all
// come from the snapshot's two session-wide reads, so the only tmux exec
// here is the one capture-pane per live task — the dash's 1s refresh used to
// spawn ~6 tmux processes per task per second through the stateless path.
// base is the task's stored (glyph-less) window name; the snapshot matcher
// carries the glyph tolerance, so no ResolveWindowName pre-pass is needed,
// and the capture targets the immutable pane id (grove-116: a name-built
// target could scrape a prefix-sibling's screen). Classification is
// classifyPaneOutput, unchanged — the byte-comparable probe core.
func DetectLiveFrom(snap *tmux.SessionSnapshot, base string) LiveInfo {
	if !snap.WindowExists(base) {
		return LiveInfo{}
	}
	pane, height, ok := snap.ClaudePane(base)
	if !ok {
		return LiveInfo{Exists: true}
	}
	output, err := captureBottomKnown(pane, height, 30)
	if err != nil || output == "" {
		return LiveInfo{Exists: true}
	}

	status, hasClaude := classifyPaneOutput(output)
	return LiveInfo{
		Exists:      true,
		HasClaude:   hasClaude,
		Status:      status,
		PaneContent: output,
	}
}

// bottomN returns the last n elements of a string slice.
func bottomN(lines []string, n int) []string {
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}

// WaitingMarker names which pane-content pattern is putting the pane into
// StatusWaiting, or "" when none matched. Split out of classifyPaneOutput
// so supervise's worker_waiting event (grove-252) has a name to report in
// `data.marker` without changing classifyPaneOutput's own signature — the
// byte-comparable-with-ovs probe core this package otherwise stays. The
// two AskUserQuestion menu markers ("enter to select", "ready to submit")
// are a deliberate divergence from that core (docs/seed-manifest.md): they
// are the shapes that produced the 2026-08-24 2h15m stall on p2p#691, which
// the pre-existing markers below never matched.
func WaitingMarker(output string) string {
	lines := strings.Split(output, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return ""
	}
	b10 := bottomN(lines, 10)
	b5 := bottomN(lines, 5)
	bot10 := strings.ToLower(strings.Join(b10, "\n"))
	bot5 := strings.ToLower(strings.Join(b5, "\n"))

	switch {
	// "esc to cancel": active input/dialog UI element (bottom 5)
	case strings.Contains(bot5, "esc to cancel"):
		return "esc_to_cancel"
	// "No, and tell Claude...": ephemeral selection text, only visible when active (bottom 10)
	case strings.Contains(bot10, "no, and tell claude what to do differently"):
		return "no_and_tell_claude"
	// "do you want" / "would you like": question text (bottom 5 to avoid stale matches)
	case strings.Contains(bot5, "do you want"):
		return "do_you_want"
	case strings.Contains(bot5, "would you like"):
		return "would_you_like"
	// AskUserQuestion menu chrome (grove-252, bottom 10, case-insensitive)
	case strings.Contains(bot10, "enter to select"):
		return "enter_to_select"
	case strings.Contains(bot10, "ready to submit"):
		return "ready_to_submit"
	default:
		return ""
	}
}

// classifyPaneOutput determines the agent status from captured pane content.
//
// Status indicators in Claude Code appear at the bottom of the terminal pane.
// We restrict pattern matching to the bottom N lines to avoid false positives
// from stale indicators that scrolled up but remain visible in the capture.
//
// Detection priority (from CLAUDE.md):
//  1. ⌕ (U+2315) → idle, not claude (search UI overlay)
//  2. "ctrl+r to toggle" → unknown (history search UI)
//  3. Permission prompts / "esc to cancel" → waiting
//  4. ✢ (U+2722) spinner / activity stats / "esc to interrupt" → busy
//  5. Claude UI elements visible → idle
//  6. Default → unknown
func classifyPaneOutput(output string) (AgentStatus, bool) {
	// Split into lines and trim trailing blanks (tmux capture-pane may include them)
	lines := strings.Split(output, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return StatusUnknown, false
	}

	lower := strings.ToLower(output)

	// Bottom slices for targeted detection.
	// Claude Code's status bar, prompts, and input area are always at the
	// bottom of the pane. Checking only the bottom avoids stale patterns
	// from earlier output that scrolled up but remain visible.
	b10 := bottomN(lines, 10)
	b5 := bottomN(lines, 5)
	bot10 := strings.ToLower(strings.Join(b10, "\n"))
	bot5 := strings.ToLower(strings.Join(b5, "\n"))
	raw5 := strings.Join(b5, "\n") // preserve case for unicode (❯)

	// 1. Search UI override — ⌕ (U+2315) anywhere → idle, not Claude
	if strings.Contains(output, "\u2315") {
		return StatusIdle, false
	}

	// 2. History search — bottom 10 → unknown (state hold)
	if strings.Contains(bot10, "ctrl+r to toggle") {
		return StatusUnknown, false
	}

	// 3. Waiting — needs user attention. See WaitingMarker for the full
	//    pattern list (esc-to-cancel / question text / the AskUserQuestion
	//    menu markers grove-252 added).
	if WaitingMarker(output) != "" {
		return StatusWaiting, true
	}

	// 4. Busy — Claude is working
	// ovs divergence from parkranger: spinner (✽ current CC, ✢ historic) and
	// activity stats ("tokens · thinking") are checked over the FULL capture,
	// not bottom 15 — current CC's bottom chrome (input box + dividers + tip
	// + status bar) pushes the live spinner >15 lines above the pane bottom.
	// Both markers are transient UI (vanish when the turn ends), so the
	// stale-scrollback risk that motivated the narrow window doesn't apply.
	// Field-tested 2026-06-10 on DEV-4546: busy pane read as idle otherwise.
	if strings.Contains(output, "✢") || strings.Contains(output, "✽") {
		return StatusBusy, true
	}
	if busyActivityRe.MatchString(lower) {
		return StatusBusy, true
	}
	if strings.Contains(bot5, "esc to interrupt") ||
		strings.Contains(bot5, "ctrl+c to interrupt") {
		return StatusBusy, true
	}

	// 5. Claude UI elements → idle
	//    "claude" branding anywhere in output + prompt/hint indicators at bottom
	//    Also detect model names (opus, sonnet, haiku) and "ctx:" in status bar
	hasPrompt := strings.Contains(raw5, "❯") ||
		strings.Contains(bot10, "type a message") ||
		strings.Contains(bot10, "type your message")
	hasHints := strings.Contains(bot10, "/help") ||
		strings.Contains(bot10, "shift+")

	// Model name or context indicator in status bar → Claude session
	hasModelBar := strings.Contains(bot5, "ctx:") ||
		strings.Contains(bot5, "opus") ||
		strings.Contains(bot5, "sonnet") ||
		strings.Contains(bot5, "haiku")

	if strings.Contains(lower, "claude") && (hasPrompt || hasHints) {
		return StatusIdle, true
	}

	// Also idle when Claude header scrolled off-screen but prompt + hints visible
	if hasPrompt && hasHints {
		return StatusIdle, true
	}

	// Model/ctx bar + prompt → idle Claude (header may have scrolled off)
	if hasModelBar && hasPrompt {
		return StatusIdle, true
	}

	return StatusUnknown, false
}
