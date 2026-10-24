// `gv brains` — the fleet-wide orchestrator brain sweep (grove-236).
//
// `gv update` swaps in a binary carrying a new orchestrator seed, and
// every workspace on the box is then running an older brain without ever
// being told. This is the read verb that says so: walk the registry (plus
// the global brain used outside any workspace), plan each against the
// seed THIS binary embeds, print the brains that need attention and
// collapse the rest into a count.
//
// Each row also says whether an operator-owned `CLAUDE.local.md` overlay
// sits beside the brain and how many real lines it holds (grove-512) —
// a stub holding only HTML comments counts as none.
//
// Plain `gv brains [--json]` is a pure read, always. `gv brains --refresh`
// (grove-511) is the writing flag: a stale brain that is PRISTINE — its
// stamp is the hash of its own body, so grove wrote it and nobody edited
// it — is grove's own file and is replaced in place. An edited brain still
// gets `CLAUDE.md.new` beside it for the human to diff (grove-190's
// invariant); an unstamped one is still only reported. Leftover `.new` /
// `.bak-*` files are listed per brain and never deleted.
//
// `gv brains --migrate [--label L]` (grove-513) is the one-time move of an
// edited brain's edits into the overlay, dry run: it diffs each edited
// brain against the archived seed its stamp names, classifies the hunks
// and writes CLAUDE.local.md.draft beside the brain — the only file it
// touches. `--apply` is car 04.
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
	"github.com/JollyGrin/grove/orchestrator/seeds"
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
	migrate := fs.Bool("migrate", false, "dry run: draft a CLAUDE.local.md.draft from each edited brain's edits (writes nothing else)")
	label := fs.String("label", "", "with --migrate: only this workspace (or \"(global)\")")
	parseAnywhere(fs, args)

	if *migrate {
		if *refresh {
			return fmt.Errorf("--migrate and --refresh are separate runs — refresh first, then migrate")
		}
		if *asJSON {
			return fmt.Errorf("--migrate has no --json form yet")
		}
		return cmdBrainsMigrate(*label)
	}
	if *label != "" {
		return fmt.Errorf("--label only applies to --migrate")
	}

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

// cmdBrainsMigrate is `gv brains --migrate [--label L]`: the dry run over
// the registry plus the global brain, or one labelled row. The base for
// each edited brain is the seed this binary carries when the stamps match,
// else the archived seed under that stamp (orchestrator/seeds).
func cmdBrainsMigrate(label string) error {
	probes, err := brainProbes()
	if err != nil {
		return err
	}
	mprobes := make([]bootstrap.MigrateProbe, 0, len(probes))
	for _, p := range probes {
		if label != "" && p.Label != label {
			continue
		}
		mp := bootstrap.MigrateProbe{BrainProbe: p}
		if p.Label != bootstrap.GlobalBrainLabel && !p.RootGone && p.Exists {
			mp.Tracked = brainTracked(p.Root)
		}
		mprobes = append(mprobes, mp)
	}
	if label != "" && len(mprobes) == 0 {
		return fmt.Errorf("no workspace labelled %q (see `gv workspaces`; the global brain is %q)", label, bootstrap.GlobalBrainLabel)
	}
	rows, sweepErr := bootstrap.MigrateSweep(mprobes, orchestrator.ClaudeMd, seeds.Lookup)
	printBrainMigrate(os.Stdout, rows)
	return sweepErr
}

// brainTracked asks git whether the workspace repo versions its brain
// (thegrid and waterhouse force-added theirs past `.grove/.gitignore`). No
// git, no repo, or untracked all read false.
func brainTracked(root string) bool {
	rel := filepath.Join(".grove", "orchestrator", bootstrap.BrainFile)
	cmd := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", "--", rel)
	return cmd.Run() == nil
}

// printBrainMigrate renders the dry run: one line per brain with a draft
// or a problem, the nothing-to-migrate rows collapsed into a count. For a
// tracked brain the two git commands --apply will leave pending are
// printed, never run; leftovers are listed, never deleted.
func printBrainMigrate(w io.Writer, rows []bootstrap.MigrateRow) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "no workspaces registered — `gv workspaces add <path>`")
		return
	}
	label, nothing, drafts, review := 0, 0, 0, 0
	for _, r := range rows {
		if r.State == bootstrap.MigrateNothing {
			nothing++
			continue
		}
		if len(r.Label) > label {
			label = len(r.Label)
		}
	}
	for _, r := range rows {
		if r.State == bootstrap.MigrateNothing {
			continue
		}
		fmt.Fprintf(w, "  %-*s  %-12s  %s\n", label, r.Label, r.State, r.Note)
		if r.State != bootstrap.MigrateDraft {
			continue
		}
		drafts++
		if r.Review > 0 {
			review++
		}
		fmt.Fprintf(w, "  %-*s  %-12s  → %s\n", label, "", "", r.DraftPath)
		if r.Tracked {
			fmt.Fprintf(w, "  %-*s  %-12s  git tracks this brain — after --apply, run these yourself (grove never does):\n", label, "", "")
			for _, c := range r.GitCommands {
				fmt.Fprintf(w, "  %-*s  %-12s    cd %s && %s\n", label, "", "", r.Root, c)
			}
		}
	}
	if drafts > 0 {
		fmt.Fprintf(w, "%d %s drafted — dry run, nothing else written; read each draft, then `%s<label>` (car 04) swaps it in.\n",
			drafts, plural(drafts, "draft"), bootstrap.MigrateApplyCmd)
		if review > 0 {
			fmt.Fprintf(w, "%d %s a `%s` block — resolve it first; --apply refuses while it is there.\n",
				review, map[bool]string{true: "draft carries", false: "drafts carry"}[review == 1], strings.TrimPrefix(bootstrap.ReviewHeading, "## "))
		}
	}
	if nothing > 0 {
		fmt.Fprintf(w, "✓ %d %s nothing to migrate (pristine or absent)\n", nothing, map[bool]string{true: "brain has", false: "brains have"}[nothing == 1])
	}
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
			fmt.Fprintf(w, "  %s/%s\n", r.Dir, l)
		}
	}
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
		note := r.Note
		if r.Overlay {
			note += fmt.Sprintf(" · overlay: %d %s", r.OverlayLines, plural(r.OverlayLines, "line"))
		}
		fmt.Fprintf(w, "%s %-*s  %-*s  %s\n", mark, label, r.Label, state, r.State, note)
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
	printBrainOverlays(w, rows)
	printBrainLeftovers(w, rows)
}

// printBrainOverlays counts the brains that carry a real CLAUDE.local.md
// (grove-512) — the operator's customizations, which a refresh never
// touches. A stub-only or absent overlay is not counted. Silent when none.
func printBrainOverlays(w io.Writer, rows []bootstrap.BrainRow) {
	n := 0
	for _, r := range rows {
		if r.Overlay {
			n++
		}
	}
	if n > 0 {
		fmt.Fprintf(w, "%d %s %s a %s overlay (operator-owned; the refresh never touches it)\n",
			n, plural(n, "brain"), map[bool]string{true: "carries", false: "carry"}[n == 1], bootstrap.OverlayFile)
	}
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
