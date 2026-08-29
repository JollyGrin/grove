// gv watch — the transition stream (grove-205). A monitor asks grove
// "tell me when this task changes state" instead of scraping a pane that
// contains the kickoff prompt's own STATUS sentinels. Pure read: it never
// appends to events.jsonl and never rewrites tasks.json.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/JollyGrin/grove/internal/watch"
	"github.com/JollyGrin/grove/internal/workspace"
)

// repeatable collects a flag given more than once (`--ticket a --ticket b`)
// and also splits a comma-separated value, so both spellings work.
type repeatable []string

func (r *repeatable) String() string { return strings.Join(*r, ",") }

func (r *repeatable) Set(v string) error {
	*r = append(*r, watch.Split(v)...)
	return nil
}

func cmdWatch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit the raw event record, one per line")
	replay := fs.Bool("replay", false, "include the log's history, not just new events")
	since := fs.String("since", "", "only events at or after this RFC3339 timestamp")
	until := fs.String("until", "", "exit 0 when this sentinel lands (question|blocked|done|none)")
	var tickets, types, sentinels repeatable
	fs.Var(&tickets, "ticket", "only this ticket (repeatable, or comma-separated)")
	fs.Var(&types, "type", "event types to stream (default: the terminal/actionable set; `all` for every type)")
	fs.Var(&sentinels, "sentinel", "only agent_status events carrying these sentinels")
	positionals := parseAnywhere(fs, args)
	if len(positionals) > 0 {
		return fmt.Errorf("usage: gv watch [--json] [--ticket X]... [--type t,…] [--sentinel s,…] [--since <RFC3339>|--replay] [--until <sentinel>]")
	}

	if err := watch.Validate(types, sentinels, *until); err != nil {
		return err
	}
	opts := watch.Options{
		StateDir:  stateDir(),
		Tickets:   tickets,
		Types:     types,
		Sentinels: sentinels,
		Replay:    *replay,
		Until:     *until,
		JSON:      *asJSON,
	}
	if *since != "" {
		if *replay {
			return fmt.Errorf("--since and --replay are alternatives: --replay is the whole log, --since a cutoff into it")
		}
		ts, err := time.Parse(time.RFC3339, *since)
		if err != nil {
			return fmt.Errorf("--since: %v (want RFC3339, e.g. %s)", err, time.Now().Format(time.RFC3339))
		}
		opts.Since = ts
	}
	warnGlobalLayerWatch()

	// signal.NotifyContext, not a bare read loop: Ctrl-C on a follower must
	// end the stream, and it must NOT be mistaken for the `--until` sentinel
	// landing (see the exit-code contract below).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// os.Stdout is unbuffered — every Write is a syscall — so an event is on
	// the pipe the instant it is folded. The Claude Code Monitor tool turns
	// one stdout LINE into one notification; a buffered writer here would
	// mean the event is never seen. Do not wrap this in a bufio.Writer.
	fired, err := watch.Run(ctx, opts, os.Stdout)
	if err != nil {
		return err
	}
	if *until != "" && !fired {
		// Exit non-zero: `gv watch --until done` exiting 0 must mean "the
		// transition happened", never "something ended the wait". A monitor
		// that cannot tell those apart is the bug this command replaces.
		return fmt.Errorf("interrupted before %s", *until)
	}
	return nil
}

// warnGlobalLayerWatch keeps silence from reading as success at the global
// layer. `gv watch` follows ONE log — the ambient workspace's, exactly as
// `gv ls` resolves it — so run from outside any workspace it tails the
// global state dir, which on a workspace-shaped machine never gets an
// event. Naming the workspaces on stderr costs the stream nothing.
func warnGlobalLayerWatch() {
	if ambient.ws != nil {
		return
	}
	list, err := workspace.LoadRegistry()
	if err != nil {
		return
	}
	var labels []string
	for _, ws := range list {
		if workspace.Alive(ws) {
			labels = append(labels, ws.Label)
		}
	}
	if len(labels) == 0 {
		return
	}
	sort.Strings(labels)
	fmt.Fprintf(os.Stderr,
		"gv watch: no ambient workspace — following %s; workspaces have their own logs (cd into one: %s)\n",
		filepath.Join(stateDir(), "events.jsonl"), strings.Join(labels, ", "))
}
