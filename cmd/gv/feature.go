package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/feature"
	"github.com/JollyGrin/grove/internal/git"
	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/ledger"
	"github.com/JollyGrin/grove/internal/provider"
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
	}
	return fmt.Errorf("unknown `gv feature %s` (want new|ls|close)", args[0])
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
	parseAnywhere(fs, args)
	features, err := state.LoadFeatures(stateDir())
	if err != nil {
		return err
	}
	rows := feature.Rows(features, *all)
	statuses, lookupErr := featureStatuses(features, !*noPR, !*noQueued)
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

// featureStatuses wires the real inputs into feature.Statuses: the full
// event log (landed cars must survive sweep), the fold, the cost ledger,
// the configured repos' checkouts, and — unless skipped — the backend's
// open issues and gh. A missing config only loses the repo-rooted fields.
func featureStatuses(features map[string]*state.Feature, withPR, withQueued bool) (map[string]*feature.Status, error) {
	events, err := state.ReadEvents(stateDir(), 0)
	if err != nil {
		return nil, err
	}
	tasks, err := state.Peek(stateDir())
	if err != nil {
		return nil, err
	}
	rows, _ := ledger.Read(stateDir()) // no ledger = no estimates
	cfg, _ := loadCfg()
	repo := func(name string) *config.Repo {
		if cfg == nil {
			return nil
		}
		if r, ok := cfg.Repos[name]; ok {
			return r
		}
		return nil
	}
	in := feature.StatusInput{
		Features: features, Tasks: tasks, Events: events, Ledger: rows,
		SkipQueued: !withQueued,
		Git:        git.Run,
		RepoDir: func(name string) string {
			if r := repo(name); r != nil {
				return r.Path
			}
			return ""
		},
		Issues: func(f *state.Feature) ([]*provider.Task, error) {
			r := repo(f.Repo)
			if r == nil {
				return nil, fmt.Errorf("repo %q is not configured", f.Repo)
			}
			prov, err := provider.FromConfigKind(cfg, cfg.ProviderKindFor(r), f.Repo, r.Path)
			if err != nil {
				return nil, err
			}
			if gh, ok := prov.(*provider.GitHub); ok {
				return gh.ListLabeled(f.Label)
			}
			return prov.List()
		},
	}
	if withPR {
		in.PR = func(f *state.Feature) (*github.PR, error) {
			r := repo(f.Repo)
			if r == nil {
				return nil, nil
			}
			return github.PRForBranch(r.Path, f.Branch)
		}
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

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
