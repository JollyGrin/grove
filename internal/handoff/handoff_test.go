package handoff

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fake is a scripted Runner that records every mutation in order.
type fake struct {
	info      *Info
	lookupErr error
	agents    []string // successive Agent() answers; last repeats
	live      bool
	verified  *Verified
	verifyErr error
	confirm   bool
	adoptErr  error
	untrackEr error
	tombErr   error

	calls   []string // mutations + the nudge, in order
	slept   time.Duration
	nudged  string
	planned string
}

func (f *fake) Lookup(string) (*Info, error) { return f.info, f.lookupErr }
func (f *fake) Agent(string) (string, bool, error) {
	a := f.agents[0]
	if len(f.agents) > 1 {
		f.agents = f.agents[1:]
	}
	return a, f.live, nil
}
func (f *fake) Nudge(_, text string) error {
	f.nudged = text
	f.calls = append(f.calls, "nudge")
	return nil
}
func (f *fake) Verify(*Info) (*Verified, error) { return f.verified, f.verifyErr }
func (f *fake) Confirm(plan string) (bool, error) {
	f.planned = plan
	return f.confirm, nil
}
func (f *fake) Untrack(_ string, rm bool) error {
	f.calls = append(f.calls, fmt.Sprintf("untrack rm=%v", rm))
	return f.untrackEr
}
func (f *fake) RemoteAdopt(host, ticket, repo, branch string) error {
	f.calls = append(f.calls, fmt.Sprintf("adopt %s %s %s %s", host, ticket, repo, branch))
	return f.adoptErr
}
func (f *fake) Tombstone(_, host, _ string) error {
	f.calls = append(f.calls, "tombstone "+host)
	return f.tombErr
}
func (f *fake) Sleep(d time.Duration) { f.slept += d }

func ready() *fake {
	return &fake{
		info:     &Info{Ticket: "grove-7", Repo: "grove", Branch: "grove-7-x", Worktree: "/wt", Agent: "idle", WindowLive: true},
		agents:   []string{"working", "working", "idle"},
		live:     true,
		verified: &Verified{OnOrigin: true, LocalHead: "abc", RemoteHead: "abc", PRNumber: 12, PRURL: "u", PRBody: strings.Repeat("x", 300)},
		confirm:  true,
	}
}

func opts() Options {
	return Options{Task: "grove-7", Host: "pc", Poll: time.Second, Timeout: 10 * time.Second}
}

func stepOf(t *testing.T, err error) Step {
	t.Helper()
	var he *Error
	if !errors.As(err, &he) {
		t.Fatalf("want *Error, got %v", err)
	}
	return he.Step
}

func TestHappyPath(t *testing.T) {
	f := ready()
	o := opts()
	o.SSH = "pc.tail"
	o.GV = "/opt/gv"
	var out strings.Builder
	if err := Run(f, o, &out); err != nil {
		t.Fatal(err)
	}
	want := []string{"nudge", "untrack rm=false", "adopt pc grove-7 grove grove-7-x", "tombstone pc"}
	if strings.Join(f.calls, ";") != strings.Join(want, ";") {
		t.Errorf("calls = %v, want %v", f.calls, want)
	}
	if f.nudged != CheckpointPrompt {
		t.Error("nudge did not carry the checkpoint template")
	}
	if f.slept != 2*time.Second {
		t.Errorf("slept %s, want 2s (two working polls)", f.slept)
	}
	// The follow command must use the host's real ssh target + gv path
	// (the config KEY is not an ssh destination) and resolve the window
	// remotely via `gv attach` — never a guessed local session label.
	if !strings.Contains(out.String(), "grove-7 → pc. Follow: ssh pc.tail -t /opt/gv attach grove-7") {
		t.Errorf("missing follow line:\n%s", out.String())
	}
	if !strings.Contains(f.planned, "ssh pc -- gv adopt grove-7 --repo grove --branch grove-7-x --sync") {
		t.Errorf("plan missing the adopt command:\n%s", f.planned)
	}
}

