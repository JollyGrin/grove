// Package config loads ~/.config/grove/config.yaml.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/JollyGrin/grove/internal/cost"
)

type Repo struct {
	Path         string   `yaml:"path"`
	Base         string   `yaml:"base"`
	Setup        string   `yaml:"setup"`
	Claude       string   `yaml:"claude"`
	Prompt       string   `yaml:"prompt"`
	Provider     string   `yaml:"provider"` // per-repo task backend override (markdown|linear); empty = global kind
	LinearLabels []string `yaml:"linear_labels"`
}

type Config struct {
	Provider struct {
		Kind     string `yaml:"kind"` // markdown (default) | linear | github
		Markdown struct {
			Dir string `yaml:"dir"` // task-file dir relative to the repo (default .grove/tasks)
		} `yaml:"markdown"`
	} `yaml:"provider"`
	Linear struct {
		APIKeyEnv string `yaml:"api_key_env"`
		Team      string `yaml:"team"`
	} `yaml:"linear"`
	Repos        map[string]*Repo `yaml:"repos"`
	Orchestrator struct {
		Dir    string `yaml:"dir"`
		Claude string `yaml:"claude"`
	} `yaml:"orchestrator"`
	Audit struct {
		StaleDays int `yaml:"stale_days"` // no-PR + dead/idle tasks older than this classify abandoned (default 7)
	} `yaml:"audit"`
	Notify Notify `yaml:"notify"`
	Cost   struct {
		StuckTurns int                   `yaml:"stuck_turns"` // turns with no delivery movement before a stuck flag (default 30)
		Pricing    map[string]cost.Rates `yaml:"pricing"`     // per-model USD/MTok overrides (est. only)
		Record     bool                  `yaml:"record"`      // default for the spend ledger; the runtime toggle persists in <state>/cost-recording and wins
	} `yaml:"cost"`
	// Workspace is the optional identity block a per-workspace
	// <root>/.grove/config.yaml carries (DESIGN §6.5). Absent (zero) in
	// the global file and for legacy no-workspace loads.
	Workspace struct {
		Label string `yaml:"label"`
		Scope string `yaml:"scope"` // repo | parent
	} `yaml:"workspace"`
	// Cockpit tunes the dashboard's presentation (grove-22). Effects is the
	// joy knob: full (default) | calm (ambient only) | off (today's exact
	// render). Empty/unknown resolves to full in the TUI — a typo never
	// breaks the cockpit, so parse() deliberately leaves it untouched.
	Cockpit struct {
		Effects string `yaml:"effects"`
	} `yaml:"cockpit"`
}

// Notify configures phone push via ntfy. The topic URL is the only secret
// — generate a long random topic (or point at a self-hosted server).
type Notify struct {
	Ntfy     string `yaml:"ntfy"`      // full topic URL (e.g. https://ntfy.sh/<random>); empty = off
	NtfyBody string `yaml:"ntfy_body"` // full (default) | title-only — sentinel text transits the ntfy server when full
}

// NotifySettings reads only the notify section of the live config —
// see NotifySettingsFrom.
func NotifySettings() Notify {
	return NotifySettingsFrom(filepath.Join(Dir(), "config.yaml"))
}

// NotifySettingsFrom is the minimal tolerant read used by hook receivers:
// no repo-path validation, no defaults — a broken repo entry must never
// disable push, and a missing/unparseable file simply means push is off.
func NotifySettingsFrom(path string) Notify {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Notify{}
	}
	var c struct {
		Notify Notify `yaml:"notify"`
	}
	if yaml.Unmarshal(raw, &c) != nil {
		return Notify{}
	}
	return c.Notify
}

// Dir returns the grove config directory.
func Dir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "grove")
}

// StateDir returns the grove state directory, creating it if needed.
// GROVE_STATE_DIR overrides it — used to point E2E tests at a scratch
// dir so they never touch live fleet state.
func StateDir() string {
	d := os.Getenv("GROVE_STATE_DIR")
	if d == "" {
		home, _ := os.UserHomeDir()
		d = filepath.Join(home, ".local", "state", "grove")
	}
	_ = os.MkdirAll(d, 0o755)
	return d
}

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}

// Load reads and validates the global config file.
func Load() (*Config, error) {
	path := filepath.Join(Dir(), "config.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w (create %s — see config.example.yaml)", err, path)
	}
	return parse(raw, path)
}

