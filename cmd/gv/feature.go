package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/feature"
	"github.com/JollyGrin/grove/internal/fleet"
	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/schema"
	"github.com/JollyGrin/grove/internal/state"
)

// cmdFeature is the `gv feature new|ls|close` glue (grove-372). Decisions
// live in internal/feature; features are events in the ambient state dir.
func cmdFeature(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: gv feature new|ls|close …")
	}
	switch args[0] {
	case "new":
		return cmdFeatureNew(args[1:])
	case "ls":
		return cmdFeatureLs(args[1:])
	case "close":
		return cmdFeatureClose(args[1:])
	case "land":
		return cmdFeatureLand(args[1:])
	}
	return fmt.Errorf("unknown `gv feature %s` (want new|ls|close|land)", args[0])
}

func cmdFeatureNew(args []string) error {
	fs := flag.NewFlagSet("feature new", flag.ExitOnError)
	repo := fs.String("repo", "", "repo name from config (required)")
	branch := fs.String("branch", "", "feature branch (default feature/<slug>)")
	base := fs.String("base", "main", "branch the feature forks from and merges back to")
	label := fs.String("label", "", "issue label that marks the feature's tickets (default <slug>)")
	adopt := fs.Bool("adopt", false, "register a branch that already exists on origin (pushes nothing)")
	pos := parseAnywhere(fs, args)
	if len(pos) != 1 {
		return fmt.Errorf("usage: gv feature new <slug> --repo R [--branch B] [--base main] [--label L] [--adopt]")
	}
	if *repo == "" {
		return fmt.Errorf("--repo is required")
	}
	echoWorkspace()
	cfg, err := loadCfg()
	if err != nil {
		return err
	}
	r, ok := cfg.Repos[*repo]
	if !ok {
		names := make([]string, 0, len(cfg.Repos))
		for n := range cfg.Repos {
			names = append(names, n)
		}
		sort.Strings(names)
		return fmt.Errorf("unknown repo %q (configured: %s)", *repo, strings.Join(names, ", "))
	}
	res, err := feature.New(stateDir(), r.Path, feature.Spec{
		Slug: pos[0], Repo: *repo, Branch: *branch, Base: *base, Label: *label, Adopt: *adopt,
	})
	if err != nil {
		return err
	}
	s := res.Spec
	verb := "created and pushed"
	if !res.Created {
		verb = "adopted"
	}
	fmt.Printf("✓ feature %s: %s %s at %s (base %s, label %s)\n", s.Slug, verb, s.Branch, shortSHA(res.SHA), s.Base, s.Label)
	return nil
}

