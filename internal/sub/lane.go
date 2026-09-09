// Package sub implements `gv sub` (grove-288): a read-only micro-task
// delegated to a cheaper model_profiles lane — a raw /v1/messages call or
// an agentic `claude -p --bare` session — so the raw blob (a file, a log,
// a diff) never enters the caller's own context. Cross-lane calls are
// impossible from inside a Claude Code subagent (ANTHROPIC_BASE_URL is
// process-global), so this is a CLI a worker or orchestrator shells out
// to.
package sub

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/openrouter"
)

// Lane is a resolved model_profiles backend plus the model slug gv sub
// will call on it.
type Lane struct {
	Name    string
	BaseURL string
	// TokenEnv is the env VAR NAME holding the credential, never the
	// credential itself.
	TokenEnv string
	Model    string
	// Env carries a profile's passthrough vars (grove-103) — unused by raw
	// mode, exported into the child process by agentic mode.
	Env map[string]string
	// Paid is true for openrouter-* lanes, which bill per token even when
	// the underlying model is otherwise "free" on the operator's own
	// Claude sub — gv sub gates these behind --allow-paid.
	Paid bool
}

var (
	ErrNoLane      = errors.New("no lane configured")
	ErrUnknownLane = errors.New("unknown lane")
	ErrNoModel     = errors.New("no model resolved for lane")
	ErrPaidLane    = errors.New("lane bills per token")
	ErrNoKey       = errors.New("no key for lane")
)

// ResolveLane picks the lane by flag > GV_SUB_LANE > cfg.Sub.Lane, then
// resolves its backend and model. The special name "anthropic" is a
// pseudo-lane pointed at the operator's own Anthropic account — documented
// as unusable with the operator's OAuth session (a fresh ANTHROPIC_API_KEY
// is required).
func ResolveLane(cfg *config.Config, flagLane, flagModel string, getenv func(string) string) (Lane, error) {
	name := flagLane
	if name == "" {
		name = getenv("GV_SUB_LANE")
	}
	if name == "" && cfg != nil {
		name = cfg.Sub.Lane
	}
	if name == "" {
		return Lane{}, ErrNoLane
	}

	if name == "anthropic" {
		model := flagModel
		if model == "" && cfg != nil {
			model = cfg.Sub.Model
		}
		if model == "" {
			model = "claude-haiku-4-5"
		}
		return Lane{
			Name:     "anthropic",
			BaseURL:  "https://api.anthropic.com",
			TokenEnv: "ANTHROPIC_API_KEY",
			Model:    model,
		}, nil
	}

	if cfg == nil {
		return Lane{}, fmt.Errorf("%w: %q (configured: none)", ErrUnknownLane, name)
	}
	p, ok := cfg.ModelProfiles[name]
	if !ok || p == nil {
		return Lane{}, fmt.Errorf("%w: %q (configured: %s)", ErrUnknownLane, name, strings.Join(profileNames(cfg), ", "))
	}

	model := flagModel
	if model == "" {
		model = cfg.Sub.Model
	}
	if model == "" {
		model = p.Haiku
	}
	if model == "" {
		model = p.Sonnet
	}
	if model == "" {
		model = p.Opus
	}
	if model == "" {
		return Lane{}, fmt.Errorf("%w: lane %q has no haiku/sonnet/opus slug configured", ErrNoModel, name)
	}

	return Lane{
		Name:     name,
		BaseURL:  p.BaseURL,
		TokenEnv: p.AuthTokenEnv,
		Model:    model,
		Env:      p.Env,
		Paid:     strings.HasPrefix(name, "openrouter-"),
	}, nil
}

func profileNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.ModelProfiles))
	for n := range cfg.ModelProfiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Key resolves the lane's credential via the process env first, then the
// profile secrets file (~/.config/grove/.env).
func (l Lane) Key(secretsPath string) (string, error) {
	key := openrouter.Key(secretsPath, l.TokenEnv)
	if key == "" {
		return "", fmt.Errorf("%w: %s not set (process env or %s)", ErrNoKey, l.TokenEnv, secretsPath)
	}
	return key, nil
}
