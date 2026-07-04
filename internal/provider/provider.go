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

	"github.com/JollyGrin/grove/internal/config"
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

// FromConfig builds the configured provider. repoPath roots repo-relative
// providers (markdown); it may be empty for repo-independent ones (linear).
func FromConfig(cfg *config.Config, repoPath string) (Provider, error) {
	switch cfg.Provider.Kind {
	case "markdown":
		if repoPath == "" {
			return nil, fmt.Errorf("markdown provider needs a repo (pass --repo)")
		}
		return NewMarkdownAt(filepath.Join(repoPath, cfg.Provider.Markdown.Dir), cfg.Provider.Markdown.Dir), nil
	case "linear":
		key, err := cfg.APIKey()
		if err != nil {
			return nil, err
		}
		return NewLinear(key), nil
	default:
		return nil, fmt.Errorf("unknown provider kind %q (markdown|linear)", cfg.Provider.Kind)
	}
}
