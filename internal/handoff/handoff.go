// Package handoff is the step sequencer behind `gv handoff` (grove-177,
// the remote-overflow train): move a running task from this host to a
// named remote grove host — "closing the lid, you take it" — by composing
// verbs that already exist: checkpoint nudge → verify → untrack → remote
// cold adopt. Decision logic lives here against a Runner seam so every
// abort point is unit-testable with a fake; cmd/gv wires the real one.
//
// The Claude session transcript does NOT travel (per-host ~/.claude); the
// handoff the worker writes into its PR body is the carrier by design —
// the remote's pickup-prompt session finds it there. `gv cost` rows for a
// handed-off task split across hosts.
package handoff

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// CheckpointPrompt is the context-rot-rescue handoff template sent via
// `gv nudge` (step 2). It asks for a durable, remote-readable state: WIP
// committed and pushed, a (draft) PR holding the five handoff headings,
// then the STATUS line so the idle detection fires.
const CheckpointPrompt = `Checkpoint now — this task is being handed to another machine and your session transcript will NOT follow it. Do exactly this, then stop:
1. Commit your WIP (a "wip:" commit is fine) and push the branch to origin.
2. If no PR exists for this branch, open a DRAFT PR against the base branch.
3. Write a handoff into the PR description with these five headings, in order: ## Goal (restated), ## Done + verified (what is done and how it was verified), ## Verified surprises (facts that were expensive to learn — not narrative), ## Remaining, ## Next step (the single next concrete action).
4. Make sure the worktree is clean (nothing uncommitted) and local == origin.
Then end your turn with your STATUS line.`

// handoffHeadings are the five headings the template asks for; the body
// check is a cheap heuristic (>= these five or >200 chars), not a parser.
var handoffHeadings = []string{"goal", "done", "surprises", "remaining", "next step"}

// MinBodyChars is the length fallback of the PR-body heuristic.
const MinBodyChars = 200

// BodyNonTrivial reports whether a PR body looks like it carries a
// handoff: either all five headings appear (case-insensitive) or the body
// is longer than MinBodyChars.
func BodyNonTrivial(body string) bool {
	b := strings.TrimSpace(body)
	if len(b) > MinBodyChars {
		return true
	}
	lower := strings.ToLower(b)
	for _, h := range handoffHeadings {
		if !strings.Contains(lower, h) {
			return false
		}
	}
	return b != ""
}

// Info is what the sequencer needs to know about the local task.
type Info struct {
	Ticket, Repo, Branch, Worktree string
	Agent                          string // state.Agent* value
	Paused, WindowLive             bool
}

// Verified holds the facts step 3 checks — all via git/gh, never ancestry.
type Verified struct {
	OnOrigin   bool   // branch exists on origin
	LocalHead  string // worktree HEAD sha
	RemoteHead string // origin/<branch> sha (ls-remote)
	Dirty      bool   // worktree has uncommitted changes
	PRNumber   int    // 0 = no PR
	PRURL      string
	PRBody     string
}

// Runner is the side-effect seam. Every method maps to an existing gv
// verb or a pure read; the sequencer owns the order and the aborts.
type Runner interface {
	Lookup(task string) (*Info, error)
	// Agent re-reads the live agent state + window liveness (idle wait).
	Agent(ticket string) (agent string, live bool, err error)
	Nudge(ticket, text string) error
	Verify(info *Info) (*Verified, error)
	// Confirm shows the plan and returns the operator's yes/no.
	Confirm(plan string) (bool, error)
	Untrack(ticket string, rm bool) error
	RemoteAdopt(host, ticket, repo, branch string) error
	Tombstone(ticket, host, branch string) error
	Sleep(d time.Duration)
}

// Options are the flags of `gv handoff --to`.
type Options struct {
	Task, Host   string
	Rm, Yes      bool
	NoCheckpoint bool          // skip step 2 (the worker already wrote its handoff)
	Timeout      time.Duration // idle wait bound (default 10 min)
	Poll         time.Duration // idle poll interval (default 2s)
	// Release stops after the local untrack — the remote half of
	// `gv handoff --from`, run over ssh. The caller does the cold adopt;
	// Host (if set) is the caller's name and becomes the tombstone.
	Release bool
	// Label names the remote cockpit session (grove-<label>) for the
	// follow line; Window is the worker window name on the remote.
	Label, Window string
}

// Step names the abort point an error belongs to.
type Step string

