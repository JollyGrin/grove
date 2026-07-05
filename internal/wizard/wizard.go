// Package wizard builds gv init's detect-then-confirm steps and resolves
// their answers (plan 2026-07-04-phase-1a Task 6). ALL decision logic —
// step set, value precedence, --yes semantics, --only filtering — lives
// here under test; the huh rendering in cmd/gv is a thin loop.
//
// Value precedence (plan-review round 2): an existing non-empty
// (hand-edited) config value always wins over fresh detection; --yes only
// fills unset fields. Explicit flags outrank both — the human typed them
// this run.
package wizard

import (
	"fmt"
	"strings"

	"github.com/JollyGrin/grove/internal/bootstrap"
	"github.com/JollyGrin/grove/internal/probe"
)

// Scope values mirror internal/workspace (string-typed here to avoid the
// import until something needs more than the literal).
const (
	ScopeRepo   = "repo"
	ScopeParent = "parent"
)

type StepKind int

const (
	KindInput   StepKind = iota // free-text, prefilled
	KindSelect                  // one of Options
	KindConfirm                 // yes/no
)

// Step is one wizard screen. Value carries the resolved answer: pre-filled
// by Build (precedence applied), overwritten by the interactive runner.
type Step struct {
	ID       string // the --only namespace: repo·setup·worker·provider·ntfy·hooks·agents-md
	Title    string
	Detected string
	Current  string
	Options  []string
	Kind     StepKind
	Value    string
	On       bool // for KindConfirm
}

// StepIDs is the --only namespace, in run order.
var StepIDs = []string{"repo", "setup", "worker", "provider", "ntfy", "hooks", "agents-md", "workspace"}

// Flags are the prompt twins. Empty string / false = not provided.
type Flags struct {
	Yes           bool
	Only          string
	Label         string
	Base          string
	Setup         string
	Worker        string
	Provider      string
	Ntfy          string
	Hooks         bool
	NoHooks       bool
	AgentsMD      bool
	NoAgentsMD    bool
	ForceAgentsMD bool
}

// Input is everything Build needs to decide the step set.
type Input struct {
	Probe          *probe.Probe
	RepoName       string
	RepoPath       string
	Doc            *bootstrap.Doc // current global config
	HooksInstalled bool
	HooksPaths     []string // worker-profile settings.json files hooks would land in
	Scope          string   // workspace scope: repo | parent
	DetectedLabel  string   // default workspace label (root basename)
	Flags          Flags
}

// resolve applies the precedence contract for one string value.
func resolve(flag, current, detected string) string {
	if flag != "" {
		return flag
	}
	if current != "" {
		return current
	}
	return detected
}

// Build returns the wizard steps with Value pre-resolved (what --yes would
// write, and what the interactive runner pre-fills).
func Build(in Input) ([]Step, error) {
	cur := func(key string) string { return in.Doc.Get("repos", in.RepoName, key) }
	f := in.Flags

	detBase := in.Probe.DefaultBranch
	if detBase == "" {
		detBase = "main"
	}
	// The worker command has no *detection* — only a configured value or an
	// interactive choice. --yes must never invent one (it would silently
	// override config.Load's default posture).
	curWorker := cur("claude")

	wsStep := Step{
		ID: "workspace", Kind: KindInput,
		Title:    fmt.Sprintf("workspace label — the cockpit becomes grove-<label>, scope: %s", in.Scope),
		Detected: in.DetectedLabel, Current: in.Doc.Get("workspace", "label"),
		Value: resolve(f.Label, in.Doc.Get("workspace", "label"), in.DetectedLabel),
	}

	steps := []Step{
		wsStep,
		{
			ID: "repo", Kind: KindInput,
			Title:    fmt.Sprintf("base branch for %s (task worktrees fork from it)", in.RepoName),
			Detected: detBase, Current: cur("base"),
			Value: resolve(f.Base, cur("base"), detBase),
		},
		{
			ID: "setup", Kind: KindInput,
			Title:    "setup command for fresh worktrees (empty = none)",
			Detected: in.Probe.Setup, Current: cur("setup"),
			Value: resolve(f.Setup, cur("setup"), in.Probe.Setup),
		},
		{
			ID: "worker", Kind: KindSelect,
			Title: "worker command (what runs in each task pane) — autonomy " +
				"(--dangerously-skip-permissions) is YOUR call: without a safety-" +
				"guard layer an autonomous worker edits and runs anything it likes",
			Options: []string{
				"claude",
				"claude --dangerously-skip-permissions",
				"ccwork --dangerously-skip-permissions",
			},
			Current: curWorker,
			Value:   resolve(f.Worker, curWorker, ""),
		},
		{
			ID: "provider", Kind: KindSelect,
			Title:    "task backend (github-issues adapter is on the roadmap — Phase 3)",
			Options:  providerOptions(in),
			Detected: "markdown", Current: in.Doc.Get("provider", "kind"),
			Value: resolve(f.Provider, in.Doc.Get("provider", "kind"), "markdown"),
		},
		{
			ID: "ntfy", Kind: KindInput,
			Title:    "ntfy topic URL for phone push (empty = off)",
			Detected: "", Current: in.Doc.Get("notify", "ntfy"),
			Value: resolve(f.Ntfy, in.Doc.Get("notify", "ntfy"), ""),
		},
		{
			ID: "hooks", Kind: KindConfirm,
			Title:   "install the session hooks (live status/question capture) into " + hooksPathsLabel(in.HooksPaths),
			Current: boolWord(in.HooksInstalled),
			On:      resolveHooks(f, in.HooksInstalled),
		},
		{
			ID: "agents-md", Kind: KindConfirm,
			Title: "generate AGENTS.md — a one-shot claude session walks the repo " +
				"and writes its portable brain (runs with skip-permissions for this " +
				"write; you review + commit the result)",
			Current: in.Probe.AgentContext.Kind,
			On:      resolveAgentsMD(f, in),
		},
	}

	// Parent scope: per-repo steps don't apply (children register as
	// path+base entries); workspace-wide steps remain.
	if in.Scope == ScopeParent {
		var kept []Step
		for _, s := range steps {
			switch s.ID {
			case "workspace", "provider", "ntfy", "hooks":
				kept = append(kept, s)
			}
		}
		steps = kept
	}

	if f.Only != "" {
		for _, s := range steps {
			if s.ID == f.Only {
				return []Step{s}, nil
			}
		}
		return nil, fmt.Errorf("unknown --only step %q (valid: %s)", f.Only, strings.Join(StepIDs, " · "))
	}
	return steps, nil
}

