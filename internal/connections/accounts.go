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
)

// shellRCFiles are where `eval "$(gv account shell-init)"` is looked for.
var shellRCFiles = []string{".bashrc", ".bash_profile", ".profile", ".zshrc", ".zprofile"}

// accountConnections returns one warn row per problem: a token file not
// 0600, an active record naming a missing account, accounts configured
// but no shell rc evaluating shell-init, a token 330+ days old. Empty
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
			`echo 'eval "$(gv account shell-init)"' >> ~/.bashrc`,
			"accounts are configured but no shell rc evals gv account shell-init — a shell `claude` always runs the login")
	}
	for _, a := range accts {
		if days := account.AgeDays(a.Minted, now); days >= account.ExpiryWarnDays {
			row("account-token-expiry:"+a.Name, "account "+a.Name+" token not near expiry",
				"claude setup-token, then: gv account add "+a.Name+"   (paste the new token)",
				fmt.Sprintf("token added %dd ago — setup-token tokens expire after one year", days))
		}
	}
	return conns
}

// shellInitInstalled reports whether any of the operator's shell rc files
// mentions `gv account shell-init`.
func shellInitInstalled(env Env) bool {
	if env.ReadFile == nil || env.Home == "" {
		return false
	}
	for _, f := range shellRCFiles {
		raw, err := env.ReadFile(filepath.Join(env.Home, f))
		if err == nil && strings.Contains(string(raw), account.ShellInitMarker) {
			return true
		}
	}
	return false
}