const (
	StepGuard       Step = "guard"
	StepCheckpoint  Step = "checkpoint"
	StepVerify      Step = "verify"
	StepConfirm     Step = "confirm"
	StepUntrack     Step = "untrack"
	StepRemoteAdopt Step = "remote-adopt"
)

// Error is a step-tagged failure so callers (and tests) know how far the
// sequence got before it stopped.
type Error struct {
	Step Step
	Err  error
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %v", e.Step, e.Err) }
func (e *Error) Unwrap() error { return e.Err }

// ErrDeclined is returned when the operator answers no at the confirm.
var ErrDeclined = errors.New("declined — nothing changed")

func fail(step Step, err error) error { return &Error{Step: step, Err: err} }

// Run executes the sequence; output goes to out. Nothing mutates before
// the confirm (steps 1–4 are reads plus one nudge); the tombstone is the
// LAST write, so a failed remote adopt leaves the task untracked with no
// tombstone and the retry command printed.
func Run(r Runner, o Options, out io.Writer) error {
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Minute
	}
	if o.Poll <= 0 {
		o.Poll = 2 * time.Second
	}

	// 1. Guard.
	info, err := r.Lookup(o.Task)
	if err != nil {
		return fail(StepGuard, err)
	}
	if info.Agent == "working" && info.WindowLive {
		return fail(StepGuard, fmt.Errorf("%s appears mid-turn (agent working) — wait for it to go idle, then retry", info.Ticket))
	}
	if info.Branch == "" {
		return fail(StepGuard, fmt.Errorf("%s has no branch — nothing a remote host could adopt", info.Ticket))
	}

	// 2. Checkpoint: nudge the worker with the handoff template and wait
	// for it to go idle. A paused/dead worker has nothing to nudge — its
	// last handoff (if any) is whatever verify finds in the PR.
	if !o.NoCheckpoint && info.WindowLive {
		fmt.Fprintf(out, "→ checkpoint: nudging %s with the handoff template (waiting up to %s for idle)\n", info.Ticket, o.Timeout)
		if err := r.Nudge(info.Ticket, CheckpointPrompt); err != nil {
			return fail(StepCheckpoint, err)
		}
		if err := waitIdle(r, info.Ticket, o); err != nil {
			return fail(StepCheckpoint, err)
		}
	} else if !info.WindowLive {
		fmt.Fprintf(out, "→ checkpoint skipped: %s has no live window (verify reads whatever the PR already holds)\n", info.Ticket)
	} else {
		fmt.Fprintf(out, "→ checkpoint skipped (--no-checkpoint)\n")
	}

	// 3. Verify.
	v, err := r.Verify(info)
	if err != nil {
		return fail(StepVerify, err)
	}
	if problems := Problems(v); len(problems) > 0 {
		return fail(StepVerify, fmt.Errorf("%s is not ready to hand off:\n  - %s", info.Ticket, strings.Join(problems, "\n  - ")))
	}

	// 4. Plan + confirm (propose, then dispose).
	plan := Plan(info, v, o)
	fmt.Fprint(out, plan)
	if !o.Yes {
		ok, err := r.Confirm(plan)
		if err != nil {
			return fail(StepConfirm, err)
		}
		if !ok {
			return fail(StepConfirm, ErrDeclined)
		}
	}

	// 5. Untrack locally (no --rm unless asked: the worktree stays as the
	// operator's hand-edit checkout; the runner still closes the window).
	if err := r.Untrack(info.Ticket, o.Rm); err != nil {
		return fail(StepUntrack, err)
	}
	if o.Release {
		if o.Host != "" {
			if err := r.Tombstone(info.Ticket, o.Host, info.Branch); err != nil {
				return fail(StepUntrack, err)
			}
		}
		fmt.Fprintf(out, "✓ %s released — untracked here; the caller adopts it\n", info.Ticket)
		return nil
	}

	// 6. Remote cold adopt. On failure: leave untracked, no tombstone,
	// print the exact retry.
	fmt.Fprintf(out, "→ ssh %s: gv adopt %s --repo %s --branch %s\n", o.Host, info.Ticket, info.Repo, info.Branch)
	if err := r.RemoteAdopt(o.Host, info.Ticket, info.Repo, info.Branch); err != nil {
		return fail(StepRemoteAdopt, fmt.Errorf("%w\n%s is now untracked here and NOT running on %s (no tombstone written).\n  retry remote:  gv adopt %s --repo %s --branch %s --host %s\n  or re-track locally:  gv adopt %s",
			err, info.Ticket, o.Host, info.Ticket, info.Repo, info.Branch, o.Host, info.Ticket))
	}
	if err := r.Tombstone(info.Ticket, o.Host, info.Branch); err != nil {
		return fail(StepRemoteAdopt, fmt.Errorf("remote adopt succeeded but the local tombstone failed: %w", err))
	}

	// 7. Follow line.
	fmt.Fprint(out, FollowLine(info.Ticket, o))
	return nil
}