func cmdFeatureLs(args []string) error {
	fs := flag.NewFlagSet("feature ls", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	all := fs.Bool("all", false, "include closed features")
	noPR := fs.Bool("no-pr", false, "skip the feature PR lookup (gh)")
	noQueued := fs.Bool("no-queued", false, "skip the queued-issue lookup (the backend's open issues)")
	noRemote := fs.Bool("no-remote", false, "skip asking the configured hosts for their cars (ssh)")
	parseAnywhere(fs, args)
	features, err := state.LoadFeatures(stateDir())
	if err != nil {
		return err
	}
	rows := feature.Rows(features, *all)
	cfg, _ := loadCfg()
	statuses, lookupErr := featureStatuses(cfg, features, !*noPR, !*noQueued, !*noRemote, nil)
	if lookupErr != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", lookupErr)
	}
	for i := range rows {
		rows[i].Status = statuses[rows[i].Slug] // nil on a closed row
	}
	serveStatuses(rows)
	if *asJSON {
		return emitJSON("features", rows)
	}
	if len(rows) == 0 {
		fmt.Println("no open features — gv feature new <slug> --repo R")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SLUG\tREPO\tBRANCH\tBASE\tLABEL\tCARS\tBEHIND\tCREATED\tSTATE")
	for _, r := range rows {
		st := "open"
		if r.Closed != nil {
			st = "closed (" + r.Closed.Reason + ")"
		}
		cars, behind := "-", "-"
		if r.Status != nil {
			cars = fmt.Sprintf("%d/%d", r.Landed, r.Total)
			if r.BehindBase != nil {
				behind = fmt.Sprintf("↓%d %s", *r.BehindBase, r.Base)
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.Slug, r.Repo, r.Branch, r.Base, r.Label, cars, behind,
			r.CreatedAt.Local().Format("2006-01-02 15:04"), st)
	}
	return w.Flush()
}

// featureStatuses computes every open feature's status off the live
// inputs (feature.LiveInput). A nil config only loses the repo-rooted
// fields. withRemote=false (--no-remote) never asks the hosts; run is the
// host runner (nil = ssh).
func featureStatuses(cfg *config.Config, features map[string]*state.Feature, withPR, withQueued, withRemote bool, run fleet.Runner) (map[string]*feature.Status, error) {
	in, err := feature.LiveInput(cfg, stateDir(), features, withPR, withQueued)
	if err != nil {
		return nil, err
	}
	in.Remote = nil
	if withRemote {
		in.Remote = feature.RemoteLookup(cfg, run)
	}
	return feature.Statuses(in)
}

func cmdFeatureClose(args []string) error {
	fs := flag.NewFlagSet("feature close", flag.ExitOnError)
	reason := fs.String("reason", state.FeatureMerged, "merged | abandoned")
	pos := parseAnywhere(fs, args)
	if len(pos) != 1 {
		return fmt.Errorf("usage: gv feature close <slug> [--reason merged|abandoned]")
	}
	f, err := feature.Close(stateDir(), pos[0], *reason)
	if err != nil {
		return err
	}
	if cfg, err := loadCfg(); err == nil {
		teardownServe(cfg, f)
	}
	fmt.Printf("✓ feature %s closed (%s) — branch %s left as is\n", f.Slug, *reason, f.Branch)
	return nil
}

// cmdFeatureLand is `gv feature land <slug>` (feature trains Decision 6):
// build the plan (every tracked, not-done car whose PR is MERGED), show
// it, and on confirm run finishTask per row. It never closes an issue or
// comments on one — that's the orchestrator's act, on the operator's
// order (Decision 8).
func cmdFeatureLand(args []string) error {
	fs := flag.NewFlagSet("feature land", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable output; without --yes, a dry run that never prompts")
	yes := fs.Bool("yes", false, "land without confirming")
	pos := parseAnywhere(fs, args)
	if len(pos) != 1 {
		return fmt.Errorf("usage: gv feature land <slug> [--json] [--yes]")
	}
	slug := pos[0]
	// The nested-workspace guard is for a human watching a mutating verb
	// act (DESIGN §14); `--json` is the plugin contract's pure envelope —
	// docs/plugins.md promises stdout is exactly one JSON object.
	if !*asJSON {
		echoWorkspace()
	}

	cfg, err := loadCfg()
	if err != nil {
		return err
	}
	features, err := state.LoadFeatures(stateDir())
	if err != nil {
		return err
	}
	if state.OpenFeature(features, slug) == nil {
		return fmt.Errorf("no open feature %q — `gv feature ls --all` lists them", slug)
	}
	tasks, err := state.Load(stateDir())
	if err != nil {
		return err
	}

	plan, lookupErr := feature.BuildLandPlan(feature.LandInput{
		Tasks: tasks, Slug: slug,
		PR: func(t *state.Task) (*github.PR, error) {
			repo, ok := cfg.Repos[t.Repo]
			if !ok {
				return nil, fmt.Errorf("repo %q no longer in config", t.Repo)
			}
			return github.PRForBranch(repo.Path, t.Branch)
		},
	})
	if lookupErr != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", lookupErr)
	}
	// Queued cars (open backend issues under the feature's label, not yet
	// grabbed) reuse the same lookup `gv feature ls` already does — no PR
	// lookup needed here, land does its own fresher one above. Hosts are
	// asked too (grove-398), so a car running on one is not mislabelled
	// queued.
	if statuses, err := featureStatuses(cfg, features, false, true, true, nil); err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
	} else if st := statuses[slug]; st != nil {
		for _, c := range st.Cars {
			if c.State == feature.CarQueued {
				plan.Skipped = append(plan.Skipped, feature.SkipRow{Ticket: c.Ticket, Number: c.Number, Reason: feature.SkipQueued})
			}
		}
	}

	finish := func(ticket string) error {
		t, ok := tasks[ticket]
		if !ok {
			return fmt.Errorf("%s: no longer tracked", ticket)
		}
		return finishTask(cfg, t, false)
	}

	if *asJSON && !*yes {
		return printLandJSON(slug, plan, nil)
	}

	if !*asJSON {
		printLandTable(slug, plan)
	}
	if len(plan.Land) == 0 {
		if *asJSON {
			return printLandJSON(slug, plan, &feature.LandResult{Landed: []int{}, Failed: []feature.LandFailure{}})
		}
		fmt.Println("landed: none")
		return nil
	}
	if !*yes {
		fmt.Printf("land %d? [y/N] ", len(plan.Land))
		sc := bufio.NewScanner(os.Stdin)
		if !(sc.Scan() && strings.ToLower(strings.TrimSpace(sc.Text())) == "y") {
			fmt.Println("landed: none")
			return nil
		}
	}

	res := feature.Land(plan, finish)
	if *asJSON {
		if err := printLandJSON(slug, plan, &res); err != nil {
			return err
		}
	} else if len(res.Landed) == 0 {
		fmt.Println("landed: none")
	} else {
		nums := make([]string, len(res.Landed))
		for i, n := range res.Landed {
			nums[i] = fmt.Sprintf("#%d", n)
		}
		fmt.Println("landed: " + strings.Join(nums, " "))
	}
	if len(res.Failed) > 0 {
		for _, f := range res.Failed {
			fmt.Fprintf(os.Stderr, "  %s: %s\n", f.Ticket, f.Error)
		}
		return fmt.Errorf("%d of %d ticket(s) failed to land", len(res.Failed), len(plan.Land))
	}
	return nil
}

func printLandTable(slug string, plan feature.LandPlan) {
	if len(plan.Land) == 0 && len(plan.Skipped) == 0 {
		fmt.Printf("no cars on feature %s\n", slug)
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TICKET\tPR\tACTION")
	for _, r := range plan.Land {
		fmt.Fprintf(w, "%s\t#%d\tland\n", r.Ticket, r.PR)
	}
	for _, s := range plan.Skipped {
		fmt.Fprintf(w, "%s\t-\tskip (%s)\n", s.Ticket, s.Reason)
	}
	w.Flush()
}

// printLandJSON is `gv feature land --json`'s envelope (docs/plugins.md):
// flat, not nested under one key. A map, not a struct with `omitempty` —
// `landed`/`failed` must be genuinely absent on a dry run (res == nil)
// but present as `[]` once a run actually executed with nothing to show,
// which plain `omitempty` on a struct field cannot tell apart.
func printLandJSON(slug string, plan feature.LandPlan, res *feature.LandResult) error {
	out := map[string]any{
		"schema_version": schema.Version,
		"feature":        slug,
		"land":           plan.Land,
		"skipped":        plan.Skipped,
	}
	if res != nil {
		out["landed"] = res.Landed
		out["failed"] = res.Failed
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
