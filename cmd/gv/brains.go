// `gv brains` — the fleet-wide orchestrator brain sweep (grove-236).
//
// `gv update` swaps in a binary carrying a new orchestrator seed, and
// every workspace on the box is then running an older brain without ever
// being told. This is the read verb that says so: walk the registry (plus
// the global brain used outside any workspace), plan each against the
// seed THIS binary embeds, print the brains that need attention and
// collapse the rest into a count.
//
// Plain `gv brains [--json]` is a pure read, always. `gv brains --refresh`
// (grove-511) is the writing flag: a stale brain that is PRISTINE — its
// stamp is the hash of its own body, so grove wrote it and nobody edited
// it — is grove's own file and is replaced in place. An edited brain still
// gets `CLAUDE.md.new` beside it for the human to diff (grove-190's
// invariant); an unstamped one is still only reported. Leftover `.new` /
// `.bak-*` files are listed per brain and never deleted.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/JollyGrin/grove/internal/bootstrap"
	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/update"
	"github.com/JollyGrin/grove/internal/workspace"
	"github.com/JollyGrin/grove/orchestrator"
)

// updateRefreshesBrains decides what `gv update` does after a real binary
// swap: run `gv brains --refresh` itself (true) or print the plain sweep,
// which names the command (false). Brain-overlay design §Open questions 1,
// decided 2026-10-24: automatic — a pristine brain is grove's own
// unchanged file, and the refresh report says what happened.
const updateRefreshesBrains = true