// waitIdle polls the agent state until the worker leaves `working` (the
// nudge flips it to working; the Stop hook flips it back). A dead window
// aborts; the timeout aborts with the retry hint from the spec.
func waitIdle(r Runner, ticket string, o Options) error {
	deadline := time.Duration(0)
	for {
		agent, live, err := r.Agent(ticket)
		if err != nil {
			return err
		}
		if !live {
			return fmt.Errorf("%s's window died while checkpointing — `gv adopt %s` to revive it, then retry", ticket, ticket)
		}
		if agent != "working" && agent != "setup" {
			return nil
		}
		if deadline >= o.Timeout {
			return fmt.Errorf("%s is still working after %s — retry when it is idle (or pass --timeout)", ticket, o.Timeout)
		}
		r.Sleep(o.Poll)
		deadline += o.Poll
	}
}

// Problems lists every verify failure (all of them, so the operator fixes
// the lot in one pass). Empty means ready.
func Problems(v *Verified) []string {
	var p []string
	if !v.OnOrigin {
		p = append(p, "branch is not on origin — push it")
	} else if v.LocalHead != v.RemoteHead {
		p = append(p, fmt.Sprintf("local HEAD %s != origin %s — push (or pull) first", short(v.LocalHead), short(v.RemoteHead)))
	}
	if v.Dirty {
		p = append(p, "worktree has uncommitted changes — commit or stash them")
	}
	if v.PRNumber == 0 {
		p = append(p, "no PR for the branch — open a draft PR carrying the handoff")
	} else if !BodyNonTrivial(v.PRBody) {
		p = append(p, fmt.Sprintf("PR #%d body has no handoff (want the five headings or >%d chars)", v.PRNumber, MinBodyChars))
	}
	return p
}

// Plan renders the dry-run summary shown before the confirm.
func Plan(info *Info, v *Verified, o Options) string {
	var b strings.Builder
	if o.Release {
		fmt.Fprintf(&b, "\nrelease %s (to %s)\n", info.Ticket, orUnknown(o.Host))
	} else {
		fmt.Fprintf(&b, "\nhandoff %s → %s\n", info.Ticket, o.Host)
	}
	fmt.Fprintf(&b, "  branch    %s (origin == local @ %s)\n", info.Branch, short(v.LocalHead))
	fmt.Fprintf(&b, "  worktree  %s (clean)\n", info.Worktree)
	fmt.Fprintf(&b, "  PR        #%d %s (handoff body: %d chars)\n", v.PRNumber, v.PRURL, len(strings.TrimSpace(v.PRBody)))
	if o.Rm {
		fmt.Fprintf(&b, "  then      gv untrack %s --rm (window, worktree, local branch removed)\n", info.Ticket)
	} else {
		fmt.Fprintf(&b, "  then      gv untrack %s + close its window (worktree kept as your hand-edit checkout)\n", info.Ticket)
	}
	if o.Release {
		fmt.Fprintf(&b, "  then      the caller runs the cold adopt locally\n")
	} else {
		fmt.Fprintf(&b, "  then      ssh %s -- gv adopt %s --repo %s --branch %s\n", o.Host, info.Ticket, info.Repo, info.Branch)
	}
	fmt.Fprintf(&b, "  note      the session transcript stays here; the PR body is the carrier\n")
	return b.String()
}

// FollowLine is the closing line of a successful handoff.
func FollowLine(ticket string, o Options) string {
	where := o.Host
	if o.Window != "" {
		where += fmt.Sprintf(" (window %s)", o.Window)
	}
	session := "grove"
	if o.Label != "" {
		session = "grove-" + o.Label
	}
	return fmt.Sprintf("✓ %s → %s. Follow: ssh %s -t tmux attach -t =%s\n", ticket, where, o.Host, session)
}

func orUnknown(s string) string {
	if s == "" {
		return "the calling host"
	}
	return s
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
