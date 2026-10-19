package chatweb_test

// grove-531: a hidden chat's pane used to be 80 columns wide (a detached
// session's default 80x24); it is now pinned to the cockpit's focus slot,
// about 139x50. Every pane read the phone makes — the list row's `waiting`,
// the stream's `turn` and `picker`, the send gate — goes through the same
// one capture (tmux.CapturePane, `capture-pane -p -J`), so the question is
// whether one Claude Code state reads the same at both sizes.
//
// testdata/cc2.1.303-<state>-w80.txt / -w139.txt are live Claude Code
// 2.1.303 panes captured on an isolated tmux server (tmux 3.6a). Most pairs
// are ONE claude, captured at 80x24, then resized to 139x50 and captured
// again (Claude Code redraws on SIGWINCH), so both files are the same
// state; trust, trust-down, idle, idle-typed and idle-typed-chrome are
// two fresh claudes started side by side at each size (deterministic
// screens); running and streaming are both mid-turn but a second apart.
// Trailing spaces are stripped and the operator's name is replaced with a
// same-length stand-in, so the wrapping is exactly what tmux returned.
//
// Each capture is read exactly as tmux.CapturePane hands it over — run()
// trims the output, so the blank rows under a modal drawn at the top of a
// tall pane never reach DetectPicker's 40-line lookback.
//
// The slot-width capture must read CORRECTLY, and the 80-column one the
// same — except where the spike found 80 columns already misread (w80
// below, pre-existing: the wider pane fixes them). Two readings are wrong
// at BOTH widths, identically (bug below): a width change does not touch
// them, so only their width-invariance is asserted here.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/chatweb"
)

