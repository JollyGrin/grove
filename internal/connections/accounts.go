package connections

// claude-accounts (design §Decision 7): the account rows. Conditional —
// built at manifest time only when something is wrong, so a machine with
// no accounts (or healthy ones) prints nothing new. No row probes a
// token's validity over the network: grove makes no calls to Anthropic on
// its own behalf.

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/JollyGrin/grove/internal/account"
	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/statusline"
)

// shellRCFiles are where `eval "$(gv account shell-init)"` is looked for.
var shellRCFiles = []string{".bashrc", ".bash_profile", ".profile", ".zshrc", ".zprofile"}

// accountConnections returns one row per problem: warn for a token file
// not 0600, an active record naming a missing account, accounts
// configured but no shell rc evaluating shell-init, accounts configured
// but the usage shim not in ~/.claude/settings.json, the shim installed
// with no statusline.wrap recorded (a blank status line, flagged with or
// without accounts), a token 330+ days
// old; a ✗ error row for a token past its one-year life. Empty
// AccountsStateDir (fake envs) drops the section.
func accountConnections(env Env, now time.Time) []Connection {
	if env.AccountsStateDir == "" {
		return nil
	}
	s := account.Open(env.AccountsStateDir)
	accts, _ := s.List()
	var conns []Connection
	row := func(id, title, fix, info string) {
		conns = append(conns, Connection{
			ID:          id,
			Kind:        KindFile,
			Severity:    SeverityWarn,
			RequiredFor: []string{"grab", "adopt"},
			Title:       title,
			Fix:         fix,
			Check:       func(Env) Status { return Status{State: StateWarn, Info: info} },
		})
	}
	failRow := func(id, title, fix, info string) {
		conns = append(conns, Connection{
			ID:          id,
			Kind:        KindFile,
			Severity:    SeverityError,
			RequiredFor: []string{"grab", "adopt"},
			Title:       title,
			Fix:         fix,
			Check:       func(Env) Status { return Status{State: StateMissing, Info: info} },
		})
	}
	for _, a := range accts {
		if fi, err := env.Stat(a.TokenPath); err == nil && fi.Mode().Perm() != 0o600 {
			row("account-token-mode:"+a.Name, "account "+a.Name+" token is private",
				"chmod 600 "+a.TokenPath,
				fmt.Sprintf("mode %#o — anyone with that access can spend this subscription", fi.Mode().Perm()))
		}
	}
	if active, err := s.Active(); err == nil && active != "" {
		if _, err := s.TokenSource(active); err != nil {
			row("account-active-missing", "active account exists",
				"gv account use login   # or: gv account add "+active,
				"active names "+active+", which is not configured — shell claude falls back to the login")
		}
	}
	if len(accts) > 0 && !shellInitInstalled(env) {
		row("account-shell-init", "shell claude follows the active account",
			`echo 'eval "$(gv account shell-init)"' >> `+shellRC(env),
			"accounts are configured but no shell rc evals gv account shell-init — a shell `claude` always runs the login")
	}
	if statuslineInstalled(env) && !statuslineWrapRecorded(env) {
		row("statusline-wrap-missing", "status line shim has a recorded command",
			"restore your command in ~/.claude/settings.json statusLine.command, then: gv statusline install",
			"~/.claude/settings.json runs gv statusline but no statusline.wrap is recorded in grove config — the status line is blank")
	}
	if len(accts) > 0 && !statuslineInstalled(env) {
		row("account-statusline", "status line records per-account usage",
			"gv statusline install",
			"accounts are configured but ~/.claude/settings.json's status line is not gv statusline — no 5h/weekly usage is recorded")
	}
	for _, a := range accts {
		days := account.AgeDays(a.Minted, now)
		switch {
		case account.Expired(a.Minted, now):
			failRow("account-token-expired:"+a.Name, "account "+a.Name+" token valid",
				"claude setup-token, then: gv account add "+a.Name+"   (paste the new token)",
				fmt.Sprintf("token EXPIRED — added %dd ago; setup-token tokens last one year", days))
		case days >= account.ExpiryWarnDays:
			row("account-token-expiry:"+a.Name, "account "+a.Name+" token not near expiry",
				"claude setup-token, then: gv account add "+a.Name+"   (paste the new token)",
				fmt.Sprintf("token added %dd ago — setup-token tokens expire after one year", days))
		}
	}
	return conns
}

// shellInitInstalled reports whether any of the operator's shell rc files
// mentions `gv account shell-init` on a line that is not commented out.
func shellInitInstalled(env Env) bool {
	if env.ReadFile == nil || env.Home == "" {
		return false
	}
	for _, f := range shellRCFiles {
		raw, err := env.ReadFile(filepath.Join(env.Home, f))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "#") && strings.Contains(line, account.ShellInitMarker) {
				return true
			}
		}
	}
	return false
}

// statuslineInstalled reports whether ~/.claude/settings.json runs the
// gv statusline shim. Unreadable reads as not installed.
func statuslineInstalled(env Env) bool {
	if env.ReadFile == nil || env.Home == "" {
		return false
	}
	raw, err := env.ReadFile(statusline.SettingsPath(env.Home))
	return err == nil && statusline.Installed(raw)
}

// statuslineWrapRecorded reports whether the global grove config records
// statusline.wrap (the shim's wrapped command).
func statuslineWrapRecorded(env Env) bool {
	if env.ReadFile == nil || env.Home == "" {
		return false
	}
	raw, err := env.ReadFile(filepath.Join(env.Home, ".config", "grove", "config.yaml"))
	if err != nil {
		return false
	}
	_, ok := config.StatuslineWrapIn(raw)
	return ok
}

// shellRC is the rc file the fix line names: ~/.zshrc for a zsh $SHELL,
// ~/.bashrc otherwise.
func shellRC(env Env) string {
	if env.Getenv != nil && filepath.Base(env.Getenv("SHELL")) == "zsh" {
		return "~/.zshrc"
	}
	return "~/.bashrc"
}
