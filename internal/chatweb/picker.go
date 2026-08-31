package chatweb

// grove-218: spotting a modal picker in a pane capture, so the phone can
// grow a raw-key row.
//
// This is the ONE place the whole subsystem reads a pane instead of the
// transcript, and it is garnish by house rule: a prose box cannot drive a
// permission prompt or an option picker (they act on the keypress itself),
// and the transcript does not record a prompt that has not been answered
// yet, so there is nothing else to read. Being garnish, it fails in the
// cheap direction — a missed picker shows a stalled chat and the UI says
// "if it is stuck, ssh in", and a false positive offers keys that type a
// harmless digit into the input box.
//
// Falsified against the shapes that must NOT fire, which is the point:
// prose containing "1." mid-line, a markdown numbered list in the
// transcript ABOVE the box, and a bare input box.

import (
	"regexp"
	"strings"
)

// Picker is what a capture says about the chat's modal state.
type Picker struct {
	// Detected is true only for a real numbered/yes-no prompt.
	Detected bool `json:"detected"`
	// Keys are the raw characters to offer, in order. Esc is always last
	// when anything is detected: every Claude Code modal takes it, and a
	// phone with no keyboard escape is a phone that has to ssh.
	Keys []string `json:"keys"`
	// Prompt is the modal's question, for the row's label. Best-effort.
	Prompt string `json:"prompt"`
}

// boxSides are the runes a pane's bordered box draws its left edge with.
// Requiring one is what separates a picker's options from a markdown
// numbered list the assistant printed into the transcript above it — the
// transcript is not boxed, the modal is.
const boxSides = "│┃"

// optionRe matches one boxed option line: an optional selection caret, a
// single digit, a dot, then text. Anchored at the start of the box's
// CONTENT, so "tell me about 1. the backlog" — a digit mid-prose — never
// matches.
var optionRe = regexp.MustCompile(`^\s*(?:[❯›>]\s*)?([1-9])\.\s+\S`)

// yesNoRe is the older prompt shape, kept because it costs one line and a
// chat that hits one is otherwise unanswerable from a phone.
var yesNoRe = regexp.MustCompile(`\(\s*y\s*/\s*n\s*\)`)

// pickerLookback is how much of the capture's tail to consider. A modal is
// always at the bottom of the pane; scanning the whole scrollback would
// match a picker the operator answered ten minutes ago.
const pickerLookback = 40

// DetectPicker reads a pane capture for a modal prompt.
//
// The rule, deliberately narrow: inside the bottom-most boxed region, the
// option lines must number 1, 2, … n CONSECUTIVELY with n ≥ 2. One boxed
// line starting "1." is a sentence; a run of them is a menu.
func DetectPicker(capture string) Picker {
	lines := strings.Split(capture, "\n")
	if len(lines) > pickerLookback {
		lines = lines[len(lines)-pickerLookback:]
	}
	var want int
	var keys []string
	var prompt string
	var lastPlain string
	for _, raw := range lines {
		body, ok := boxBody(raw)
		if !ok {
			// A line outside the box breaks a run: the options of one modal
			// are contiguous, and two boxes on screen are two prompts.
			want, keys, prompt = 0, nil, ""
			continue
		}
		if m := optionRe.FindStringSubmatch(body); m != nil {
			if n := m[1]; n == string(rune('1'+want)) {
				if want == 0 {
					prompt = lastPlain
				}
				want++
				keys = append(keys, n)
				continue
			}
			// A number out of sequence is not this menu — restart the run
			// at it if it is a fresh "1.", else drop the run.
			want, keys, prompt = 0, nil, ""
			if m[1] == "1" {
				want, keys, prompt = 1, []string{"1"}, lastPlain
			}
			continue
		}
		if yesNoRe.MatchString(body) {
			return Picker{Detected: true, Keys: []string{"y", "n", "esc"}, Prompt: strings.TrimSpace(body)}
		}
		if t := strings.TrimSpace(body); t != "" {
			lastPlain = t
		}
	}
	if want < 2 {
		return Picker{}
	}
	return Picker{Detected: true, Keys: append(keys, "esc"), Prompt: prompt}
}

// boxBody returns a line's content with its left border stripped, and
// whether the line was inside a box at all. A border-only line (the top or
// bottom rule) counts as inside — it is part of the same box — so a modal
// whose question sits under its own rule still reads as one run.
func boxBody(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if t == "" {
		return "", false
	}
	r := []rune(t)
	if strings.ContainsRune(boxSides, r[0]) {
		return strings.TrimRight(string(r[1:]), " \t"+boxSides), true
	}
	if strings.ContainsRune("╭╮╰╯┌┐└┘─━", r[0]) {
		return "", true
	}
	return "", false
}

// ValidKey gates what the phone may send raw. One key per tap, from the set
// a modal actually reads — never a newline, which would SUBMIT (that is
// `send`'s job, and its verified submit is why that verb exists), and never
// a free-form string, which is how a raw-key endpoint turns into a way to
// type anything into somebody's agent without the relay's verification.
func ValidKey(key string) bool {
	switch key {
	case "1", "2", "3", "4", "5", "6", "7", "8", "9", "y", "n", "esc":
		return true
	}
	return false
}

// KeyLiteral maps a UI key onto the characters tmux send-keys -l delivers.
// Only Esc needs translating; a digit is itself.
func KeyLiteral(key string) string {
	if key == "esc" {
		return "\x1b"
	}
	return key
}