// parse unmarshals raw config yaml, applies defaults, and validates.
// src names the source file in error messages. LoadAt runs merged bytes
// through here, so defaulting always happens AFTER any workspace merge —
// meaningful zero values set in either layer survive.
func parse(raw []byte, src string) (*Config, error) {
	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", src, err)
	}
	if c.Provider.Kind == "" {
		c.Provider.Kind = "markdown"
	}
	if c.Provider.Markdown.Dir == "" {
		c.Provider.Markdown.Dir = filepath.Join(".grove", "tasks")
	}
	if c.Linear.APIKeyEnv == "" {
		c.Linear.APIKeyEnv = "LINEAR_API_KEY"
	}
	for name, r := range c.Repos {
		if r == nil {
			return nil, fmt.Errorf("repo %q: empty config", name)
		}
		if r.Provider != "" && r.Provider != "markdown" && r.Provider != "linear" && r.Provider != "github" {
			return nil, fmt.Errorf("repo %q: provider %q (want markdown, linear, or github)", name, r.Provider)
		}
		r.Path = expand(r.Path)
		if r.Base == "" {
			r.Base = "main"
		}
		if r.Claude == "" {
			r.Claude = "claude --dangerously-skip-permissions"
		}
		if r.Prompt != "" {
			r.Prompt = expand(r.Prompt)
		}
		if st, err := os.Stat(r.Path); err != nil || !st.IsDir() {
			return nil, fmt.Errorf("repo %q: path %s is not a directory", name, r.Path)
		}
	}
	if c.Orchestrator.Dir == "" {
		c.Orchestrator.Dir = filepath.Join(Dir(), "orchestrator")
	} else {
		c.Orchestrator.Dir = expand(c.Orchestrator.Dir)
	}
	if c.Orchestrator.Claude == "" {
		// Personal claude, prompt-free: the orchestrator's guardrails are
		// CLAUDE.md-based (propose-then-dispose), not permission prompts —
		// locked interview decision (DESIGN.md §15).
		c.Orchestrator.Claude = "claude --dangerously-skip-permissions"
	}
	if c.Audit.StaleDays <= 0 {
		c.Audit.StaleDays = 7
	}
	if c.Cost.StuckTurns <= 0 {
		c.Cost.StuckTurns = 30
	}
	if len(c.Cost.Pricing) > 0 {
		cost.Overrides(c.Cost.Pricing)
	}
	return &c, nil
}

// WithModel injects a `--model <model>` flag into a claude command string,
// right after the executable token, so a single grab can pin a model without
// editing (and later reverting) a repo's `claude:` line. An empty model
// returns cmd unchanged. model is single-quoted for the shell — the command
// is run via a tmux shell, so an unquoted alias with metachars would break it.
func WithModel(cmd, model string) string {
	if strings.TrimSpace(model) == "" {
		return cmd
	}
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return cmd
	}
	head, rest := cmd, ""
	if i := strings.IndexAny(cmd, " \t"); i >= 0 {
		head, rest = cmd[:i], strings.TrimLeft(cmd[i:], " \t")
	}
	out := head + " --model " + shellQuote(model)
	if rest != "" {
		out += " " + rest
	}
	return out
}

// shellQuote single-quotes s for safe embedding in a shell command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ProviderKindFor returns the effective task backend for a repo: its own
// provider override when set, else the global kind. Lets one fleet mix
// Linear-driven repos with markdown-driven ones until per-workspace config
// (DESIGN §6.5) subsumes this.
func (c *Config) ProviderKindFor(r *Repo) string {
	if r != nil && r.Provider != "" {
		return r.Provider
	}
	return c.Provider.Kind
}

// APIKey resolves the Linear API key from the configured env var.
func (c *Config) APIKey() (string, error) {
	key := os.Getenv(c.Linear.APIKeyEnv)
	if key == "" {
		return "", fmt.Errorf("%s is not set — create a personal API key at linear.app/settings/api", c.Linear.APIKeyEnv)
	}
	return key, nil
}

// ResolveRepo picks a repo by explicit name, or by matching the ticket's
// labels/team against linear_labels. Returns an error listing candidates
// when ambiguous — the caller surfaces it with a --repo hint.
func (c *Config) ResolveRepo(explicit string, labels []string) (string, *Repo, error) {
	if explicit != "" {
		r, ok := c.Repos[explicit]
		if !ok {
			return "", nil, fmt.Errorf("unknown repo %q (configured: %s)", explicit, strings.Join(c.repoNames(), ", "))
		}
		return explicit, r, nil
	}
	var matches []string
	for name, r := range c.Repos {
		for _, rl := range r.LinearLabels {
			for _, l := range labels {
				if strings.EqualFold(rl, l) {
					matches = append(matches, name)
				}
			}
		}
	}
	if len(matches) == 1 {
		return matches[0], c.Repos[matches[0]], nil
	}
	if len(c.Repos) == 1 {
		for name, r := range c.Repos {
			return name, r, nil
		}
	}
	return "", nil, fmt.Errorf("cannot infer repo from labels %v — pass --repo <%s>", labels, strings.Join(c.repoNames(), "|"))
}

func (c *Config) repoNames() []string {
	var names []string
	for n := range c.Repos {
		names = append(names, n)
	}
	return names
}
