package chatweb_test

// The detector is falsified BEFORE it is trusted (LEARNINGS: a probe you
// have not tried on a known-negative case is a probe that fires on
// everything). Every case below is a pane shape that really occurs, and
// half of them must NOT fire.

import (
	"reflect"
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