func TestGuardsAbortBeforeAnyMutation(t *testing.T) {
	cases := map[string]struct {
		mut  func(*fake)
		step Step
		msg  string
	}{
		"untracked": {func(f *fake) { f.info, f.lookupErr = nil, errors.New("no active task grove-7") }, StepGuard, ""},
		"mid-turn":  {func(f *fake) { f.info.Agent = "working" }, StepGuard, ""},
		"no branch": {func(f *fake) { f.info.Branch = ""; f.info.Paused = true; f.info.WindowLive = false }, StepGuard, ""},
		// Checkpoint outcomes that are NOT a finished checkpoint: only a
		// clean idle may proceed — waiting means the worker asked a
		// question instead, dead means the session died mid-turn, and
		// treating either as done would hand off with the checkpoint
		// never written (verify passes on any pre-existing PR body).
		"window died":            {func(f *fake) { f.live = false }, StepCheckpoint, ""},
		"timeout":                {func(f *fake) { f.agents = []string{"working"} }, StepCheckpoint, "still working"},
		"waiting mid-checkpoint": {func(f *fake) { f.agents = []string{"working", "waiting"} }, StepCheckpoint, "waiting"},
		"blocked mid-checkpoint": {func(f *fake) { f.agents = []string{"working", "blocked"} }, StepCheckpoint, "blocked"},
		"dead mid-checkpoint":    {func(f *fake) { f.agents = []string{"working", "dead"} }, StepCheckpoint, "dead"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := ready()
			tc.mut(f)
			err := Run(f, opts(), &strings.Builder{})
			if err == nil {
				t.Fatal("want abort")
			}
			for _, c := range f.calls {
				if c != "nudge" {
					t.Errorf("mutation %q happened after a guard failure", c)
				}
			}
			if step := stepOf(t, err); step != tc.step {
				t.Errorf("step = %s, want %s", step, tc.step)
			}
			if tc.msg != "" && !strings.Contains(err.Error(), tc.msg) {
				t.Errorf("message %q missing from: %v", tc.msg, err)
			}
		})
	}
}

func TestVerifyFailureAbortsBeforeUntrack(t *testing.T) {
	cases := map[string]func(*Verified){
		"not on origin": func(v *Verified) { v.OnOrigin = false },
		"unpushed":      func(v *Verified) { v.LocalHead = "def" },
		"dirty":         func(v *Verified) { v.Dirty = true },
		"no PR":         func(v *Verified) { v.PRNumber = 0 },
		"thin body":     func(v *Verified) { v.PRBody = "wip" },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			f := ready()
			mut(f.verified)
			err := Run(f, opts(), &strings.Builder{})
			if stepOf(t, err) != StepVerify {
				t.Fatalf("step = %v", err)
			}
			if len(f.calls) != 1 || f.calls[0] != "nudge" {
				t.Errorf("verify failure must not mutate: %v", f.calls)
			}
		})
	}
}

func TestDeclineAbortsBeforeUntrack(t *testing.T) {
	f := ready()
	f.confirm = false
	err := Run(f, opts(), &strings.Builder{})
	if !errors.Is(err, ErrDeclined) || stepOf(t, err) != StepConfirm {
		t.Fatalf("err = %v", err)
	}
	if len(f.calls) != 1 {
		t.Errorf("decline must not mutate: %v", f.calls)
	}
}

func TestYesSkipsConfirm(t *testing.T) {
	f := ready()
	f.confirm = false
	o := opts()
	o.Yes = true
	if err := Run(f, o, &strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	if f.planned != "" {
		t.Error("--yes must not prompt")
	}
}

func TestRemoteAdoptFailureLeavesNoTombstone(t *testing.T) {
	f := ready()
	f.adoptErr = errors.New("ssh: exit 255")
	err := Run(f, opts(), &strings.Builder{})
	if stepOf(t, err) != StepRemoteAdopt {
		t.Fatalf("err = %v", err)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "tombstone") {
			t.Error("tombstone written despite adopt failure")
		}
	}
	if !strings.Contains(strings.Join(f.calls, ";"), "untrack") {
		t.Error("task should have been untracked before the remote adopt")
	}
	msg := err.Error()
	if !strings.Contains(msg, "gv adopt grove-7 --repo grove --branch grove-7-x --sync --host pc") || !strings.Contains(msg, "untracked") {
		t.Errorf("retry hint missing:\n%s", msg)
	}
}

func TestNoLiveWindowSkipsCheckpoint(t *testing.T) {
	f := ready()
	f.info.WindowLive, f.info.Paused = false, true
	if err := Run(f, opts(), &strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	if f.nudged != "" {
		t.Error("nudged a paused worker")
	}
}

func TestReleaseStopsAfterUntrack(t *testing.T) {
	f := ready()
	o := opts()
	o.Release, o.Rm = true, true
	if err := Run(f, o, &strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	// NO tombstone on release: the puller writes it back (--tombstone-to)
	// only after its local adopt succeeds — otherwise a failed pull
	// strands the task untracked everywhere while this host claims it
	// moved (review finding 3).
	if strings.Join(f.calls, ";") != "nudge;untrack rm=true" {
		t.Errorf("calls = %v", f.calls)
	}
}

func TestBodyNonTrivial(t *testing.T) {
	headings := "## Goal\n## Done + verified\n## Verified surprises\n## Remaining\n## Next step"
	if !BodyNonTrivial(headings) {
		t.Error("five headings should pass")
	}
	if BodyNonTrivial("## Goal\n## Remaining") {
		t.Error("two headings, short body should fail")
	}
	if !BodyNonTrivial(strings.Repeat("a", 201)) {
		t.Error(">200 chars should pass")
	}
	if BodyNonTrivial("") {
		t.Error("empty should fail")
	}
}