func cmdBrains(args []string) error {
	fs := flag.NewFlagSet("brains", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable rows — one per registered workspace, plus the global brain")
	refresh := fs.Bool("refresh", false, "replace every PRISTINE stale brain in place; an edited one gets CLAUDE.md.new, an unstamped one is only reported")
	parseAnywhere(fs, args)

	probes, err := brainProbes()
	if err != nil {
		return err
	}
	var rows []bootstrap.BrainRow
	var refreshErr error
	if *refresh {
		rows, refreshErr = bootstrap.RefreshSweep(probes, orchestrator.ClaudeMd)
	} else {
		rows = bootstrap.PlanSweep(probes, orchestrator.ClaudeMd)
	}
	if *asJSON {
		if err := emitJSON("brains", rows); err != nil {
			return err
		}
		return refreshErr
	}
	printBrainSweep(os.Stdout, rows, *refresh)
	return refreshErr
}

// brainProbes is the I/O half: registry → probes, plus the global brain
// (`cfg.Orchestrator.Dir`) as the last row. A registry that will not
// parse is the only hard error; a workspace whose root has vanished is a
// ROW, not a failure — one stale entry must never cost the other ten
// workspaces their sweep. A box with no global config yet still gets
// the global row, at the default dir — that is where its first cockpit
// outside a workspace would look.
func brainProbes() ([]bootstrap.BrainProbe, error) {
	list, err := workspace.LoadRegistry()
	if err != nil {
		return nil, err
	}
	probes := make([]bootstrap.BrainProbe, 0, len(list)+1)
	for _, ws := range list {
		probes = append(probes, bootstrap.ProbeBrain(ws.Label, ws.Root))
	}
	probes = append(probes, bootstrap.ProbeGlobalBrain(globalOrchestratorDir()))
	return probes, nil
}

// globalOrchestratorDir is `cfg.Orchestrator.Dir` from the GLOBAL config
// (never a workspace's merged view — the global brain is the one used
// outside any workspace), falling back to the default when there is no
// config to read.
func globalOrchestratorDir() string {
	if cfg, err := config.Load(); err == nil && cfg.Orchestrator.Dir != "" {
		return cfg.Orchestrator.Dir
	}
	return filepath.Join(config.Dir(), "orchestrator")
}

// printBrainSweep renders the sweep. Up-to-date brains collapse into the
// trailing count — the report has to stay readable at 11+ workspaces or
// it will be scrolled past, which is the whole failure this fixes. Under
// --refresh the rows just acted on are printed with a ✓ and counted as
// done, not as attention.
func printBrainSweep(w io.Writer, rows []bootstrap.BrainRow, refreshed bool) {
	if len(rows) == 0 || (len(rows) == 1 && rows[0].Label == bootstrap.GlobalBrainLabel) {
		fmt.Fprintln(w, "no workspaces registered — `gv workspaces add <path>`")
		if len(rows) == 0 {
			return
		}
	}
	label, state, current, acted, pristineStale := 0, 0, 0, 0, 0
	for _, r := range rows {
		if r.Acted() {
			acted++
		} else if !r.NeedsAttention() {
			current++
			continue
		}
		if r.State == string(bootstrap.BrainStale) && r.Pristine && !r.Acted() {
			pristineStale++
		}
		if len(r.Label) > label {
			label = len(r.Label)
		}
		if len(r.State) > state {
			state = len(r.State)
		}
	}
	for _, r := range rows {
		if !r.NeedsAttention() && !r.Acted() {
			continue
		}
		mark := " "
		if r.Acted() {
			mark = "✓"
		}
		fmt.Fprintf(w, "%s %-*s  %-*s  %s\n", mark, label, r.Label, state, r.State, r.Note)
		switch {
		case r.Command == "":
		case strings.HasPrefix(r.Command, bootstrap.RefreshSweepCmd):
			// Fleet-wide: runs from anywhere.
			fmt.Fprintf(w, "  %-*s  %-*s  → %s\n", label, "", state, "", r.Command)
		default:
			fmt.Fprintf(w, "  %-*s  %-*s  → cd %s && %s\n", label, "", state, "", r.Root, r.Command)
		}
		if r.Action == string(bootstrap.ActionNew) {
			dir := brainDirOf(r)
			fmt.Fprintf(w, "  %-*s  %-*s  → diff %s/%s %s/%s.new\n", label, "", state, "", dir, bootstrap.BrainFile, dir, bootstrap.BrainFile)
		}
	}
	if behind := len(rows) - current - acted; behind > 0 {
		verb := "need"
		if behind == 1 {
			verb = "needs"
		}
		fmt.Fprintf(w, "%d of %d %s %s attention\n", behind, len(rows), plural(len(rows), "brain"), verb)
		// The invariant belongs in the report, not only in grove-190's
		// source: the operator has to know the refresh is safe to run.
		for _, r := range rows {
			edited := r.State == string(bootstrap.BrainStale) && !r.Pristine
			if r.Command != "" && (edited || r.State == string(bootstrap.BrainUnstamped)) {
				fmt.Fprintf(w, "grove never overwrites an edited brain — the refresh writes %s.new beside yours to diff.\n", bootstrap.BrainFile)
				break
			}
		}
		if pristineStale > 0 && !refreshed {
			fmt.Fprintf(w, "%d pristine %s (grove's own, unedited) — `%s` replaces them in place, no merge.\n",
				pristineStale, plural(pristineStale, "brain"), bootstrap.RefreshSweepCmd)
		}
	}
	if acted > 0 {
		fmt.Fprintf(w, "✓ %d %s refreshed\n", acted, plural(acted, "brain"))
	}
	if current > 0 {
		fmt.Fprintf(w, "✓ %d %s current\n", current, plural(current, "brain"))
	}
	printBrainLeftovers(w, rows)
}

// printBrainLeftovers names every CLAUDE.md.new / .bak-* beside a brain.
// Grove never deletes them — the operator does, once happy.
func printBrainLeftovers(w io.Writer, rows []bootstrap.BrainRow) {
	first := true
	for _, r := range rows {
		if len(r.Leftovers) == 0 {
			continue
		}
		if first {
			fmt.Fprintln(w, "leftovers — safe to delete once you're happy (grove never removes them):")
			first = false
		}
		for _, l := range r.Leftovers {
			fmt.Fprintf(w, "  %s/%s\n", brainDirOf(r), l)
		}
	}
}

// brainDirOf is the orchestrator dir a row's leftovers live in: the
// global row's Root IS its dir; a workspace row's is under its root.
func brainDirOf(r bootstrap.BrainRow) string {
	if r.Label == bootstrap.GlobalBrainLabel {
		return r.Root
	}
	return bootstrap.OrchestratorDirAt(r.Root)
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// reportBrainSweepAfterUpdate is (2) in grove-236: after `gv update`
// actually replaces the binary, say which brains are now behind — and,
// when updateRefreshesBrains says so, bring the pristine ones current.
//
// It shells out to the REPLACED binary rather than sweeping in-process —
// this process still holds the OLD embedded seed, so an in-process sweep
// would cheerfully report every workspace current against the seed we
// just superseded. Never fatal: any failure here is a warning, and `gv
// update` still exits 0.
func reportBrainSweepAfterUpdate(target string) {
	if target == "" {
		var err error
		if target, err = update.Target(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: brain sweep skipped (%v) — run `gv brains` yourself\n", err)
			return
		}
	}
	args := []string{"brains"}
	if updateRefreshesBrains {
		args = append(args, "--refresh")
	}
	out, err := exec.Command(target, args...).Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: brain sweep failed (%v) — run `gv brains` yourself\n", err)
		return
	}
	if updateRefreshesBrains {
		fmt.Print("\norchestrator brains refreshed against the seed the new binary carries (pristine ones replaced in place; edited ones get CLAUDE.md.new):\n")
	} else {
		fmt.Print("\norchestrator brains, against the seed the new binary carries:\n")
	}
	os.Stdout.Write(out)
}
