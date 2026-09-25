package detect

import (
	"regexp"
	"strings"
)

// Grove-side additions to the probe core. live.go stays byte-comparable
// with ovs; anything grove adds on top lives here (docs/seed-manifest.md).

// Classify is classifyPaneOutput for a caller that captured the pane
// itself — `gv chat serve` reads one capture per poll for its picker and
// its turn state (grove-300), and has no Detector to hand it.
func Classify(output string) (status AgentStatus, hasClaude bool) {
	return classifyPaneOutput(output)
}

// ErrorMarker scans a pane capture for the markers that mean the turn
// already died silently — usage limit, a sleep-cut, an API error — checked
// line by line so the reported line is the specific matched one, not the
// whole capture. Moved here from internal/supervise (grove-300) so the
// chat server reads the same list the supervisor alerts on.
func ErrorMarker(pane string) (reason, line string, ok bool) {
	for l := range strings.SplitSeq(pane, "\n") {
		switch {
		case strings.Contains(l, "Usage limit reached"), strings.Contains(l, "Request rejected (429)"):
			return "usage_limit", truncateRunes(l), true
		case strings.Contains(l, "computer went to sleep"):
			return "sleep", truncateRunes(l), true
		case strings.Contains(l, "API Error:"):
			return "api_error", truncateRunes(l), true
		}
	}
	return "", "", false
}

// truncateRunes caps the matched line at 200 runes, rune-safe (never
// mid-codepoint — the grove-131 class of bug).
func truncateRunes(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > 200 {
		r = r[:200]
	}
	return string(r)
}

// spinnerRe is a turn's LIVE spinner line on Claude Code 2.1.283 — a
// cycling glyph, a present-participle verb with an ellipsis, then the
// elapsed clock: "✶ Crunching… (2m 41s · ↓ 14.1k tokens)". The glyph
// cycles through · ✢ ✳ ✶ ✻ ✽ frame by frame, and the "thinking/thought"
// stats come and go, so classifyPaneOutput's ✢/✽ + stats checks read a
// running turn as idle on roughly half its frames (the Detector papers
// over that with its hash-change upgrade; a one-shot capture cannot). The
// finished form has no ellipsis and no clock in parens — "✻ Baked for 55s
// · done 12:13 AM" — so it never matches.
var spinnerRe = regexp.MustCompile(`(?m)^\s*[·✢✳✶✻✽*]\s+\S+…\s*\(\d`)

// Spinning reports whether a pane capture shows a turn's live spinner.
// grove-300.
func Spinning(output string) bool {
	return spinnerRe.MatchString(output)
}
