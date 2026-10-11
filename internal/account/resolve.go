package account

// The launch resolver (claude-accounts design §Decisions 1–3): which
// account a worker launch runs on, and the token path its wrap reads.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Launch is everything one launch knows that bears on its account.
type Launch struct {
	// Flag is the launch's `--account` value: "" = not given, `login` =
	// the interactive login, explicitly.
	Flag string
	// Pinned is the account recorded on the task (adopt only): set only
	// by an earlier explicit `--account`.
	Pinned string
	// Profile is the resolved model profile name; "" = Anthropic.
	Profile string
	// ConfigDir is the workspace's claude_config_dir.
	ConfigDir string
	// ClaudeCmd is the resolved claude command (the repo's `claude:`).
	ClaudeCmd string
	// Ticket names the task in a recorded pin's override hint; "" = a
	// generic hint.
	Ticket string
}

// Resolved is a launch's account. Name and TokenPath are both "" for the
// login (no env added — today's path). Pin is what the task records: an
// explicit account name, or "" (unpinned, or a pin stripped by `--account
// login`).
type Resolved struct {
	Name      string
	TokenPath string
	Pin       string
}

// SetsConfigDir reports whether a claude command runs against a config
// dir other than the personal one: it names CLAUDE_CONFIG_DIR anywhere, or
// its program — past a leading `env` and VAR=val assignments, by base name
// so a path or ~ form counts — is the `ccwork` wrapper (~/.cc-work, the
// shape hooks.SettingsPaths recognizes). Conservative: a mention is enough.
func SetsConfigDir(cmd string) bool {
	if strings.Contains(cmd, "CLAUDE_CONFIG_DIR") {
		return true
	}
	for i, f := range strings.Fields(cmd) {
		if (i == 0 && f == "env") || strings.Contains(f, "=") {
			continue
		}
		return filepath.Base(f) == "ccwork"
	}
	return false
}

// foreign names why an account may not apply to this launch (Decisions 1
// and 2), or "" when it may.
func (l Launch) foreign() string {
	switch {
	case l.Profile != "":
		return "model profile " + l.Profile + " sets its own credentials (an account only runs on Anthropic)"
	case l.ConfigDir != "":
		return "this workspace sets claude_config_dir " + l.ConfigDir + " (accounts only run on the personal ~/.claude)"
	case SetsConfigDir(l.ClaudeCmd):
		return "the claude command " + fmt.Sprintf("%q", l.ClaudeCmd) + " sets its own CLAUDE_CONFIG_DIR (accounts only run on the personal ~/.claude)"
	}
	return ""
}

// override is the hint a refused recorded pin carries: re-pin with
// another account, or strip it with login.
func (l Launch) override() string {
	t := l.Ticket
	if t == "" {
		t = "<ticket>"
	}
	return "override: gv adopt " + t + " --account <name|login> (login strips the pin)"
}

// Resolve picks a launch's account: `--account`, else the recorded pin,
// else the active account, else the login. An explicit or recorded
// account that cannot apply (Decisions 1, 2) is an error; the active one
// is skipped silently there. A pin naming a missing account is an error,
// never a fall back to login. Every token that resolves is preflighted.
func (s Store) Resolve(l Launch) (Resolved, error) {
	if l.Flag == Login {
		return Resolved{}, nil
	}
	name, how := l.Flag, "--account"
	if name == "" {
		name, how = l.Pinned, "recorded pin"
	}
	if name != "" {
		if err := ValidateName(name); err != nil {
			return Resolved{}, fmt.Errorf("%s: %w", how, err)
		}
		if why := l.foreign(); why != "" {
			hint := ""
			if how == "recorded pin" {
				hint = " — " + l.override()
			}
			return Resolved{}, fmt.Errorf("%s %s cannot apply: %s%s", how, name, why, hint)
		}
		p, err := s.Preflight(name)
		if err != nil {
			if how == "recorded pin" {
				return Resolved{}, fmt.Errorf("task is pinned to account %s: %w — %s", name, err, l.override())
			}
			return Resolved{}, err
		}
		return Resolved{Name: name, TokenPath: p, Pin: name}, nil
	}
	if l.foreign() != "" {
		return Resolved{}, nil
	}
	active, err := s.Active()
	if err != nil || active == "" {
		return Resolved{}, err
	}
	p, err := s.Preflight(active)
	if err != nil {
		return Resolved{}, fmt.Errorf("active account: %w — `gv account use login` runs the login", err)
	}
	return Resolved{Name: active, TokenPath: p}, nil
}

// Preflight is the Go-side check before any launch reads a token (design
// §Decision 5): the account exists, its token file is a non-empty 0600
// regular file, and the token is inside its one-year life. It returns the
// token path; it never reads the value.
func (s Store) Preflight(name string) (string, error) {
	p, err := s.TokenSource(name)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if fi.Size() == 0 {
		return "", fmt.Errorf("account %s's token file %s is empty — gv account add %s", name, p, name)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		return "", fmt.Errorf("account %s's token file %s is mode %#o, not 0600 — chmod 600 %s", name, p, perm, p)
	}
	if a := s.account(name); Expired(a.Minted, s.now()) {
		return "", fmt.Errorf("account %s's token EXPIRED (added %dd ago; tokens last one year) — %s",
			name, AgeDays(a.Minted, s.now()), ExpiredFix(name))
	}
	return p, nil
}
