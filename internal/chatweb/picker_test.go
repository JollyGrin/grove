package chatweb_test

// The detector is falsified BEFORE it is trusted (LEARNINGS: a probe you
// have not tried on a known-negative case is a probe that fires on
// everything). Every case below is a pane shape that really occurs, and
// half of them must NOT fire.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/chatweb"
)

// permissionPrompt is the shape that matters most: Claude Code asking to
// run a command. Answering it from a phone is the whole reason the raw-key
// row exists.
const permissionPrompt = `● I'll check the backlog.

╭──────────────────────────────────────────────╮
│ Bash command                                 │
│                                              │
│   gv ls --json                               │
│                                              │
│ Do you want to proceed?                      │
│ ❯ 1. Yes                                     │
│   2. Yes, and don't ask again for gv commands│
│   3. No, and tell Claude what to do instead  │
╰──────────────────────────────────────────────╯`

// idleBox is the ordinary input box: no modal, nothing to answer.
const idleBox = `● Done — three tickets triaged.

╭──────────────────────────────────────────────╮
│ >                                            │
╰──────────────────────────────────────────────╯
  ? for shortcuts`

// numberedProse is the false positive that would ruin this: the assistant
// printed a markdown list. It is in the TRANSCRIPT, above the box, not
// inside it — which is exactly the distinction the detector draws.
const numberedProse = `● Three things stand out:

1. grove-215's resolver pairs on mtime
2. grove-216 has no picker path
3. grove-218 needs both

╭──────────────────────────────────────────────╮
│ >                                            │
╰──────────────────────────────────────────────╯`

// typedDigits is the other false positive: the operator's own half-written
// message, inside the box, mentioning a number mid-sentence.
const typedDigits = `╭──────────────────────────────────────────────╮
│ > tell me about 1. the resolver and 2. the   │
│   picker                                     │
╰──────────────────────────────────────────────╯`

func TestDetectPicker(t *testing.T) {
	cases := []struct {
		name    string
		capture string
		want    []string
	}{
		{"permission prompt", permissionPrompt, []string{"1", "2", "3", "esc"}},
		{"idle input box", idleBox, nil},
		{"markdown list above the box", numberedProse, nil},
		{"digits typed mid-sentence", typedDigits, nil},
		{"empty pane", "", nil},
		{"a shell, no chrome at all", "$ ", nil},
		{"two options is a menu", "╭──╮\n│ Pick:  │\n│ 1. yes │\n│ 2. no  │\n╰──╯", []string{"1", "2", "esc"}},
		// One boxed line starting "1." is a sentence, not a menu.
		{"a single numbered line is not a menu", "╭──╮\n│ 1. run the gate first │\n╰──╯", nil},
		// Out of sequence: not one menu's options.
		{"non-consecutive numbers", "╭──╮\n│ 1. yes │\n│ 3. no  │\n╰──╯", nil},
		{"yes/no prompt", "╭──╮\n│ Continue? (y/n) │\n╰──╯", []string{"y", "n", "esc"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := chatweb.DetectPicker(c.capture)
			if c.want == nil {
				if got.Detected {
					t.Fatalf("must NOT fire, got %+v", got)
				}
				return
			}
			if !got.Detected {
				t.Fatalf("must fire, got %+v", got)
			}
			if !reflect.DeepEqual(got.Keys, c.want) {
				t.Fatalf("keys = %v, want %v", got.Keys, c.want)
			}
		})
	}
}

// The prompt labels the raw-key row, so a phone shows the QUESTION rather
// than three unexplained buttons.
func TestDetectPickerPrompt(t *testing.T) {
	if got := chatweb.DetectPicker(permissionPrompt).Prompt; got != "Do you want to proceed?" {
		t.Errorf("prompt = %q, want the modal's question", got)
	}
}

func TestValidKeyAndLiteral(t *testing.T) {
	for _, ok := range []string{"1", "9", "y", "n", "esc"} {
		if !chatweb.ValidKey(ok) {
			t.Errorf("ValidKey(%q) = false, want true", ok)
		}
	}
	// A raw-key endpoint that took free text would be a way to type into
	// somebody's agent while skipping the relay's verified submit — and a
	// newline would SUBMIT it.
	for _, bad := range []string{"", "0", "10", "yes", "\n", "\r", "gv done", "Enter", "esc\n", "Y"} {
		if chatweb.ValidKey(bad) {
			t.Errorf("ValidKey(%q) = true, want false", bad)
		}
	}
	if chatweb.KeyLiteral("esc") != "\x1b" {
		t.Error("esc must map to the escape character tmux send-keys -l delivers")
	}
	if chatweb.KeyLiteral("3") != "3" {
		t.Error("a digit is itself")
	}
}

