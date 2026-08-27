package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/git"
	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/handoff"
	"github.com/JollyGrin/grove/internal/provider"
	"github.com/JollyGrin/grove/internal/remote"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/tmux"
)

// cmdHandoff (grove-177) moves a running task between grove hosts.
//
//	gv handoff <ticket> --to <host>    checkpoint → verify → untrack → ssh adopt
//	gv handoff <ticket> --from <host>  the mirror: remote release, local adopt
//
// --release is the remote half of --from (guard → checkpoint → verify →
// untrack, NO tombstone), run over ssh by the pulling host;
// --tombstone-to is the puller's call-back that writes the tombstone only
// AFTER its local adopt succeeded — the last write on either side.
func cmdHandoff(args []string) error {
	echoWorkspace()
	fs := flag.NewFlagSet("handoff", flag.ExitOnError)
	to := fs.String("to", "", "hand the task to this configured host")
	from := fs.String("from", "", "pull the task from this configured host")
	as := fs.String("as", "", "with --from: the name THIS host goes by in the remote's hosts: config (tombstone pointer; default: os.Hostname())")
	rm := fs.Bool("rm", false, "also remove the releasing side's window, worktree, and branch — local with --to, REMOTE with --from (default: keep that worktree as a hand-edit checkout)")
	yes := fs.Bool("yes", false, "skip the confirm prompt")
	noCheckpoint := fs.Bool("no-checkpoint", false, "skip the checkpoint nudge (the worker already wrote its handoff)")
	timeout := fs.Duration("timeout", 10*time.Minute, "how long to wait for the worker to go idle after the checkpoint nudge")
	release := fs.Bool("release", false, "remote half of --from: checkpoint, verify, untrack — no adopt, no tombstone")
	tombstoneTo := fs.String("tombstone-to", "", "record that this (already released) task now runs on the named host — the puller's post-adopt call-back")
	positionals := parseAnywhere(fs, args)
	oneMode := 0
	for _, set := range []bool{*release, *tombstoneTo != "", *to != "", *from != ""} {
		if set {
			oneMode++
		}
	}
	if len(positionals) != 1 || oneMode != 1 {
		return fmt.Errorf("usage: gv handoff <ticket> --to <host> | --from <host>  [--as name] [--rm] [--yes] [--no-checkpoint] [--timeout 10m]")
	}
	cfg, err := loadCfg()
	if err != nil {
		return err
	}
	switch {
	case *tombstoneTo != "":
		return handoffTombstone(positionals[0], *tombstoneTo)
	case *release:
		r := &localHandoff{cfg: cfg}
		return handoff.Run(r, handoff.Options{
			Task: positionals[0], Rm: *rm, Yes: *yes,
			NoCheckpoint: *noCheckpoint, Timeout: *timeout, Release: true,
		}, os.Stdout)
	case *from != "":
		return handoffFrom(cfg, positionals[0], *from, *as, *rm, *yes, *noCheckpoint, *timeout)
	default:
		h, err := cfg.Host(*to)
		if err != nil {
			return err
		}
		r := &localHandoff{cfg: cfg}
		o := handoff.Options{
			Task: positionals[0], Host: *to, Rm: *rm, Yes: *yes,
			NoCheckpoint: *noCheckpoint, Timeout: *timeout,
			SSH: h.SSH, GV: h.GV,
		}
		if t, err := findTask(positionals[0]); err == nil {
			// Best-effort window name for the follow line: the remote
			// builds it from the same repo label + branch.
			o.Window = tmux.WorkerWindowProfile(repoShort(t.Repo, ambient.ws), t.Branch, t.ModelProfile)
		}
		return handoff.Run(r, o, os.Stdout)
	}
}

// handoffTombstone appends the forwarding tombstone for a task that was
// already released (untracked) here — the puller calls it over ssh after
// its local adopt succeeds, so a failed pull leaves no pointer. The
// branch comes from folded state: untrack keeps it.
func handoffTombstone(task, host string) error {
	tasks, err := state.Load(stateDir())
	if err != nil {
		return err
	}
	var t *state.Task
	for _, cand := range provider.IDCandidates(task) {
		if hit, ok := tasks[cand]; ok {
			t = hit
			break
		}
	}
	if t == nil {
		return fmt.Errorf("%s has never been tracked here — nothing to tombstone", task)
	}
	if !t.Done {
		return fmt.Errorf("%s is still tracked here — release it first (gv handoff %s --release)", t.Ticket, t.Ticket)
	}
	if err := state.Append(stateDir(), state.Event{
		Type: state.EvTaskHandedOff, Ticket: t.Ticket,
		Data: map[string]string{"host": host, "branch": t.Branch},
	}); err != nil {
		return err
	}
	fmt.Printf("✓ %s → %s recorded (gv ls keeps the pointer)\n", t.Ticket, host)
	return nil
}

