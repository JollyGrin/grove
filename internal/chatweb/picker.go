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
	// digit toggles — AskUserQuestion multiSelect), "select" (unnumbered:
	// ↑/↓ walk the caret, Enter confirms — grove-333) or "yesno".
	// Additive (grove-308): a client that ignores it still has Keys.
	Kind string `json:"kind,omitempty"`
	// Options are the numbered rows with their labels, so the phone can
	// show "Banana" instead of a bare "2". Each Key is also in Keys.
	Options []Option `json:"options,omitempty"`
	// Typing is true when the caret sits on AskUserQuestion's free-text
	// row: the menu is now a text input, and the composer's verified send
	// (paste + Enter) is what answers it.
	Typing bool `json:"typing,omitempty"`
	// Review is AskUserQuestion's Submit page's picks, one "question →
	// answer" per line (grove-334, review.go). Additive.
	Review []string `json:"review,omitempty"`
	// Caret is the Key of the option the ❯ sits on, "" when none is drawn
	// (grove-333, additive). A "select" menu is answered by walking the
	// caret, so the server needs to know where it starts.
	Caret string `json:"caret,omitempty"`
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
		if m := optionRe.FindStringSubmatch(body); m != nil && (boxed || !strings.HasPrefix(m[1], ">")) {
			// grove-318: unboxed, ">" is a markdown quote marker, not a
			// caret — v2.1.282's modals draw ❯ only.
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
		return detectSelect(lines)
	}
	return last.picker(lines)
}

// modalChrome is the unboxed rule's anchor: the caret is ON an option (a
// transcript list never has one), nothing after the run says the
// transcript went on, and the modal's own chrome surrounds it — "Esc to
// cancel" as one of the capture's last few lines, or the AskUserQuestion
// tab bar above (its Submit page has no footer).
//
// grove-318: the footer used to count anywhere below the run, so the
// operator's echoed "❯ 1. … 2. …" prompt fired on a reply that merely
// mentioned "Esc to cancel". A modal is the last thing on the pane: a ●
// line (the transcript continuing) or a ─ rule (the idle input box's)
// after the run means the run is not a modal.
func modalChrome(lines []string, r *run) bool {
	if r.caret == "" {
		return false
	}
	for _, l := range lines[r.end+1:] {
		if _, kind := classify(l); kind == lineRule || strings.HasPrefix(strings.TrimSpace(l), "●") {
			return false
		}
	}
	tail := 0
	for i := len(lines) - 1; i > r.end && tail < footerLookback; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		tail++
		if strings.Contains(strings.ToLower(lines[i]), "esc to cancel") {
			return true
		}
	}
	return tabBar(lines[:r.start])
}

// footerLookback is how many of the capture's last non-blank lines may
// carry a modal's "Esc to cancel" footer. The v2.1.282 captures put it on
// the very last one; the slack is for a status line drawn beneath.
const footerLookback = 3

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
	p.Caret = r.caret
	p.Keys = append(p.Keys, "esc")
	return p
}

// selectFooterRe is an unnumbered menu's footer (grove-333): the
// folder-trust dialog draws "Enter to confirm · Esc to cancel", and the
// shared Select component "Enter to select … Esc to cancel".
var selectFooterRe = regexp.MustCompile(`(?i)enter to (confirm|select).*esc to cancel`)

// selectPromptLookback is how far above an unnumbered menu its question
// may sit — the trust dialog puts two paragraphs and a link between them.
const selectPromptLookback = 12

// detectSelect is the unnumbered menu (grove-333) — Claude Code's
// folder-trust dialog, which fires on the FIRST chat in every new
// directory, i.e. exactly a phone-spawned chat:
//
//	❯ No, exit
//	  Yes, I trust this folder
//
//	Enter to confirm · Esc to cancel
//
// No digits to anchor on, so the rule is all chrome, and narrower than the
// numbered one: the capture's LAST non-blank line is the footer (nothing
// below it, so no transcript and no idle input box); directly above it,
// past blanks, a run of ≥ 2 lines with exactly ONE ❯ caret; and every
// other row's text starts in the caret row's label column. A transcript
// never ends on that footer, and an echoed "❯ prompt" has no aligned run.
func detectSelect(lines []string) Picker {
	i := len(lines) - 1
	for i >= 0 && strings.TrimSpace(lines[i]) == "" {
		i--
	}
	if i < 0 || !selectFooterRe.MatchString(lines[i]) {
		return Picker{}
	}
	i--
	for i >= 0 && strings.TrimSpace(lines[i]) == "" {
		i--
	}
	end := i
	for i >= 0 && strings.TrimSpace(lines[i]) != "" {
		i--
	}
	block := lines[i+1 : end+1]
	col, caret := -1, -1
	for j, l := range block {
		t := strings.TrimLeft(l, " ")
		if rest, ok := strings.CutPrefix(t, "❯"); ok {
			if caret >= 0 {
				return Picker{}
			}
			caret = j
			body := strings.TrimLeft(rest, " \u00a0")
			col = len([]rune(l)) - len([]rune(body))
		}
	}
	if caret < 0 || len(block) < 2 {
		return Picker{}
	}
	p := Picker{Detected: true, Kind: "select", Keys: []string{"up", "down", "enter", "esc"}}
	for j, l := range block {
		label := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "❯"))
		if j != caret && indent(l) != col || label == "" || optionRe.MatchString(l) {
			// Misaligned, or numbered (the numbered rule's job): not
			// this shape — refuse rather than guess.
			return Picker{}
		}
		key := string(rune('1' + len(p.Options)))
		if len(p.Options) == 9 {
			return Picker{}
		}
		p.Options = append(p.Options, Option{Key: key, Label: strings.TrimLeft(label, "\u00a0 ")})
		if j == caret {
			p.Caret = key
		}
	}
	p.Prompt = selectPrompt(lines[max(0, i-selectPromptLookback) : i+1])
	return p
}

// selectPrompt is the question above an unnumbered menu: the nearest
// sentence ending in "?" (cut there — the trust dialog's wraps on into a
// parenthetical), else the nearest non-blank line.
func selectPrompt(above []string) string {
	nearest := ""
	for j := len(above) - 1; j >= 0; j-- {
		t := strings.TrimSpace(above[j])
		if t == "" {
			continue
		}
		if nearest == "" {
			nearest = t
		}
		if q := strings.Index(t, "?"); q >= 0 {
			return t[:q+1]
		}
	}
	return nearest
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
// Space is deliberately absent. The v2.1.282 captures show a digit
// already answers a single-select and toggles a multi-select, and the
// Submit page is itself a numbered menu — so it is not needed. Enter is
// exactly the key that submits whatever sits in an input box, which is
// why a numbered menu never offers it.
//
// grove-333: up, down and enter join it, for the unnumbered "select" menu
// (the folder-trust dialog), where digits do nothing and the caret is the
// only way to choose. Still gated the same way: only a fresh capture whose
// picker OFFERS them lets them through, so Enter can never reach an idle
// input box.
func MenuKey(key string) bool {
	switch key {
	case "tab", "up", "down", "enter":
		return true
	}
	return false
}

// KeyLiteral maps a UI key onto the characters tmux send-keys -l delivers.
// Esc and Tab need translating; a digit is itself.
func KeyLiteral(key string) string {
	switch key {
	case "esc":
		return "\x1b"
	case "tab":
		return "\t"
	case "up":
		return "\x1b[A"
	case "down":
		return "\x1b[B"
	case "enter":
		return "\r"
	}
	return key
}