// --- grove-308: the v2.1.282 chrome, from real captures ---
//
// testdata/cc2.1.282-*.txt are `tmux capture-pane -p` of a scratch Claude
// Code v2.1.282 session (2026-09-25). None of them has a │ box: the modal
// is bare lines between ─ rules, which the boxed rule above never saw.

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "cc2.1.282-"+name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func labels(p chatweb.Picker) []string {
	var out []string
	for _, o := range p.Options {
		s := o.Key + ":" + o.Label
		if o.Checked {
			s += "[x]"
		}
		if o.Free {
			s += "(free)"
		}
		out = append(out, s)
	}
	return out
}

func TestDetectPickerCaptures(t *testing.T) {
	cases := []struct {
		fixture, kind, prompt string
		keys, labels          []string
		typing                bool
	}{
		{"single", "menu", "Which fruit do you prefer?",
			[]string{"1", "2", "3", "4", "esc"},
			[]string{"1:Apple", "2:Banana", "3:Type something.(free)", "4:Chat about this"}, false},
		// Two questions: a tab bar, so Tab is offered to walk the pages.
		{"twoq", "menu", "Which pet do you prefer?",
			[]string{"1", "2", "3", "4", "tab", "esc"},
			[]string{"1:Cat", "2:Dog", "3:Type something.(free)", "4:Chat about this"}, false},
		// The caret on the free row: the composer answers it.
		{"typesomething", "menu", "Which drink do you prefer?",
			[]string{"1", "2", "3", "4", "tab", "esc"},
			[]string{"1:Tea", "2:Coffee", "3:Type something.(free)", "4:Chat about this"}, true},
		// Multi-select: digits toggle, the free row (a checkbox with no
		// input behind it) is hidden, Tab reaches Submit.
		{"multi", "multi", "Which colors do you like?",
			[]string{"1", "2", "3", "5", "tab", "esc"},
			[]string{"1:Red", "2:Green", "3:Blue", "5:Chat about this"}, false},
		{"multi-toggled", "multi", "Which colors do you like?",
			[]string{"1", "2", "3", "5", "tab", "esc"},
			[]string{"1:Red[x]", "2:Green", "3:Blue[x]", "5:Chat about this"}, false},
		// The Submit page has no footer; the tab bar anchors it.
		{"multi-review", "menu", "Ready to submit your answers?",
			[]string{"1", "2", "tab", "esc"},
			[]string{"1:Submit answers", "2:Cancel"}, false},
		// The permission prompt lost its box too; wrapped option text is a
		// continuation, not the end of the menu.
		{"perm", "menu", "Do you want to proceed?",
			[]string{"1", "2", "3", "4", "esc"}, nil, false},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			got := chatweb.DetectPicker(fixture(t, c.fixture))
			if !got.Detected {
				t.Fatalf("must fire, got %+v", got)
			}
			if got.Kind != c.kind || got.Prompt != c.prompt || got.Typing != c.typing {
				t.Errorf("kind/prompt/typing = %q/%q/%v, want %q/%q/%v", got.Kind, got.Prompt, got.Typing, c.kind, c.prompt, c.typing)
			}
			if !reflect.DeepEqual(got.Keys, c.keys) {
				t.Errorf("keys = %v, want %v", got.Keys, c.keys)
			}
			if c.labels != nil && !reflect.DeepEqual(labels(got), c.labels) {
				t.Errorf("options = %v, want %v", labels(got), c.labels)
			}
		})
	}
}

// The v2.1.282 idle screen: the input box is two ─ rules around "❯ ",
// with no │ anywhere — every negative below sits on top of it.
const idleV2 = `
✻ Churned for 2s · done 8:13 AM

                                                                                  ● high · /effort
────────────────────────────────────────────────────────────────────────────────────────────────────
❯ 
────────────────────────────────────────────────────────────────────────────────────────────────────
  ⏸ manual mode on · ← for agents`

