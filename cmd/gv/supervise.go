// gv supervise — the headless supervisor loop (grove-253): every pass reads
// state.Peek + one tmux.SnapshotSession per session + github.FetchAll, feeds
// internal/supervise.Transitions, appends whatever fired, and prints it
// exactly like `gv watch` would (plus an ntfy/desktop push per
// docs/plugins.md's table). Pure read + append — no auto-actions, no
// --host (an ambient, local-only verb; #208's territory). A workspace
// with no desk cockpit open (the VPS case) gets a trustworthy stream for
// the whole task lifecycle without another hand-rolled monitor script.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/JollyGrin/grove/internal/detect"
	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/notify"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/supervise"
	"github.com/JollyGrin/grove/internal/tmux"
	"github.com/JollyGrin/grove/internal/watch"
)

// superviseRecommendedFloor is the cost-discipline floor (docs, not
// enforced): at 30s this is <=2 tmux execs per session + 1 capture per task
// + N gh calls per pass — the cockpit's own budget, once every 30 passes of
// its 1s beat. Not a hard minimum — e2e/supervise.sh and impatient manual
// testing both run well under it.
const superviseRecommendedFloor = 5 * time.Second

func cmdSupervise(args []string) error {
	fs := flag.NewFlagSet("supervise", flag.ExitOnError)
	interval := fs.Duration("interval", 30*time.Second,
		fmt.Sprintf("poll interval (recommended floor %s — cost discipline, not enforced)", superviseRecommendedFloor))
	once := fs.Bool("once", false, "one pass then exit 0 — hysteresis is in-process, so a single pass "+
		"can emit delivery and worker_errored events but never worker_waiting/worker_vanished (those "+
		"need a continuous debounce window that only a running loop accumulates)")
	asJSON := fs.Bool("json", false, "emit the raw event record, one per line")
	positionals := parseAnywhere(fs, args)
	if len(positionals) > 0 {
		return fmt.Errorf("usage: gv supervise [--interval 30s] [--once] [--json]")
	}

	cfg, err := loadCfg()
	if err != nil {
		return err
	}

	unlock, err := supervise.Lock(stateDir())
	if err != nil {
		return err
	}
	defer unlock()

	deps := supervise.PollDeps{
		StateDir: stateDir(),
		RepoLookup: func(repo string) (string, bool) {
			r, ok := cfg.Repos[repo]
			if !ok {
				return "", false
			}
			return r.Path, true
		},
		SnapFor:    tmux.SnapshotSession,
		DetectFrom: detect.DetectLiveFrom,
		FetchAll:   github.FetchAll,
	}
	mem := supervise.NewMemory()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	for {
		evs, err := supervise.Poll(deps, mem, time.Now())
		if err != nil {
			return err
		}
		for _, ev := range evs {
			emitSupervise(ev, *asJSON)
			pushSupervise(ev)
		}
		if *once {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(*interval):
		}
	}
}

// emitSupervise prints one event exactly like `gv watch` would — the human
// row by default, or the raw record with --json — unbuffered and
// line-flushed (os.Stdout is unbuffered; see cmd/gv/watch.go's comment on
// why this must never be wrapped in a bufio.Writer).
func emitSupervise(ev state.Event, asJSON bool) {
	line := watch.Row(ev)
	if asJSON {
		if b, err := json.Marshal(ev); err == nil {
			line = string(b)
		}
	}
	fmt.Fprintln(os.Stdout, line)
}

// pushSupervise fans one emitted event out to ntfy/desktop per
// docs/plugins.md's table. Body is the same trailing detail `gv watch`
// prints, capped the same way (watch.Detail) — the two surfaces read the
// same tail, never two independently-truncated strings.
func pushSupervise(ev state.Event) {
	priority, tags, desktop := pushClassFor(ev.Type)
	if priority == "" && !desktop {
		return
	}
	title := ev.Ticket
	if label := watch.Label(ev); label != "" {
		title += " " + label
	}
	body := watch.Detail(ev)
	if desktop {
		notify.Desktop(title, body)
	}
	if priority != "" {
		notify.Push(title, body, priority, tags)
	}
}

// pushClassFor is the grove-253 notification table: every one of the
// eleven delivery/liveness types maps to exactly one row here — the
// default case covers pr_opened/pr_updated/pr_closed/worker_recovered,
// which push nothing.
func pushClassFor(evType string) (priority, tags string, desktop bool) {
	switch evType {
	case state.EvWorkerWaiting, state.EvWorkerVanished, state.EvWorkerErrored:
		return "high", "warning", true
	case state.EvPRCIFailed, state.EvPRConflicting:
		return "high", "x", true
	case state.EvPRReady:
		return "default", "white_check_mark", true
	case state.EvPRMerged:
		return "default", "tada", true
	default: // pr_opened, pr_updated, pr_closed, worker_recovered
		return "", "", false
	}
}
