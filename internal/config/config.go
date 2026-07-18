// Package config loads ~/.config/grove/config.yaml.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/JollyGrin/grove/internal/cost"
)

type Repo struct {
	Path         string   `yaml:"path"`
	Base         string   `yaml:"base"`
	Setup        string   `yaml:"setup"`
	Claude       string   `yaml:"claude"`
	Prompt       string   `yaml:"prompt"`
	Provider     string   `yaml:"provider"`      // per-repo task backend override (markdown|linear); empty = global kind
	ModelProfile string   `yaml:"model_profile"` // per-repo default model profile (grove-36); empty = anthropic
	LinearLabels []string `yaml:"linear_labels"`
}

// ModelProfile bundles what makes a non-Anthropic, Anthropic-API-compatible
// backend reachable: endpoint + credential-reference + which slug fills
// each Claude Code model class. base_url/auth_token_env travel with the
// slugs because ANTHROPIC_BASE_URL is process-global (grove-36 design §2) —
// a bare model override can't express "also point at a different backend."
type ModelProfile struct {
	BaseURL      string `yaml:"base_url"`
	AuthTokenEnv string `yaml:"auth_token_env"` // env VAR NAME holding the key, never the key itself
	Opus         string `yaml:"opus"`
	Sonnet       string `yaml:"sonnet"`
	Haiku        string `yaml:"haiku"`
	// Env carries backend-specific vars a profile needs beyond the six
	// built-ins (grove-103, e.g. Kimi Code's CLAUDE_CODE_AUTO_COMPACT_WINDOW).
	// Exported before the built-ins in WrapProfile, so a profile's env can
	// never shadow the dedicated base_url/auth_token_env/slug fields.
	Env map[string]string `yaml:"env"`
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
	Repos         map[string]*Repo         `yaml:"repos"`
	ModelProfiles map[string]*ModelProfile `yaml:"model_profiles"` // grove-36: named non-Anthropic backends; nil/absent = today's behavior
	Orchestrator  struct {
		Dir string `yaml:"dir"`
		// Hotkeys maps cockpit digit keys "1".."8" to model profile names
		// (grove-105): pressing the digit spawns that profile's orchestrator
		// directly. Bound/unbound from the `)` picker, persisted here.
		// (The old default_profile key is gone; a lingering one in yaml is
		// silently ignored like any unknown field.)
		Hotkeys map[string]string `yaml:"hotkeys"`
		Claude  string            `yaml:"claude"`
	} `yaml:"orchestrator"`
	Audit struct {
		StaleDays int    `yaml:"stale_days"` // no-PR + dead/idle tasks older than this classify abandoned (default 7)
		IdleAfter string `yaml:"idle_after"` // done/waiting workers quiet longer than this classify idle (default 30m)
	} `yaml:"audit"`
	Notify Notify `yaml:"notify"`
	Cost   struct {
		StuckTurns int                   `yaml:"stuck_turns"` // turns with no delivery movement before a stuck flag (default 30)
		Pricing    map[string]cost.Rates `yaml:"pricing"`     // per-model USD/MTok overrides (est. only)
		Record     bool                  `yaml:"record"`      // default for the spend ledger; the runtime toggle persists in <state>/cost-recording and wins
		// OpenRouter tunes the cockpit's ACCOUNT tab (grove-87). TankUSD set
		// (> 0) switches the RUNWAY gauge to flat fuel mode — bar = remaining ÷
		// tank — instead of weeks-of-burn; clearing it resumes runway mode.
		// Config-file only: the TUI never writes it.
		OpenRouter struct {
			TankUSD float64 `yaml:"tank_usd"`
		} `yaml:"openrouter"`
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
	// Layout is the pane orientation the cockpit window is built with
	// (grove-52): horizontal (default, side-by-side columns) | vertical
	// (main-vertical, dash-dominant left) | tiled. Resolved through
	// CockpitLayout, which is equally typo-forgiving.
	Cockpit struct {
		Effects string `yaml:"effects"`
		Layout  string `yaml:"layout"`
	} `yaml:"cockpit"`
}

// IdleAfter resolves audit.idle_after to a duration. Zero, unset, or
// invalid falls back to 30m (mirrors CockpitLayout's forgiveness — a typo
// in the config must never break the audit).
func (c *Config) IdleAfter() time.Duration {
	d, err := time.ParseDuration(c.Audit.IdleAfter)
	if err != nil || d <= 0 {
		return 30 * time.Minute
	}
	return d
}

// CockpitLayout resolves cockpit.layout to a valid grove layout name.
// Empty/unknown falls back to horizontal (mirrors parseFx's forgiveness —
// a typo in the config must never break the cockpit).
func (c *Config) CockpitLayout() string {
	switch c.Cockpit.Layout {
	case "vertical", "tiled":
		return c.Cockpit.Layout
	default:
		return "horizontal"
	}
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
	for name, p := range c.ModelProfiles {
		if p == nil {
			continue
		}
		for k := range p.Env {
			if !envKeyPattern.MatchString(k) {
				return nil, fmt.Errorf("model profile %q: invalid env key %q (want %s)", name, k, envKeyPattern.String())
			}
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

// SecretsPath is the model-profile secrets file the launch wrap
// self-sources (grove-36 design §2.1) — never inherited from the tmux
// server's or launching shell's environment.
func SecretsPath() string {
	return filepath.Join(Dir(), ".env")
}

// modelFlagValue extracts a --model flag's shell-quoted value from a
// WithModel'd command, e.g. `claude --model 'opus' --foo` -> "opus".
var modelFlagValue = regexp.MustCompile(`--model\s+'([^']*)'`)

// envKeyPattern is the shape a ModelProfile.Env key must match — env keys
// are interpolated unquoted into WrapProfile's shell line (they're the
// variable name, not a value), so anything outside a bare identifier is a
// config load error rather than a shell-injection surface.
var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// modelSlot classifies a (possibly WithModel'd) claude command into the
// opus/sonnet/haiku class it requested, so a profile can substitute its own
// slug for that class. No --model flag (the common case — grab/orchestrator
// launches rarely pin one) defaults to sonnet, Claude Code's own baseline
// tier.
func modelSlot(modeledCmd string) string {
	m := modelFlagValue.FindStringSubmatch(modeledCmd)
	if m == nil {
		return "sonnet"
	}
	v := strings.ToLower(m[1])
	switch {
	case strings.Contains(v, "opus"):
		return "opus"
	case strings.Contains(v, "haiku"):
		return "haiku"
	default:
		return "sonnet"
	}
}

// slugFor returns the profile's slug for a slot, falling back to Sonnet's
// when the requested slot's own field is empty (e.g. a profile that only
// bothered to set opus/sonnet).
func (p *ModelProfile) slugFor(slot string) string {
	switch slot {
	case "opus":
		if p.Opus != "" {
			return p.Opus
		}
	case "haiku":
		if p.Haiku != "" {
			return p.Haiku
		}
	}
	return p.Sonnet
}

// WrapProfile wraps an already-`WithModel`'d claude command so it runs
// against a model profile's backend instead of the operator's own Claude
// sub. p == nil returns modeledCmd unchanged — the no-profile path is
// byte-for-byte today's behavior.
//
// The wrap self-sources secretsPath (~/.config/grove/.env) rather than
// relying on inherited environment — validated by experiment that a fresh
// tmux pane does not inherit an interactive shell's export (grove-36 design
// §2.1). `export … ; exec` (never `env NAME=$VAR`) keeps the expanded
// token out of argv and out of `tmux capture-pane`; base_url and every
// slug are shell-quoted, the token stays a bare $NAME so it expands only
// from the sourced file.
//
// Sets ANTHROPIC_MODEL to the slug for the class the modeled command
// requested (opus/sonnet/haiku), plus all three ANTHROPIC_DEFAULT_*_MODEL
// vars so an in-session `/model` switch still resolves to one of the
// profile's own slugs rather than an Anthropic one.
//
// p.Env entries (grove-103) are exported first, in sorted key order for
// deterministic output, followed by the six built-ins — so a profile's env
// map can never shadow the dedicated base_url/auth_token_env/slug fields.
func WrapProfile(modeledCmd string, p *ModelProfile, secretsPath string) string {
	if p == nil {
		return modeledCmd
	}
	slug := p.slugFor(modelSlot(modeledCmd))
	var extra strings.Builder
	if len(p.Env) > 0 {
		keys := make([]string, 0, len(p.Env))
		for k := range p.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			extra.WriteString(k + "=" + shellQuote(p.Env[k]) + " ")
		}
	}
	return fmt.Sprintf(
		"( . %s && export %sANTHROPIC_BASE_URL=%s ANTHROPIC_AUTH_TOKEN=\"$%s\" ANTHROPIC_MODEL=%s "+
			"ANTHROPIC_DEFAULT_OPUS_MODEL=%s ANTHROPIC_DEFAULT_SONNET_MODEL=%s ANTHROPIC_DEFAULT_HAIKU_MODEL=%s "+
			"&& exec %s )",
		shellQuote(secretsPath), extra.String(), shellQuote(p.BaseURL), p.AuthTokenEnv, shellQuote(slug),
		shellQuote(p.Opus), shellQuote(p.Sonnet), shellQuote(p.Haiku),
		modeledCmd,
	)
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

// ResolveProfile picks the effective model profile: an explicit
// (--profile) name wins, else the repo's own `model_profile` default, else
// none. An empty name or "anthropic" both mean no profile — the operator's
// own Claude sub, unwrapped launch. r may be nil (no repo-level default to
// fall back to, e.g. an orchestrator launch).
func (c *Config) ResolveProfile(explicit string, r *Repo) (string, *ModelProfile, error) {
	name := explicit
	if name == "" && r != nil {
		name = r.ModelProfile
	}
	if name == "" || name == "anthropic" {
		return "", nil, nil
	}
	p, ok := c.ModelProfiles[name]
	if !ok {
		return "", nil, fmt.Errorf("unknown model profile %q (configured: %s)", name, strings.Join(c.profileNames(), ", "))
	}
	return name, p, nil
}

// ProfileAction is what the cockpit's `)` hotkey should do given the current
// model_profiles config — see ResolveOrchestratorProfile.
type ProfileAction int

const (
	// ProfileHint: zero profiles configured. The caller flashes a one-line
	// hint; there is nothing to spawn or pick.
	ProfileHint ProfileAction = iota
	// ProfilePick: at least one profile. The caller opens a picker over
	// candidates (sorted profile names) — `)` never auto-spawns (grove-105):
	// the choice is always shown, even for a lone profile.
	ProfilePick
)

// ResolveOrchestratorProfile decides what the `)` hotkey does (grove-41,
// grove-45, simplified in grove-105): zero profiles → ProfileHint (caller
// shows a one-line hint); otherwise ProfilePick over the sorted names.
// Direct spawns live on the digit hotkeys (Orchestrator.Hotkeys), not here.
func (c *Config) ResolveOrchestratorProfile() (candidates []string, action ProfileAction) {
	if len(c.ModelProfiles) == 0 {
		return nil, ProfileHint
	}
	return c.profileNames(), ProfilePick
}

// HotkeyFor returns the digit bound to profile in orchestrator.hotkeys,
// or "" when unbound. Linear over ≤8 entries — display-path cheap.
func (c *Config) HotkeyFor(profile string) string {
	for d, p := range c.Orchestrator.Hotkeys {
		if p == profile {
			return d
		}
	}
	return ""
}

func (c *Config) profileNames() []string {
	var names []string
	for n := range c.ModelProfiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (c *Config) repoNames() []string {
	var names []string
	for n := range c.Repos {
		names = append(names, n)
	}
	return names
}
