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
//
// grove-308: Claude Code v2.1.282 draws its modals WITHOUT a │ box — the
// permission prompt and the AskUserQuestion menu are bare lines between
// two ─ rules (testdata/cc2.1.282-*.txt are the captures). The boxed rule
// below is kept for the older chrome; the unboxed rule is anchored on what
// the transcript never has: a ❯ caret ON an option, plus the modal's own
// chrome (the "Esc to cancel" footer, or AskUserQuestion's tab bar).

import (
	"reflect"
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
	// Kind is "menu" (numbered, a digit answers), "multi" (checkboxes, a
	// digit toggles — AskUserQuestion multiSelect) or "yesno". Additive
	// (grove-308): a client that ignores it still has Keys.
	Kind string `json:"kind,omitempty"`
	// Options are the numbered rows with their labels, so the phone can
	// show "Banana" instead of a bare "2". Each Key is also in Keys.
	Options []Option `json:"options,omitempty"`
	// Typing is true when the caret sits on AskUserQuestion's free-text
	// row: the menu is now a text input, and the composer's verified send
	// (paste + Enter) is what answers it.
	Typing bool `json:"typing,omitempty"`
}

// Option is one numbered row of a menu.
type Option struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Checked is a multi-select row's state ("[✔]").
	Checked bool `json:"checked,omitempty"`
	// Free marks AskUserQuestion's "Type something." row: its digit moves
	// the caret into a text input rather than answering.
	Free bool `json:"free,omitempty"`
}

// boxSides are the runes a pane's bordered box draws its left edge with.
// Requiring one is what separates a picker's options from a markdown
// numbered list the assistant printed into the transcript above it — the
// transcript is not boxed, the modal is.
const boxSides = "│┃"

// optionRe matches one option line: an optional selection caret, a single
// digit, a dot, then text. Anchored at the start of the line's CONTENT, so
// "tell me about 1. the backlog" — a digit mid-prose — never matches.
var optionRe = regexp.MustCompile(`^\s*([❯›>]\s*)?([1-9])\.\s+(\S.*)$`)

// checkRe is a multi-select row's checkbox, at the front of its label.
var checkRe = regexp.MustCompile(`^\[([ ✔✓xX])\]\s*`)

// freeRe is AskUserQuestion's free-text row.
var freeRe = regexp.MustCompile(`(?i)^type something\.?$`)

// tabBarRe is AskUserQuestion's header when it has more than one page (a
// multi-select, or several questions): `←  ☐ Pet  ☐ Drink  ✔ Submit  →`.
// Tab walks it — the only way to reach a multi-select's Submit page.
var tabBarRe = regexp.MustCompile(`^\s*←.*✔\s*Submit.*→\s*$`)

// yesNoRe is the older prompt shape, kept because it costs one line and a
// chat that hits one is otherwise unanswerable from a phone.
var yesNoRe = regexp.MustCompile(`\(\s*y\s*/\s*n\s*\)`)

// pickerLookback is how much of the capture's tail to consider. A modal is
// always at the bottom of the pane; scanning the whole scrollback would
// match a picker the operator answered ten minutes ago.
const pickerLookback = 40

// descIndent is how far a line must be indented to count as an option's
// description or wrapped continuation rather than the end of the menu.
const descIndent = 3

// run is one candidate menu: consecutive options 1..n.
type run struct {
	opts   []Option
	caret  string // key the ❯ sits on, "" if none
	boxed  bool
	prompt string
	start  int // line index of "1."
	end    int // line index of the last line that belonged to it
}

// DetectPicker reads a pane capture for a modal prompt.
//
// The rule, deliberately narrow: the bottom-most run of option lines must
// number 1, 2, … n CONSECUTIVELY with n ≥ 2 — one line starting "1." is a
// sentence; a run of them is a menu — AND be a modal's, not the
// transcript's: either inside a │ box (older chrome), or carrying the ❯
// caret with the modal's chrome around it (v2.1.282+).
func DetectPicker(capture string) Picker {
	lines := strings.Split(capture, "\n")
	if len(lines) > pickerLookback {
		lines = lines[len(lines)-pickerLookback:]
	}
	var cur, last *run
	var lastPlain string
	closeRun := func() {
		if cur != nil && len(cur.opts) >= 2 {
			last = cur
		}
		cur = nil
	}
	for i, raw := range lines {
		body, kind := classify(raw)
		switch kind {
		case lineBlank:
			// Options of one modal are contiguous.
			closeRun()
			continue
		case lineRule:
			// A border or a ─ rule sits inside a modal (v2.1.282 puts one
			// between "Type something" and "Chat about this").
			continue
		}
		boxed := kind == lineBoxed
		if cur != nil && cur.boxed != boxed {
			// Two boxes, or a box and bare text, are two prompts.
			closeRun()
		}
		if boxed && yesNoRe.MatchString(body) {
			return Picker{Detected: true, Kind: "yesno", Keys: []string{"y", "n", "esc"}, Prompt: strings.TrimSpace(body)}
		}
		if m := optionRe.FindStringSubmatch(body); m != nil {
			n := m[2]
			if cur == nil || n != string(rune('1'+len(cur.opts))) {
				// Out of sequence is not this menu — restart the run at it
				// if it is a fresh "1.", else drop the run.
				closeRun()
				if n != "1" {
					continue
				}
				cur = &run{boxed: boxed, prompt: lastPlain, start: i}
			}
			cur.opts = append(cur.opts, option(n, m[3]))
			cur.end = i
			if m[1] != "" {
				cur.caret = n
			}
			continue
		}
		t := strings.TrimSpace(body)
		if cur != nil {
			if boxed || indent(body) >= descIndent {
				// An option's description, or its wrapped tail.
				cur.end = i
				continue
			}
			closeRun()
		}
		if t != "" {
			lastPlain = t
		}
	}
	closeRun()
	if last == nil || !last.boxed && !modalChrome(lines, last) {
		return Picker{}
	}
	return last.picker(lines)
}

