package connections

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JollyGrin/grove/internal/config"
)

// Core returns the built-in connection instances, in render order. Rows
// derived from repo config only exist when the config loaded cleanly —
// the config row itself carries the load error otherwise.
func Core(env Env) []Connection {
	var conns []Connection

	type bin struct {
		name        string
		requiredFor []string
	}
	for _, b := range []bin{
		{"tmux", []string{"grab", "ui", "attach"}},
		{"gh", []string{"grab", "done", "audit"}},
		{"git", []string{"grab", "done"}},
	} {
		conns = append(conns, Connection{
			ID:          "binary:" + b.name,
			Kind:        KindBinary,
			Severity:    SeverityError,
			RequiredFor: b.requiredFor,
			Title:       b.name + " installed",
			Fix:         "brew install " + b.name,
			Check:       checkBinary(b.name),
		})
	}

	// Desktop notifications are darwin-only and nice-to-have: warn, and
	// the row doesn't exist elsewhere (deliberate change from the P0
	// doctor, logged in docs/seed-manifest.md).
	if env.GOOS == "darwin" {
		conns = append(conns, Connection{
			ID:          "binary:terminal-notifier",
			Kind:        KindBinary,
			Severity:    SeverityWarn,
			RequiredFor: []string{"notify"},
			Title:       "terminal-notifier installed",
			Fix:         "brew install terminal-notifier",
			Check:       checkBinary("terminal-notifier"),
		})
	}

	conns = append(conns, Connection{
		ID:          "binary:claude",
		Kind:        KindBinary,
		Severity:    SeverityError,
		RequiredFor: []string{"grab"},
		Title:       "claude installed",
		Fix:         "brew install claude",
		Check:       checkBinary("claude"),
	})

	conns = append(conns, Connection{
		ID:          "gh-auth",
		Kind:        KindCLIAuth,
		Severity:    SeverityError,
		RequiredFor: []string{"grab", "done", "audit"},
		Title:       "gh authenticated",
		Fix:         "gh auth login",
		Check:       checkCLIAuth("gh", "auth", "status"),
	})

	conns = append(conns, Connection{
		ID:          "config",
		Kind:        KindFile,
		Severity:    SeverityError,
		RequiredFor: []string{"grab", "ls", "ui"},
		Title:       "config.yaml",
		Fix:         "gv init",
		Check:       checkConfig,
	})

	if env.Cfg != nil && env.CfgErr == nil {
		conns = append(conns, providerConnections(env)...)
		conns = append(conns, workerConnections(env)...)
		conns = append(conns, agentsMdConnections(env)...)
	}

	conns = append(conns, Connection{
		ID:          "hooks",
		Step:        "hooks",
		Kind:        KindHooks,
		Severity:    SeverityError,
		RequiredFor: []string{"ls", "ui"},
		Title:       "gv hooks installed",
		Fix:         "gv hooks install",
		Check:       checkHooks,
	})

	return conns
}

func checkConfig(e Env) Status {
	if e.CfgErr != nil {
		return Status{State: StateMissing, Info: e.CfgErr.Error()}
	}
	return Status{State: StateOK, Info: fmt.Sprintf("%d repo(s)", len(e.Cfg.Repos))}
}

// providerConnections declares readiness for the configured task backend.
// The linear key row exists only when the linear provider is selected —
// deliberate change from the P0 doctor (markdown users never see it),
// logged in docs/seed-manifest.md.
func providerConnections(env Env) []Connection {
	cfg := env.Cfg
	if cfg.Provider.Kind == "linear" {
		keyEnv := cfg.Linear.APIKeyEnv
		return []Connection{{
			ID:          "provider:linear-key",
			Step:        "provider",
			Kind:        KindEnv,
			Severity:    SeverityError,
			RequiredFor: []string{"grab", "ls"},
			Title:       keyEnv + " set",
			Fix:         "create a personal API key at linear.app/settings/api, export " + keyEnv + " in ~/.zshrc",
			Check:       checkEnvVar(keyEnv),
		}}
	}
	// markdown (default): each repo needs its task dir scaffolded.
	var conns []Connection
	for _, name := range repoNames(cfg) {
		r := cfg.Repos[name]
		conns = append(conns, Connection{
			ID:          "provider:markdown:" + name,
			Step:        "provider",
			Kind:        KindFile,
			Severity:    SeverityError,
			RequiredFor: []string{"grab", "ls"},
			Title:       "task dir in " + name,
			Fix:         "gv init",
			Check:       checkFile(filepath.Join(r.Path, cfg.Provider.Markdown.Dir)),
		})
	}
	return conns
}

// workerConnections probes each distinct worker-command token once —
// repos usually share one `ccwork` alias.
func workerConnections(env Env) []Connection {
	var conns []Connection
	probed := map[string]bool{}
	for _, name := range repoNames(env.Cfg) {
		r := env.Cfg.Repos[name]
		fields := strings.Fields(r.Claude)
		if len(fields) == 0 {
			continue
		}
		tok := fields[0]
		if probed[tok] {
			continue
		}
		probed[tok] = true
		conns = append(conns, Connection{
			ID:          "worker:" + tok,
			Step:        "worker",
			Kind:        KindWorkerCommand,
			Severity:    SeverityError,
			RequiredFor: []string{"grab"},
			Title:       "worker command `" + tok + "` resolves",
			Fix:         "add to ~/.zshrc: alias " + tok + "='CLAUDE_CONFIG_DIR=$HOME/.cc-work claude'",
			Check:       checkWorkerCommand(r.Claude),
		})
	}
	return conns
}

// agentsMdConnections nudges (warn) toward agent-readable context per repo.
func agentsMdConnections(env Env) []Connection {
	var conns []Connection
	for _, name := range repoNames(env.Cfg) {
		r := env.Cfg.Repos[name]
		conns = append(conns, Connection{
			ID:       "agents-md:" + name,
			Step:     "agents-md",
			Kind:     KindFile,
			Severity: SeverityWarn,
			Title:    "AGENTS.md in " + name,
			Fix:      "gv init --only agents-md",
			Check:    checkAgentContext(r.Path),
		})
	}
	return conns
}

// repoNames returns configured repo names in stable (sorted) order.
func repoNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Repos))
	for n := range cfg.Repos {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
