package feature

import (
	"fmt"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/git"
	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/ledger"
	"github.com/JollyGrin/grove/internal/provider"
	"github.com/JollyGrin/grove/internal/state"
)

// LiveInput wires the real inputs into a StatusInput — shared by `gv
// feature ls` and the cockpit's PR-cadence pass (grove-377): the full
// event log (landed cars must survive sweep), the fold, the cost ledger,
// the configured repos' checkouts, and — unless skipped — the backend's
// open issues and gh. A nil cfg only loses the repo-rooted fields.
func LiveInput(cfg *config.Config, stateDir string, features map[string]*state.Feature, withPR, withQueued bool) (StatusInput, error) {
	events, err := state.ReadEvents(stateDir, 0)
	if err != nil {
		return StatusInput{}, err
	}
	tasks, err := state.Peek(stateDir)
	if err != nil {
		return StatusInput{}, err
	}
	rows, _ := ledger.Read(stateDir) // no ledger = no estimates
	repo := func(name string) *config.Repo {
		if cfg == nil {
			return nil
		}
		if r, ok := cfg.Repos[name]; ok {
			return r
		}
		return nil
	}
	in := StatusInput{
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
	// The GitHub landed lookup (grove-397) rides the queued pass; other
	// providers leave both nil and landed comes from events alone.
	ghRepo := func(f *state.Feature) (*config.Repo, *provider.GitHub, error) {
		r := repo(f.Repo)
		if r == nil {
			return nil, nil, nil // Issues already reports it
		}
		prov, err := provider.FromConfigKind(cfg, cfg.ProviderKindFor(r), f.Repo, r.Path)
		if err != nil {
			return nil, nil, err
		}
		gh, _ := prov.(*provider.GitHub)
		return r, gh, nil
	}
	in.ClosedIssues = func(f *state.Feature) ([]*provider.Task, error) {
		_, gh, err := ghRepo(f)
		if err != nil || gh == nil {
			return nil, err
		}
		return gh.ListClosedLabeled(f.Label)
	}
	in.MergedPRs = func(f *state.Feature) ([]github.MergedPR, error) {
		r, gh, err := ghRepo(f)
		if err != nil || gh == nil {
			return nil, err
		}
		return github.MergedInto(r.Path, f.Branch)
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
	return in, nil
}