// modalChrome is the unboxed rule's anchor: the caret is ON an option (a
// transcript list never has one), and the modal's own chrome surrounds it —
// "Esc to cancel" below, or the AskUserQuestion tab bar above (its Submit
// page has no footer).
func modalChrome(lines []string, r *run) bool {
	if r.caret == "" {
		return false
	}
	for _, l := range lines[r.end+1:] {
		if strings.Contains(strings.ToLower(l), "esc to cancel") {
			return true
		}
	}
	return tabBar(lines[:r.start])
}

func tabBar(lines []string) bool {
	for _, l := range lines {
		if tabBarRe.MatchString(l) {
			return true
		}
	}
	return false
}

func (r *run) picker(lines []string) Picker {
	p := Picker{Detected: true, Kind: "menu", Prompt: r.prompt}
	for _, o := range r.opts {
		if checkRe.MatchString(o.Label) || o.Checked {
			p.Kind = "multi"
		}
	}
	for _, o := range r.opts {
		if o.Free && p.Kind == "multi" {
			// A multi-select's free row is a checkbox with no text input
			// behind it (its digit toggles, the caret stays put): nothing
			// a phone can fill, so it is not offered.
			continue
		}
		o.Label = checkRe.ReplaceAllString(o.Label, "")
		p.Options = append(p.Options, o)
		p.Keys = append(p.Keys, o.Key)
		if o.Free && r.caret == o.Key {
			p.Typing = true
		}
	}
	if tabBar(lines[:r.start]) {
		p.Keys = append(p.Keys, "tab")
	}
	p.Keys = append(p.Keys, "esc")
	return p
}

func option(key, label string) Option {
	label = strings.TrimSpace(label)
	o := Option{Key: key, Label: label}
	if m := checkRe.FindStringSubmatch(label); m != nil {
		o.Checked = m[1] != " "
		label = checkRe.ReplaceAllString(label, "")
	}
	o.Free = freeRe.MatchString(label)
	return o
}

func indent(s string) int {
	return len(s) - len(strings.TrimLeft(s, " \t"))
}

type lineKind int

const (
	lineBlank lineKind = iota
	lineRule
	lineBoxed
	linePlain
)

// classify returns a line's content with any left border stripped, and
// what kind of line it is. A border-only line (the top or bottom rule, or
// v2.1.282's bare ─ rule) is lineRule: part of whatever modal it sits in.
func classify(line string) (string, lineKind) {
	t := strings.TrimSpace(line)
	if t == "" {
		return "", lineBlank
	}
	r := []rune(t)
	if strings.ContainsRune(boxSides, r[0]) {
		return strings.TrimRight(string(r[1:]), " \t"+boxSides), lineBoxed
	}
	if strings.ContainsRune("╭╮╰╯┌┐└┘─━", r[0]) {
		return "", lineRule
	}
	return strings.TrimRight(line, " \t"), linePlain
}

// same is Picker's change test, so an unchanged modal is not re-sent every
// second (the phone re-renders its key row on every picker event). A
// multi-select toggle changes only an Option's Checked, and must re-send.
func (p Picker) same(o Picker) bool { return reflect.DeepEqual(p, o) }

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

// MenuKey is a key valid ONLY into a detected menu that offers it
// (grove-308): the guard moves from "which key" to "which key, into what",
// so the caller must check a FRESH capture's Keys before sending one. Tab
// walks AskUserQuestion's pages; in the bare input box it would do
// something else entirely.
//
// Enter and Space are deliberately absent. The v2.1.282 captures show a
// digit already answers a single-select and toggles a multi-select, and
// the Submit page is itself a numbered menu — so neither is needed, and
// Enter is exactly the key that submits whatever sits in an input box.
func MenuKey(key string) bool { return key == "tab" }

// KeyLiteral maps a UI key onto the characters tmux send-keys -l delivers.
// Esc and Tab need translating; a digit is itself.
func KeyLiteral(key string) string {
	switch key {
	case "esc":
		return "\x1b"
	case "tab":
		return "\t"
	}
	return key
}
