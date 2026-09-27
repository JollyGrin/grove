package feature

import (
	"fmt"
	"sort"
	"strings"

	"github.com/JollyGrin/grove/internal/git"
	"github.com/JollyGrin/grove/internal/state"
)

// None is the `gv grab --feature none` opt-out: fork from the repo's base
// even when a ticket label names an open feature.
const None = "none"

// Choice is the base a grab forks from, and why (grove-373, Decision 2).
type Choice struct {
	// Feature is the open feature the task rides, nil off-train.
	Feature *state.Feature
	// Base is the branch the worktree forks from (and the PR targets):
	// the feature's branch, else the repo's base.
	Base string
	// Why is the operator-facing reason, printed as `base: <Base> (<Why>)`.
	Why string
}

// Line is grab's one stdout line naming the chosen base and why.
func (c Choice) Line() string { return fmt.Sprintf("base: %s (%s)", c.Base, c.Why) }

// ChooseForGrab resolves which feature, if any, a grab of a ticket on repo
// rides. flagVal is --feature verbatim: "none" opts out, a slug must name
// an OPEN feature on this repo. Absent a flag, the ticket's labels infer
// one: exactly one open feature whose label is on the ticket wins; two or
// more refuse (the operator passes --feature). Pure: no git, no I/O.
func ChooseForGrab(features map[string]*state.Feature, flagVal, repo, repoBase string, labels []string) (Choice, error) {
	off := Choice{Base: repoBase, Why: "repo base"}
	switch flagVal {
	case None:
		off.Why = "--feature none"
		return off, nil
	case "":
	default:
		f := state.OpenFeature(features, flagVal)
		if f == nil {
			return Choice{}, fmt.Errorf("no open feature %q — `gv feature ls` lists them", flagVal)
		}
		if f.Repo != repo {
			return Choice{}, fmt.Errorf("feature %s is on repo %s, not %s — grab it with --repo %s, or pass --feature none", f.Slug, f.Repo, repo, f.Repo)
		}
		return Choice{Feature: f, Base: f.Branch, Why: "feature " + f.Slug + ", from --feature"}, nil
	}

	has := make(map[string]bool, len(labels))
	for _, l := range labels {
		has[l] = true
	}
	var matches []*state.Feature
	for _, f := range features {
		if !f.Closed && f.Label != "" && has[f.Label] {
			matches = append(matches, f)
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Slug < matches[j].Slug })
	switch len(matches) {
	case 0:
		return off, nil
	case 1:
	default:
		names := make([]string, len(matches))
		for i, f := range matches {
			names[i] = fmt.Sprintf("%s (label %s)", f.Slug, f.Label)
		}
		return Choice{}, fmt.Errorf("ticket labels match %d open features: %s — pass --feature <slug>, or --feature none",
			len(matches), strings.Join(names, ", "))
	}
	f := matches[0]
	if f.Repo != repo {
		return Choice{}, fmt.Errorf("ticket label %s marks feature %s on repo %s, not %s — grab it with --repo %s, or pass --feature none",
			f.Label, f.Slug, f.Repo, repo, f.Repo)
	}
	return Choice{Feature: f, Base: f.Branch, Why: "feature " + f.Slug + ", from label " + f.Label}, nil
}

// ForkRef is the ref a grab's worktree forks from, after the caller's
// fetch. Off-train it is git.BaseRef (origin/<base>, else the local
// base). A feature lives on origin, so a feature grab forks from
// origin/<branch> only — a stale or same-named local branch never
// stands in for it.
func ForkRef(root string, c Choice) (string, error) {
	if c.Feature == nil {
		return git.BaseRef(root, c.Base)
	}
	ref := "origin/" + c.Base
	if _, err := git.RevParse(root, "refs/remotes/"+ref); err != nil {
		return "", fmt.Errorf("feature %s: %s not found — did the fetch fail? (`gv feature ls` shows its branch)", c.Feature.Slug, ref)
	}
	return ref, nil
}
