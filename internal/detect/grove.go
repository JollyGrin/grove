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

// errorTail is how far up from the bottom a death marker still counts —
// Claude Code prints its own error chrome just above the input box; a
// marker further up is either scrollback from a turn already moved past,
// or (grove-347) a worker's own view of source/diff content that happens
// to quote one of these strings. Both callers window to this same depth,
// so a full 30-line supervisor capture and chatweb's trimmed capture agree.
const errorTail = 15

// codeGutterRe matches a source or diff viewer's line-number gutter
// (Claude Code's Read tool and diff view both prefix real content with
// one: "    24  func...", "   363 -   case..."). grove-347: a worker
// merely viewing internal/detect/grove.go — which quotes every marker
// string in this file's own switch statement — flapped worker_errored/
// worker_recovered because the old matcher counted a marker anywhere in
// the pane. A gutter line is never Claude Code's own error chrome, which
// prints flush left with no line-number prefix.
var codeGutterRe = regexp.MustCompile(`^\d+\s*[-+]?\s`)

// ErrorMarker scans the bottom of a pane capture for the markers that
// mean the turn already died silently — usage limit, a sleep-cut, an API
// error, an expired login — checked line by line, skipping any
// source/diff line-number gutter, so the reported line is Claude Code's
// own error chrome, never a quote of it in tool output, a file view or a
// diff. Moved here from internal/supervise (grove-300) so the chat server
// and the supervisor read the same list, windowed the same way (grove-347).
func ErrorMarker(pane string) (reason, line string, ok bool) {
	lines := strings.Split(strings.TrimRight(pane, "\n \t"), "\n")
	if len(lines) > errorTail {
		lines = lines[len(lines)-errorTail:]
	}
	for _, l := range lines {
		if codeGutterRe.MatchString(strings.TrimLeft(l, " \t")) {
			continue
		}
		low := strings.ToLower(l)
		switch {
		case strings.Contains(l, "Usage limit reached"), strings.Contains(l, "Request rejected (429)"):
			return "usage_limit", truncateRunes(l), true
		case strings.Contains(l, "computer went to sleep"):
			return "sleep", truncateRunes(l), true
		case strings.Contains(l, "API Error:"):
			return "api_error", truncateRunes(l), true
		// grove-342: an expired Claude login prints one of these (live on
		// groveremote, 2.1.283) and the pane otherwise reads as quiet idle.
		// Case-insensitive: the auth wording is not stable across versions.
		case strings.Contains(low, "login expired"),
			strings.Contains(low, "please run /login"),
			strings.Contains(low, "oauth session expired"):
			return "auth", truncateRunes(l), true
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
