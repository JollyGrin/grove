// Package feature holds the decisions behind `gv feature new/ls/close`
// (grove-372, feature trains Decision 1): slug rules, the create/adopt git
// plumbing, and the `gv feature ls --json` rows. A feature is runtime
// state — it lives in events.jsonl (state.EvFeatureCreated/Closed), never
// in config. cmd/gv/feature.go is the thin glue.
package feature

import (
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/JollyGrin/grove/internal/git"
	"github.com/JollyGrin/grove/internal/state"
)

var slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidateSlug: lowercase [a-z0-9-], no leading/trailing/double hyphen —
// the slug names a branch, a label and (later) a tmux window.
func ValidateSlug(slug string) error {
	if !slugRe.MatchString(slug) {
		return fmt.Errorf("invalid feature slug %q: use lowercase letters, digits and single hyphens (e.g. feature-trains)", slug)
	}
	return nil
}

// Spec is one `gv feature new` request.
type Spec struct {
	Slug, Repo, Branch, Base, Label string
	Adopt                           bool
}

// WithDefaults fills branch feature/<slug>, base main, label <slug>.
func (s Spec) WithDefaults() Spec {
	if s.Branch == "" {
		s.Branch = "feature/" + s.Slug
	}
	if s.Base == "" {
		s.Base = "main"
	}
	if s.Label == "" {
		s.Label = s.Slug
	}
	return s
}

// CheckNew is the pure refusal logic: a valid slug that is not already
// open. A closed slug may be re-created.
func CheckNew(features map[string]*state.Feature, s Spec) error {
	if err := ValidateSlug(s.Slug); err != nil {
		return err
	}
	if s.Repo == "" {
		return fmt.Errorf("--repo is required")
	}
	if f := state.OpenFeature(features, s.Slug); f != nil {
		return fmt.Errorf("feature %q is already open (branch %s) — `gv feature close %s` first", s.Slug, f.Branch, s.Slug)
	}
	return nil
}

// Result reports what New did.
type Result struct {
	Spec    Spec
	SHA     string // the branch tip on origin at registration
	Created bool   // false: --adopt registered an existing branch
}

// New registers a feature: without Adopt it creates spec.Branch on origin
// at origin/<base> (never touching a local branch) and refuses when the
// branch already exists there; with Adopt it requires the branch on origin
// and pushes nothing. Either way the feature_created event is appended
// last, only after the git side succeeded. It never opens a PR.
func New(stateDir, repoRoot string, spec Spec) (Result, error) {
	s := spec.WithDefaults()
	features, err := state.LoadFeatures(stateDir)
	if err != nil {
		return Result{}, err
	}
	if err := CheckNew(features, s); err != nil {
		return Result{}, err
	}
	if !git.HasRemote(repoRoot, "origin") {
		return Result{}, fmt.Errorf("repo %s has no origin remote — a feature branch must live on origin", s.Repo)
	}
	remote, err := git.RemoteHead(repoRoot, s.Branch)
	if err != nil {
		return Result{}, err
	}
	res := Result{Spec: s, SHA: remote}
	if s.Adopt {
		if remote == "" {
			return Result{}, fmt.Errorf("--adopt: branch %s is not on origin — drop --adopt to create it", s.Branch)
		}
	} else {
		if remote != "" {
			return Result{}, fmt.Errorf("branch %s already exists on origin — pass --adopt to register it as feature %q", s.Branch, s.Slug)
		}
		if err := git.Fetch(repoRoot, "origin", s.Base); err != nil {
			return Result{}, err
		}
		sha, err := git.RevParse(repoRoot, "origin/"+s.Base)
		if err != nil {
			return Result{}, fmt.Errorf("base origin/%s not found: %w", s.Base, err)
		}
		if err := git.CreateRemoteBranch(repoRoot, sha, s.Branch); err != nil {
			return Result{}, err
		}
		res.SHA, res.Created = sha, true
	}
	err = state.Append(stateDir, state.Event{Type: state.EvFeatureCreated, Data: map[string]string{
		"slug": s.Slug, "repo": s.Repo, "branch": s.Branch, "base": s.Base, "label": s.Label,
	}})
	return res, err
}

// Close appends feature_closed for an open slug. reason "" means merged.
// Deletes nothing.
func Close(stateDir, slug, reason string) (*state.Feature, error) {
	if reason == "" {
		reason = state.FeatureMerged
	}
	if reason != state.FeatureMerged && reason != state.FeatureAbandoned {
		return nil, fmt.Errorf("--reason must be %s or %s, got %q", state.FeatureMerged, state.FeatureAbandoned, reason)
	}
	features, err := state.LoadFeatures(stateDir)
	if err != nil {
		return nil, err
	}
	f := state.OpenFeature(features, slug)
	if f == nil {
		return nil, fmt.Errorf("no open feature %q — `gv feature ls --all` lists them", slug)
	}
	err = state.Append(stateDir, state.Event{Type: state.EvFeatureClosed, Data: map[string]string{
		"slug": slug, "reason": reason,
	}})
	return f, err
}

// Row is one `gv feature ls --json` entry (docs/plugins.md). Additive
// only: ticket 04 adds status fields here.
type Row struct {
	Slug      string    `json:"slug"`
	Repo      string    `json:"repo"`
	Branch    string    `json:"branch"`
	Base      string    `json:"base"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
	Closed    *Closure  `json:"closed,omitempty"`
}

// Closure is present on a closed feature's row only.
type Closure struct {
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// Rows lists open features (all: closed ones too), oldest first.
func Rows(features map[string]*state.Feature, all bool) []Row {
	rows := []Row{}
	for _, f := range features {
		if f.Closed && !all {
			continue
		}
		r := Row{Slug: f.Slug, Repo: f.Repo, Branch: f.Branch, Base: f.Base, Label: f.Label, CreatedAt: f.CreatedAt}
		if f.Closed {
			r.Closed = &Closure{Reason: f.ClosedReason, At: f.ClosedAt}
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].CreatedAt.Before(rows[j].CreatedAt)
		}
		return rows[i].Slug < rows[j].Slug
	})
	return rows
}
