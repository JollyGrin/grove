package hooks

import (
	"regexp"
	"strings"
)

// Sentinel kinds — the three the kickoff prompt teaches.
const (
	SentinelQuestion = "QUESTION"
	SentinelBlocked  = "BLOCKED"
	SentinelDone     = "DONE"
)

// sentinelLineRe matches ONE line of a final assistant message as a STATUS
// sentinel (grove-441). Tolerated variants of the kickoff's
// `STATUS: DONE — text` contract, every one observed in the wild:
//
//	STATUS: DONE                      bare, no separator, no text
//	**STATUS: DONE** — text           bold wraps the sentinel only
//	**STATUS: DONE — text**           bold wraps the whole line
//	STATUS: DONE: text                colon separator
//	STATUS: DONE - text / – text      hyphen / en dash
//	   STATUS: DONE — text            indented (numbered-list echo)
//
// Groups: 1 = kind, 2 = separator (empty when absent), 3 = text. A kind
// with no separator is only a sentinel when its text is EMPTY —
// "STATUS: DONE lines are required" is prose about the protocol, not a
// claim of it.
var sentinelLineRe = regexp.MustCompile(`^\s*(?:\*\*|__)?\s*STATUS:\s*(QUESTION|BLOCKED|DONE)\s*(?:\*\*|__)?\s*([—–-]+|:)?\s*(.*?)\s*(?:\*\*|__)?\s*$`)

// ParseSentinel finds the STATUS sentinel in a final assistant message.
// The kickoff says "end your final message with" one, so the LAST
// matching line wins — an earlier quote of the protocol ("I'll finish
// with STATUS: DONE — …") never shadows the real line at the bottom.
//
// kind is one of SentinelQuestion/SentinelBlocked/SentinelDone; text is
// the trailing text (may be empty for DONE/BLOCKED). A QUESTION with no
// text of its own takes the message's last paragraph before the sentinel
// line, so a question asked in prose above a bare `STATUS: QUESTION`
// still reaches the operator.
//
// Prompt-echo guard: the kickoff's own example lines carry an angle-
// bracket placeholder (`STATUS: DONE — <one paragraph: …>`). A line whose
// text is such a placeholder is the protocol being quoted, never a claim,
// and is skipped — so a message that only echoes the template classifies
// as "no STATUS line". Callers only ever feed last_assistant_message
// here, never pane text (the pane always contains the kickoff).
func ParseSentinel(msg string) (kind, text string, ok bool) {
	lines := strings.Split(msg, "\n")
	at := -1
	for i, line := range lines {
		m := sentinelLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		k, sep, t := m[1], m[2], m[3]
		if sep == "" && t != "" {
			continue // prose mentioning the kind, not a sentinel
		}
		if isPlaceholder(t) {
			continue // the kickoff template quoted back
		}
		kind, text, ok, at = k, t, true, i
	}
	if !ok {
		return "", "", false
	}
	if kind == SentinelQuestion && text == "" {
		text = lastParagraph(lines[:at])
	}
	return kind, text, true
}

// isPlaceholder reports whether sentinel text is the kickoff template's
// `<…>` slot rather than real content.
func isPlaceholder(t string) bool {
	return strings.HasPrefix(t, "<") && strings.HasSuffix(t, ">")
}

// lastParagraph returns the last blank-line-delimited paragraph of lines,
// joined with single spaces and trimmed; "" when there is none.
func lastParagraph(lines []string) string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	start := end
	for start > 0 && strings.TrimSpace(lines[start-1]) != "" {
		start--
	}
	var parts []string
	for _, l := range lines[start:end] {
		parts = append(parts, strings.TrimSpace(l))
	}
	return strings.Join(parts, " ")
}