func TestCaptureWidthDoesNotChangeTheReading(t *testing.T) {
	selectKeys := []string{"up", "down", "enter", "esc"}
	trust := func(caret string) chatweb.Picker {
		return chatweb.Picker{
			Detected: true, Kind: "select", Keys: selectKeys, Caret: caret,
			Prompt: "Quick safety check: Is this a project you created or one you trust?",
			Options: []chatweb.Option{
				{Key: "1", Label: "No, exit"},
				{Key: "2", Label: "Yes, I trust this folder"},
			},
		}
	}
	color := func(caret string, typing bool) chatweb.Picker {
		return chatweb.Picker{
			Detected: true, Kind: "menu", Keys: []string{"1", "2", "3", "4", "esc"},
			Prompt: "Which color do you prefer, red or blue?", Caret: caret, Typing: typing,
			Options: []chatweb.Option{
				{Key: "1", Label: "Red"},
				{Key: "2", Label: "Blue"},
				{Key: "3", Label: "Type something.", Free: true},
				{Key: "4", Label: "Chat about this"},
			},
		}
	}
	submit := func(review ...string) chatweb.Picker {
		return chatweb.Picker{
			Detected: true, Kind: "menu", Keys: []string{"1", "2", "tab", "esc"},
			Prompt: "Ready to submit your answers?", Caret: "1", Review: review,
			Options: []chatweb.Option{
				{Key: "1", Label: "Submit answers"},
				{Key: "2", Label: "Cancel"},
			},
		}
	}
	none := chatweb.Picker{}

	for _, c := range []struct {
		state   string
		turn    string
		waiting bool
		refused bool // SendRefusal != nil
		picker  chatweb.Picker
		w80     string // the 80-col ClassifyTurn state where 80 misreads (pre-existing)
		bug     string // the picker is wrong at both widths, identically (pre-existing)
	}{
		// The folder-trust dialog sits at the TOP of a fresh pane, every row
		// below it blank.
		{"trust", chatweb.TurnWaiting, true, true, trust("1"), "", ""},
		{"trust-down", chatweb.TurnWaiting, true, true, trust("2"), "", ""},
		{"idle", chatweb.TurnIdle, false, false, none, "", ""},
		// Unsent text in the box: one wrapped line at 80, one line at 139.
		{"idle-typed", chatweb.TurnIdle, false, false, none, "", ""},
		// Unsent text that wraps to THREE lines at 80 (two at 139): the ❯
		// line leaves the classifier's bottom-5 window.
		{"idle-typed-long", chatweb.TurnIdle, false, false, none, chatweb.TurnUnknown, ""},
		// Unsent text whose own words say "esc to interrupt": at 80 those
		// words wrap onto a continuation line typedRe does not blank.
		{"idle-typed-chrome", chatweb.TurnIdle, false, false, none, chatweb.TurnRunning, ""},
		{"perm", chatweb.TurnWaiting, true, true, chatweb.Picker{
			Detected: true, Kind: "menu", Keys: []string{"1", "2", "3", "4", "esc"},
			Prompt: "Do you want to proceed?", Caret: "1",
			Options: []chatweb.Option{
				{Key: "1", Label: "Yes"},
				{Key: "2", Label: "Yes, and always allow access to /private/tmp/claude-501/-Users-oprtr-git--worktrees-grove-grove-531-focus-swap-03-direct-attach/f2844754-ebee-45bc-b8d7-938d6c8c02bf/scratchpad/partb/repo from this project"},
				{Key: "3", Label: "Yes, and switch to auto mode · auto mode handles these prompts for you"},
				{Key: "4", Label: "No"},
			},
		}, "", "a wrapped option label is cut at its first wrap (w80 at …git--, w139 at …/f2844)"},
		{"running", chatweb.TurnRunning, false, false, none, "", ""},
		{"streaming", chatweb.TurnRunning, false, false, none, "", ""},
		// A reply that soft-wraps (overflowing the 80x24 screen; 14 lines at 139), turn over.
		{"longreply", chatweb.TurnIdle, false, false, none, "", ""},
		{"single", chatweb.TurnWaiting, true, true, color("1", false), "", ""},
		{"typesomething", chatweb.TurnWaiting, true, false, color("3", true), "", ""},
		{"multi", chatweb.TurnWaiting, true, true, chatweb.Picker{
			Detected: true, Kind: "multi", Keys: []string{"1", "2", "3", "5", "tab", "esc"},
			Prompt: "Which fruits do you like?", Caret: "1",
			Options: []chatweb.Option{
				{Key: "1", Label: "Apple"},
				{Key: "2", Label: "Banana"},
				{Key: "3", Label: "Cherry"},
				{Key: "5", Label: "Chat about this"},
			},
		}, "", ""},
		{"review", chatweb.TurnWaiting, true, true, submit(
			"Which fruits do you like? → Banana",
			"Which drink do you prefer, tea or coffee? → Coffee",
			"Which season do you prefer, summer or winter? → Summer",
			"How do you usually get around, bike or train? → Train",
		), "", ""},
		// A question long enough to wrap at 80. 2.1.303 draws the review
		// page's current question as "│ ● …" (a bar at its left).
		{"reviewlong", chatweb.TurnWaiting, true, true, submit(
			"When you think about how you would most like to spend a long, quiet weekend away from screens and work, which of these do you prefer? → Reading",
			"Tea or coffee? → Tea",
		), "", "reviewPicks skips 2.1.303's \"│ ● …\" current question: Review is [Reading, Tea or coffee? → Tea]"},
	} {
		t.Run(c.state, func(t *testing.T) {
			type reading struct {
				Turn    chatweb.Turn
				Waiting bool
				Refused bool
				Picker  chatweb.Picker
			}
			read := func(w string) reading {
				raw := strings.TrimSpace(capture(t, "cc2.1.303-"+c.state+"-"+w+".txt"))
				return reading{
					Turn:    chatweb.ClassifyTurn(raw, true),
					Waiting: chatweb.Waiting(raw),
					Refused: chatweb.SendRefusal(raw) != nil,
					Picker:  chatweb.WithReview(chatweb.DetectPicker(raw), raw),
				}
			}
			want := reading{Turn: chatweb.Turn{State: c.turn}, Waiting: c.waiting, Refused: c.refused, Picker: c.picker}
			w80, w139 := read("w80"), read("w139")
			if c.w80 != "" {
				if w80.Turn.State != c.w80 {
					t.Errorf("w80: ClassifyTurn = %+v, the spike recorded %q — update the note", w80.Turn, c.w80)
				}
				w80.Turn = w139.Turn
			}
			if c.bug != "" && len(w80.Picker.Options) == len(w139.Picker.Options) {
				// A label cut at its first wrap is cut later in a wider pane:
				// the slot-width label must extend the 80-column one. The key
				// the phone sends is the same either way.
				w80.Picker.Options = append([]chatweb.Option(nil), w80.Picker.Options...)
				for i, o := range w80.Picker.Options {
					if l := w139.Picker.Options[i].Label; o.Label != l && strings.HasPrefix(l, o.Label) {
						w80.Picker.Options[i].Label = l
					}
				}
			}
			if !reflect.DeepEqual(w80, w139) {
				t.Errorf("80 and 139 columns disagree:\n w80: %+v\nw139: %+v", w80, w139)
			}
			for w, got := range map[string]reading{"w80": w80, "w139": w139} {
				if got.Turn != want.Turn {
					t.Errorf("%s: ClassifyTurn = %+v, want %+v", w, got.Turn, want.Turn)
				}
				if got.Waiting != want.Waiting {
					t.Errorf("%s: Waiting = %v, want %v", w, got.Waiting, want.Waiting)
				}
				if got.Refused != want.Refused {
					t.Errorf("%s: SendRefusal refused = %v, want %v", w, got.Refused, want.Refused)
				}
				if c.bug == "" && !reflect.DeepEqual(got.Picker, want.Picker) {
					t.Errorf("%s: DetectPicker+WithReview =\n %+v\nwant\n %+v", w, got.Picker, want.Picker)
				}
			}
		})
	}
}
