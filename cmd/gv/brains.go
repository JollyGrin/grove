// `gv brains` — the fleet-wide orchestrator brain sweep (grove-236).
//
// `gv update` swaps in a binary carrying a new orchestrator seed, and
// every workspace on the box is then running an older brain without ever
// being told. This is the read verb that says so: walk the registry, plan
// each root against the seed THIS binary embeds, print the workspaces
// that need attention and collapse the rest into a count.
//
// Pure read, always. grove-190's invariant is untouched: grove never
// overwrites a brain — the sweep hands over the `gv init --only
// orchestrator-md` line and the human diffs the CLAUDE.md.new it writes.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/JollyGrin/grove/internal/bootstrap"
	"github.com/JollyGrin/grove/internal/update"
	"github.com/JollyGrin/grove/internal/workspace"
	"github.com/JollyGrin/grove/orchestrator"
)

func cmdBrains(args []string) error {
	fs := flag.NewFlagSet("brains", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable rows — one per registered workspace")
	parseAnywhere(fs, args)

	rows, err := brainSweep()
	if err != nil {
		return err
	}
	if *asJSON {
		return emitJSON("brains", rows)
	}
	printBrainSweep(os.Stdout, rows)
	return nil
}

// brainSweep is the I/O half: registry → probes → rows. A registry that
// will not parse is the only hard error; a workspace whose root has
// vanished is a ROW, not a failure — one stale entry must never cost the
// other ten workspaces their sweep.
func brainSweep() ([]bootstrap.BrainRow, error) {
	list, err := workspace.LoadRegistry()
	if err != nil {
		return nil, err
	}
	probes := make([]bootstrap.BrainProbe, 0, len(list))
	for _, ws := range list {
		probes = append(probes, bootstrap.ProbeBrain(ws.Label, ws.Root))
	}
	return bootstrap.PlanSweep(probes, orchestrator.ClaudeMd), nil
}

// printBrainSweep renders the sweep. Up-to-date workspaces collapse into
// the trailing count — the report has to stay readable at 11+ workspaces
// or it will be scrolled past, which is the whole failure this fixes.
func printBrainSweep(w io.Writer, rows []bootstrap.BrainRow) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "no workspaces registered — `gv workspaces add <path>`")
		return
	}
	label, state, current := 0, 0, 0
	for _, r := range rows {
		if !r.NeedsAttention() {
			current++
			continue
		}
		if len(r.Label) > label {
			label = len(r.Label)
		}
		if len(r.State) > state {
			state = len(r.State)
		}
	}
	for _, r := range rows {
		if !r.NeedsAttention() {
			continue
		}
		fmt.Fprintf(w, "%-*s  %-*s  %s\n", label, r.Label, state, r.State, r.Note)
		if r.Command != "" {
			fmt.Fprintf(w, "%-*s  %-*s  → cd %s && %s\n", label, "", state, "", r.Root, r.Command)
		}
	}
	if behind := len(rows) - current; behind > 0 {
		fmt.Fprintf(w, "%d of %d %s need attention\n", behind, len(rows), plural(len(rows), "workspace"))
		// The invariant belongs in the report, not only in grove-190's
		// source: the operator has to know the refresh is safe to run.
		for _, r := range rows {
			if r.Command != "" {
				fmt.Fprintf(w, "grove never overwrites a brain — the refresh writes %s.new beside yours to diff.\n", bootstrap.BrainFile)
				break
			}
		}
	}
	if current > 0 {
		fmt.Fprintf(w, "✓ %d %s current\n", current, plural(current, "workspace"))
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// reportBrainSweepAfterUpdate is (2) in grove-236: after `gv update`
// actually replaces the binary, say which workspaces are now behind.
//
// It shells out to the REPLACED binary rather than sweeping in-process —
// this process still holds the OLD embedded seed, so an in-process sweep
// would cheerfully report every workspace current against the seed we
// just superseded. Report-only and never fatal: any failure here is a
// warning, and `gv update` still exits 0.
func reportBrainSweepAfterUpdate(target string) {
	if target == "" {
		var err error
		if target, err = update.Target(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: brain sweep skipped (%v) — run `gv brains` yourself\n", err)
			return
		}
	}
	out, err := exec.Command(target, "brains").Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: brain sweep failed (%v) — run `gv brains` yourself\n", err)
		return
	}
	fmt.Print("\norchestrator brains, against the seed the new binary carries:\n")
	os.Stdout.Write(out)
}
