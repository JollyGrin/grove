// Package provider is the TaskProvider seam (DESIGN.md §5): the single
// abstraction that makes grove backend-agnostic. Providers are read-biased
// by design — the binary never transitions a task; it hands each provider's
// transition *instructions* (Verbs) to the worker agent via the kickoff
// prompt, and humans own terminal state.
//
// P0 subset of DESIGN §5.1 (recorded in docs/seed-manifest.md): Attach and
// the fuller Capabilities set (canTransition, canComment, autoLinksPR) are
// deferred until a write path needs them.
package provider

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/linear"
)

type Comment struct {
	Author string
	Body   string
}

// Task is the provider-neutral task shape consumed by grab/adopt/kickoff.
type Task struct {
	ID          string
	Title       string
	Description string
	URL         string
	Status      string // provider-native; markdown: backlog|todo|in-progress|review|done
	Labels      []string
	Comments    []Comment
}

// Verbs are the transition *instructions* rendered into kickoff prompts.
// The agent performs them; the binary never does (DESIGN.md §5.4).
type Verbs struct {
	Start  string // how the agent moves the task to in-progress
	Review string // how the agent moves the task to review
}

type Capabilities struct {
	CanList bool // provider can enumerate a grabbable backlog (gv grab with no args)
}

type Provider interface {
	Kind() string
	ParseID(raw string) (string, error)
	Get(id string) (*Task, error)
	List() ([]*Task, error) // grabbable backlog; in-flight filtering is the caller's job (event-state is authoritative)
	Verbs() Verbs
	Capabilities() Capabilities
}

// FromConfig builds the globally-configured provider. repoPath roots
// repo-relative providers (markdown); it may be empty for repo-independent
// ones (linear).
func FromConfig(cfg *config.Config, repoPath string) (Provider, error) {
	return FromConfigKind(cfg, cfg.Provider.Kind, filepath.Base(repoPath), repoPath)
}

// FromConfigKind builds a provider of an explicit kind — the per-repo
// override path (config.ProviderKindFor). repoName keys github's
// fleet-unique ids; markdown/linear ignore it.
func FromConfigKind(cfg *config.Config, kind, repoName, repoPath string) (Provider, error) {
	switch kind {
	case "markdown":
		if repoPath == "" {
			return nil, fmt.Errorf("markdown provider needs a repo (pass --repo)")
		}
		return NewMarkdownAt(filepath.Join(repoPath, cfg.Provider.Markdown.Dir), cfg.Provider.Markdown.Dir), nil
	case "github":
		if repoPath == "" {
			return nil, fmt.Errorf("github provider needs a repo (pass --repo)")
		}
		return NewGitHub(repoPath, repoName), nil
	case "linear":
		key, err := cfg.APIKey()
		if err != nil {
			return nil, err
		}
		return NewLinear(key), nil
	default:
		return nil, fmt.Errorf("unknown provider kind %q (markdown|linear|github)", kind)
	}
}

// IDCandidates returns every normalization a raw task reference could
// resolve to — with per-repo providers, linear (DEV-1234) and markdown
// (task-001) id shapes are live in one fleet at once, and "task-001"
// uppercases into a string the linear regex also matches. Callers try
// candidates against tracked state; the first hit wins.
func IDCandidates(raw string) []string {
	var out []string
	seen := map[string]bool{}
	if id, err := linear.ParseIdentifier(strings.ToUpper(raw)); err == nil && !seen[id] {
		seen[id] = true
		out = append(out, id)
	}
	if id, err := NewMarkdown("").ParseID(raw); err == nil && !seen[id] {
		seen[id] = true
		out = append(out, id)
	}
	return out
}