// handoffFrom pulls a task from host: `ls --json` there to learn repo +
// branch, `handoff --release` there (streamed; its confirm prompt reads
// our stdin), a local cold adopt, then the tombstone call-back — written
// on the remote only once the adopt here succeeded.
func handoffFrom(cfg *config.Config, task, host, as string, rm, yes, noCheckpoint bool, timeout time.Duration) error {
	if _, err := cfg.Host(host); err != nil {
		return err
	}
	// The tombstone must name THIS host as the remote's config knows it,
	// or every command the remote's `gv ls` row suggests will error. A
	// raw hostname is the fallback; --as overrides it.
	if as == "" {
		as, _ = os.Hostname()
	}
	if as == "" {
		return fmt.Errorf("cannot determine this host's name for the remote tombstone — pass --as <name>")
	}
	var buf bytes.Buffer
	code, err := remote.Run(cfg, host, "ls", []string{"--json", "--no-pr", "--no-cost"}, &buf, os.Stderr)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("ssh %s: gv ls exited %d", host, code)
	}
	var env struct {
		Tasks []struct {
			Ticket, Repo, Branch string
			Done                 bool
		} `json:"tasks"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		return fmt.Errorf("parse remote ls: %w", err)
	}
	repo, branch := "", ""
	for _, t := range env.Tasks {
		if t.Ticket == task && !t.Done {
			repo, branch = t.Repo, t.Branch
			break
		}
	}
	if branch == "" {
		return fmt.Errorf("%s is not tracked on %s (gv ls --host %s)", task, host, host)
	}
	// Preflight the LOCAL half before touching the remote: releasing a
	// task we then can't adopt (repo missing from this host's config)
	// would strand it untracked everywhere.
	if _, ok := cfg.Repos[repo]; !ok {
		return fmt.Errorf("repo %q (which %s runs %s under) is not in this host's config — add it to config.yaml first; nothing was released", repo, host, task)
	}
	relArgs := []string{task, "--release", "--timeout", timeout.String()}
	if rm {
		relArgs = append(relArgs, "--rm")
	}
	if yes {
		relArgs = append(relArgs, "--yes")
	}
	if noCheckpoint {
		relArgs = append(relArgs, "--no-checkpoint")
	}
	code, err = remote.Run(cfg, host, "handoff", relArgs, os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("%s was not released by %s (exit %d) — nothing changed here", task, host, code)
	}
	fmt.Printf("→ adopting %s here (repo %s, branch %s)\n", task, repo, branch)
	if err := cmdAdopt([]string{task, "--repo", repo, "--branch", branch, "--sync"}); err != nil {
		return fmt.Errorf("%w\n%s is released on %s but not running here (no tombstone written there) — retry: gv adopt %s --repo %s --branch %s --sync", err, task, host, task, repo, branch)
	}
	// Tombstone call-back — the remote's LAST write, only now that the
	// task verifiably runs here. Best-effort: the task is safe either
	// way, the remote just loses its forwarding row.
	if code, err = remote.Run(cfg, host, "handoff", []string{task, "--tombstone-to", as}, os.Stdout, os.Stderr); err != nil || code != 0 {
		fmt.Fprintf(os.Stderr, "warning: %s runs here, but recording the pointer on %s failed (%v, exit %d) — retry: gv handoff %s --tombstone-to %s --host %s\n",
			task, host, err, code, task, as, host)
	}
	return nil
}

// localHandoff is the real handoff.Runner: each method is the existing
// verb's code path (findTask, relayText, untrack event, remote adopt).
// Lookup caches the task row — tmux/window identity is stable across the
// sequence, so later steps skip re-folding events.jsonl.
type localHandoff struct {
	cfg *config.Config
	t   *state.Task
}

func (l *localHandoff) Lookup(task string) (*handoff.Info, error) {
	t, err := findTask(task)
	if err != nil {
		return nil, err
	}
	l.t = t
	return &handoff.Info{
		Ticket: t.Ticket, Repo: t.Repo, Branch: t.Branch, Worktree: t.Worktree,
		Agent: t.Agent, Paused: t.Paused,
		WindowLive: tmux.WindowLive(t.TmuxSession, t.TmuxWindow),
	}, nil
}

func (l *localHandoff) Agent(ticket string) (string, bool, error) {
	// Peek, not Load: this runs every poll tick and only the agent field
	// moves — no point rewriting the derived tasks.json each time.
	tasks, err := state.Peek(stateDir())
	if err != nil {
		return "", false, err
	}
	t, ok := tasks[ticket]
	if !ok {
		return "", false, fmt.Errorf("%s vanished from state", ticket)
	}
	return t.Agent, tmux.WindowLive(t.TmuxSession, t.TmuxWindow), nil
}

func (l *localHandoff) Nudge(_ string, text string) error {
	// A local checkpoint nudge carries no op id (grove-186): it is not a
	// relayed hop, and the handoff flow owns its own retry semantics.
	return relayText(l.t, text, "")
}

func (l *localHandoff) Verify(info *handoff.Info) (*handoff.Verified, error) {
	repo, ok := l.cfg.Repos[info.Repo]
	if !ok {
		return nil, fmt.Errorf("repo %q no longer in config", info.Repo)
	}
	if _, err := os.Stat(info.Worktree); err != nil {
		return nil, fmt.Errorf("worktree %s is gone — `gv adopt %s` to re-create it first", info.Worktree, info.Ticket)
	}
	v := &handoff.Verified{}
	remoteHead, err := git.RemoteHead(repo.Path, info.Branch)
	if err != nil {
		return nil, err
	}
	v.OnOrigin, v.RemoteHead = remoteHead != "", remoteHead
	if v.LocalHead, err = git.Head(info.Worktree); err != nil {
		return nil, err
	}
	if v.Dirty, err = git.IsDirty(info.Worktree); err != nil {
		return nil, err
	}
	if v.PRNumber, v.PRURL, v.PRBody, err = github.OpenPRBody(repo.Path, info.Branch); err != nil {
		return nil, err
	}
	return v, nil
}

func (l *localHandoff) Confirm(string) (bool, error) {
	fmt.Print("proceed? [y/N] ")
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		return false, nil
	}
	a := strings.ToLower(strings.TrimSpace(sc.Text()))
	return a == "y" || a == "yes", nil
}

// Untrack is `gv untrack` (+ --rm when asked). Without --rm the worktree
// stays as the operator's hand-edit checkout, but the worker window is
// still closed: two agents on one branch (the idle local one and the
// remote's fresh pickup) is exactly the split-brain a handoff exists to
// avoid. Same kill as gv pause — worktree, branch, transcript survive.
func (l *localHandoff) Untrack(ticket string, rm bool) error {
	t := l.t
	args := []string{ticket}
	if rm {
		args = append(args, "--rm")
	}
	if err := cmdUntrack(args); err != nil {
		return err
	}
	if !rm && tmux.WindowLive(t.TmuxSession, t.TmuxWindow) {
		if err := tmux.KillWindow(t.TmuxSession, t.TmuxWindow); err != nil {
			return err
		}
		fmt.Printf("→ closed the local worker window (worktree %s kept)\n", t.Worktree)
	}
	return nil
}

func (l *localHandoff) RemoteAdopt(host, ticket, repo, branch string) error {
	// --sync: the remote may hold a stale worktree/branch from an earlier
	// stint on this task — fetch + hard-reset to origin before the pickup.
	code, err := remote.Run(l.cfg, host, "adopt", []string{ticket, "--repo", repo, "--branch", branch, "--sync"}, os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("ssh %s: gv adopt exited %d", host, code)
	}
	return nil
}

func (l *localHandoff) Tombstone(ticket, host, branch string) error {
	return state.Append(stateDir(), state.Event{
		Type: state.EvTaskHandedOff, Ticket: ticket,
		Data: map[string]string{"host": host, "branch": branch},
	})
}

func (l *localHandoff) Sleep(d time.Duration) { time.Sleep(d) }
