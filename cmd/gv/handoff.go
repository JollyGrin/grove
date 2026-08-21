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
	"github.com/JollyGrin/grove/internal/remote"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/tmux"
)

// cmdHandoff (grove-177) moves a running task between grove hosts.
//
//	gv handoff <ticket> --to <host>    checkpoint → verify → untrack → ssh adopt
//	gv handoff <ticket> --from <host>  the mirror: remote release, local adopt
//
// --release is the remote half of --from (steps 1–5 only, run over ssh by
// the pulling host); --release-to names the puller for the tombstone.
func cmdHandoff(args []string) error {
	echoWorkspace()
	fs := flag.NewFlagSet("handoff", flag.ExitOnError)
	to := fs.String("to", "", "hand the task to this configured host")
	from := fs.String("from", "", "pull the task from this configured host")
	rm := fs.Bool("rm", false, "also remove the local window, worktree, and branch (default: keep the worktree as your hand-edit checkout)")
	yes := fs.Bool("yes", false, "skip the confirm prompt")
	noCheckpoint := fs.Bool("no-checkpoint", false, "skip the checkpoint nudge (the worker already wrote its handoff)")
	timeout := fs.Duration("timeout", 10*time.Minute, "how long to wait for the worker to go idle after the checkpoint nudge")
	release := fs.Bool("release", false, "remote half of --from: checkpoint, verify, untrack — no adopt")
	releaseTo := fs.String("release-to", "", "with --release: the pulling host's name (written as the tombstone)")
	positionals := parseAnywhere(fs, args)
	if len(positionals) != 1 || (!*release && (*to == "") == (*from == "")) {
		return fmt.Errorf("usage: gv handoff <ticket> --to <host> | --from <host>  [--rm] [--yes] [--no-checkpoint] [--timeout 10m]")
	}
	cfg, err := loadCfg()
	if err != nil {
		return err
	}
	switch {
	case *release:
		r := &localHandoff{cfg: cfg}
		return handoff.Run(r, handoff.Options{
			Task: positionals[0], Host: *releaseTo, Rm: *rm, Yes: *yes,
			NoCheckpoint: *noCheckpoint, Timeout: *timeout, Release: true,
		}, os.Stdout)
	case *from != "":
		return handoffFrom(cfg, positionals[0], *from, *rm, *yes, *noCheckpoint, *timeout)
	default:
		if _, err := cfg.Host(*to); err != nil {
			return err
		}
		r := &localHandoff{cfg: cfg}
		o := handoff.Options{
			Task: positionals[0], Host: *to, Rm: *rm, Yes: *yes,
			NoCheckpoint: *noCheckpoint, Timeout: *timeout, Label: wsLabel(),
		}
		if t, err := findTask(positionals[0]); err == nil {
			// Best-effort window name for the follow line: the remote
			// builds it from the same repo label + branch.
			o.Window = tmux.WorkerWindowProfile(repoShort(t.Repo, ambient.ws), t.Branch, t.ModelProfile)
		}
		return handoff.Run(r, o, os.Stdout)
	}
}

// handoffFrom pulls a task from host: `ls --json` there to learn repo +
// branch, `handoff --release` there (streamed; its confirm prompt reads
// our stdin), then a local cold adopt.
func handoffFrom(cfg *config.Config, task, host string, rm, yes, noCheckpoint bool, timeout time.Duration) error {
	if _, err := cfg.Host(host); err != nil {
		return err
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
	self, _ := os.Hostname()
	relArgs := []string{task, "--release", "--release-to", self, "--timeout", timeout.String()}
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
	if err := cmdAdopt([]string{task, "--repo", repo, "--branch", branch}); err != nil {
		return fmt.Errorf("%w\n%s is released on %s but not running here — retry: gv adopt %s --repo %s --branch %s", err, task, host, task, repo, branch)
	}
	return nil
}

// localHandoff is the real handoff.Runner: each method is the existing
// verb's code path (findTask, relayText, untrack event, remote adopt).
type localHandoff struct {
	cfg *config.Config
}

func (l *localHandoff) Lookup(task string) (*handoff.Info, error) {
	t, err := findTask(task)
	if err != nil {
		return nil, err
	}
	return &handoff.Info{
		Ticket: t.Ticket, Repo: t.Repo, Branch: t.Branch, Worktree: t.Worktree,
		Agent: t.Agent, Paused: t.Paused,
		WindowLive: tmux.WindowLive(t.TmuxSession, t.TmuxWindow),
	}, nil
}

func (l *localHandoff) Agent(ticket string) (string, bool, error) {
	tasks, err := state.Load(stateDir())
	if err != nil {
		return "", false, err
	}
	t, ok := tasks[ticket]
	if !ok {
		return "", false, fmt.Errorf("%s vanished from state", ticket)
	}
	return t.Agent, tmux.WindowLive(t.TmuxSession, t.TmuxWindow), nil
}

func (l *localHandoff) Nudge(ticket, text string) error {
	t, err := findTask(ticket)
	if err != nil {
		return err
	}
	return relayText(t, text)
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
	t, err := findTask(ticket)
	if err != nil {
		return err
	}
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
	code, err := remote.Run(l.cfg, host, "adopt", []string{ticket, "--repo", repo, "--branch", branch}, os.Stdout, os.Stderr)
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