func TestDetectPickerV2Negatives(t *testing.T) {
	cases := map[string]string{
		"idle":                        idleV2,
		"markdown list above the box": "● Three things stand out:\n\n1. grove-215's resolver\n2. grove-216 has no picker path\n3. grove-218 needs both\n" + idleV2,
		// The operator's own message echoed into the transcript carries a
		// caret — but no modal chrome follows it.
		"echoed numbered prompt": "❯ 1. run the gate\n  2. open the PR\n\n● On it.\n" + idleV2,
		"answered menu":          "● User answered Claude's questions:\n  ⎿  · Which fruit do you prefer? → Banana\n\n● Banana it is.\n" + idleV2,
		// A list mid-turn: "esc to interrupt" is not "esc to cancel".
		"list while working":        "● Plan:\n  1. read\n  2. write\n\n✽ Enchanting… (4s · esc to interrupt)\n" + idleV2,
		"digits typed into the box": strings.Replace(idleV2, "❯ \n", "❯ 1. the resolver and 2. the picker\n", 1),
		"y/n in the transcript":     "● Shall I? (y/n)\n" + idleV2,
		// grove-318: the three probes that fired on PR #314's rule. A reply
		// that mentions the footer is transcript, not chrome.
		"echoed prompt, reply mentions the footer": "❯ 1. run the gate\n  2. open the PR\n\n● Done. The footer reads Esc to cancel.\n" + idleV2,
		"assistant list with > markers":            "● Options:\n> 1. foo\n> 2. bar\n\nEsc to cancel",
		"digits typed into the box above the footer": strings.Replace(
			strings.Replace(idleV2, "❯ \n", "❯ 1. a\n  2. b\n", 1),
			"⏸ manual mode on", "⏸ Esc to cancel", 1),
	}
	for name, capture := range cases {
		t.Run(name, func(t *testing.T) {
			if got := chatweb.DetectPicker(capture); got.Detected {
				t.Fatalf("must NOT fire, got %+v", got)
			}
		})
	}
}

// The event is additive: a grove-218 client reads detected/keys/prompt and
// never sees a renamed field.
func TestPickerJSONIsAdditive(t *testing.T) {
	raw, err := json.Marshal(chatweb.DetectPicker(fixture(t, "single")))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{`"detected":true`, `"keys":[`, `"prompt":"Which fruit`, `"kind":"menu"`, `"options":[`} {
		if !strings.Contains(string(raw), f) {
			t.Errorf("picker JSON %s lacks %s", raw, f)
		}
	}
}

func TestMenuKey(t *testing.T) {
	if !chatweb.MenuKey("tab") || chatweb.KeyLiteral("tab") != "\t" {
		t.Error("tab is a menu key, sent as a literal tab")
	}
	for k, lit := range map[string]string{"up": "\x1b[A", "down": "\x1b[B", "enter": "\r"} {
		if !chatweb.MenuKey(k) || chatweb.KeyLiteral(k) != lit {
			t.Errorf("%s is a menu key (grove-333), sent as %q", k, lit)
		}
	}
	for _, k := range []string{"space", "\r", "\n", " ", "1", "esc"} {
		if chatweb.MenuKey(k) {
			t.Errorf("MenuKey(%q) = true", k)
		}
	}
}

// grove-333: Claude Code's folder-trust dialog (2.1.283, captured on an
// isolated tmux server) is an UNNUMBERED menu — a ❯ caret, no digits, and
// an "Enter to confirm · Esc to cancel" footer. Its default is "No, exit".
func TestDetectPickerTrustDialog(t *testing.T) {
	for name, caret := range map[string]string{"trust": "1", "trust-down": "2"} {
		raw, err := os.ReadFile(filepath.Join("testdata", "cc2.1.283-"+name+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		p := chatweb.DetectPicker(string(raw))
		if !p.Detected || p.Kind != "select" || p.Caret != caret {
			t.Fatalf("%s: %+v, want a select with the caret on %s", name, p, caret)
		}
		if len(p.Options) != 2 || p.Options[0].Label != "No, exit" || p.Options[1].Label != "Yes, I trust this folder" {
			t.Errorf("%s: options %+v", name, p.Options)
		}
		if !reflect.DeepEqual(p.Keys, []string{"up", "down", "enter", "esc"}) {
			t.Errorf("%s: keys %q", name, p.Keys)
		}
		if !strings.HasSuffix(p.Prompt, "one you trust?") {
			t.Errorf("%s: prompt %q", name, p.Prompt)
		}
		if !chatweb.Waiting(string(raw)) {
			t.Errorf("%s: Waiting = false — the list row would say it is ready", name)
		}
	}
}

// The select rule's anti-false-positives: it needs the footer as the LAST
// line, one caret, and aligned rows.
func TestDetectSelectDoesNotFire(t *testing.T) {
	for name, c := range map[string]string{
		"echoed prompt, idle box": "❯ No, exit\n  Yes, I trust this folder\n\nEnter to confirm · Esc to cancel\n● ok\n────\n❯ \n────\n  ? for shortcuts",
		"footer not last":         " ❯ a\n   b\n\n Enter to confirm · Esc to cancel\n more prose",
		"two carets":              " ❯ a\n ❯ b\n\n Enter to confirm · Esc to cancel",
		"no caret":                "   a\n   b\n\n Enter to confirm · Esc to cancel",
		"misaligned row":          " ❯ a\n b\n\n Enter to confirm · Esc to cancel",
		"one row":                 " ❯ a\n\n Enter to confirm · Esc to cancel",
		"no esc":                  " ❯ a\n   b\n\n Enter to confirm",
	} {
		if p := chatweb.DetectPicker(c); p.Detected {
			t.Errorf("%s: detected %+v", name, p)
		}
	}
}