func providerOptions(in Input) []string {
	opts := []string{"markdown"}
	if in.Probe.LinearKeySet {
		opts = append(opts, "linear")
	}
	return opts
}

// resolveHooks: safe-mechanical but touches the SHARED worker settings —
// --yes never installs unless already installed or explicitly asked.
// `--only hooks` IS the explicit ask (naming the one step is consent).
func resolveHooks(f Flags, installed bool) bool {
	switch {
	case f.NoHooks:
		return false
	case f.Hooks, f.Only == "hooks":
		return true
	case f.Yes:
		return installed // keep state; never a new outward write under --yes
	default:
		return true // interactive default: offer ON (human confirms)
	}
}

// resolveAgentsMD: a paid LLM run — OFF under --yes unless explicitly
// asked (the --agents-md flag or `--only agents-md`); interactively
// offered only when no agent context exists yet.
func resolveAgentsMD(f Flags, in Input) bool {
	switch {
	case f.NoAgentsMD:
		return false
	case f.AgentsMD, f.Only == "agents-md":
		return true
	case f.Yes:
		return false
	default:
		return in.Probe.AgentContext.Kind == "" // offer when the repo has no brain
	}
}

// Answers extracts the final decisions from (possibly runner-edited) steps.
type Answers struct {
	Label, Base, Setup, Worker, Provider, Ntfy string
	InstallHooks, RunAgentsMD                  bool
}

func Collect(steps []Step) Answers {
	var a Answers
	for _, s := range steps {
		switch s.ID {
		case "workspace":
			a.Label = s.Value
		case "repo":
			a.Base = s.Value
		case "setup":
			a.Setup = s.Value
		case "worker":
			a.Worker = s.Value
		case "provider":
			a.Provider = s.Value
		case "ntfy":
			a.Ntfy = s.Value
		case "hooks":
			a.InstallHooks = s.On
		case "agents-md":
			a.RunAgentsMD = s.On
		}
	}
	return a
}

// Apply writes the confirmed answers into the config doc (comment-
// preserving; no-ops never dirty the file). Empty answers for absent
// steps (--only runs) are skipped — Apply only ever narrows to what was
// actually asked.
func Apply(in Input, steps []Step) {
	a := Collect(steps)
	has := func(id string) bool {
		for _, s := range steps {
			if s.ID == id {
				return true
			}
		}
		return false
	}
	if has("workspace") && a.Label != "" {
		in.Doc.Set(a.Label, "workspace", "label")
		if in.Scope != "" {
			in.Doc.Set(in.Scope, "workspace", "scope")
		}
	}
	if has("repo") {
		in.Doc.SetRepoField(in.RepoName, "path", in.RepoPath)
		if a.Base != "" {
			in.Doc.SetRepoField(in.RepoName, "base", a.Base)
		}
	}
	if has("setup") && a.Setup != "" {
		in.Doc.SetRepoField(in.RepoName, "setup", a.Setup)
	}
	if has("worker") && a.Worker != "" {
		in.Doc.SetRepoField(in.RepoName, "claude", a.Worker)
	}
	if has("provider") && a.Provider != "" {
		in.Doc.Set(a.Provider, "provider", "kind")
	}
	if has("ntfy") && a.Ntfy != "" {
		in.Doc.Set(a.Ntfy, "notify", "ntfy")
	}
}

// hooksPathsLabel names the worker-profile settings files hooks land in —
// derived from the configured worker commands, so a personal-claude fleet
// sees ~/.claude, not the Grid's ~/.cc-work.
func hooksPathsLabel(paths []string) string {
	if len(paths) == 0 {
		return "your worker profile's settings.json"
	}
	return strings.Join(paths, " + ")
}

func boolWord(b bool) string {
	if b {
		return "installed"
	}
	return ""
}
