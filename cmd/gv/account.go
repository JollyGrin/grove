package main

// `gv account` (claude-accounts design §Decisions 4, 6, 7): thin glue
// over internal/account. The store is GLOBAL — the grove state dir, so
// GROVE_STATE_DIR relocates it — never a workspace's, because the shell
// function asks `gv account token-path` from whatever cwd the operator
// is in. main dispatches `account` before resolving the ambient
// workspace, so nothing here creates a state dir it does not write to
// (token-path, ls and shell-init are read-only). No launch path reads the
// store yet (that is car 03).

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/JollyGrin/grove/internal/account"
	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/workspace"
)

const accountUsage = "usage: gv account add <name> | ls [--usage] [--json] | use <name|login> | rm <name> [--force] | token-path | shell-init"

func accountStore() account.Store { return account.Open(config.StateDirPath()) }

// accountUsageErr renders a store usage error (bad/reserved name) with the
// verb's usage line.
func accountUsageErr(err error, verb string) error {
	if errors.Is(err, account.ErrUsage) {
		return fmt.Errorf("%w\nusage: gv account %s", err, verb)
	}
	return err
}

func cmdAccount(args []string) error {
	if len(args) == 0 {
		return errors.New(accountUsage)
	}
	verb, rest := args[0], args[1:]
	s := accountStore()
	switch verb {
	case "add":
		if len(rest) != 1 {
			return errors.New("usage: gv account add <name>   (token on stdin)")
		}
		return accountAdd(s, rest[0])
	case "ls":
		jsonOut, withUsage := false, false
		for _, a := range rest {
			switch a {
			case "--json":
				jsonOut = true
			case "--usage":
				withUsage = true
			default:
				return errors.New("usage: gv account ls [--usage] [--json]")
			}
		}
		return accountLs(s, jsonOut, withUsage)
	case "use":
		if len(rest) != 1 {
			return errors.New("usage: gv account use <name|login>")
		}
		if err := s.Use(rest[0]); err != nil {
			return accountUsageErr(err, "use <name|login>")
		}
		fmt.Printf("✓ active account: %s (new launches and shell `claude`; live sessions keep theirs)\n", rest[0])
		return nil
	case "rm":
		force := false
		var pos []string
		for _, a := range rest {
			if a == "--force" {
				force = true
				continue
			}
			pos = append(pos, a)
		}
		if len(pos) != 1 {
			return errors.New("usage: gv account rm <name> [--force]")
		}
		return accountRm(s, pos[0], force)
	case "token-path":
		if len(rest) != 0 {
			return errors.New("usage: gv account token-path")
		}
		p, err := s.ActiveTokenPath()
		if err != nil {
			return err
		}
		if p != "" {
			fmt.Println(p)
		}
		return nil
	case "shell-init":
		if len(rest) != 0 {
			return errors.New("usage: gv account shell-init")
		}
		fmt.Print(account.ShellInit)
		return nil
	}
	return errors.New(accountUsage)
}

// accountAdd reads the token from stdin only: piped as-is, or, at a
// terminal, one line with echo off so the value never lands on screen.
func accountAdd(s account.Store, name string) error {
	if err := account.ValidateName(name); err != nil {
		return accountUsageErr(err, "add <name>")
	}
	var in io.Reader = os.Stdin
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, "paste the token `claude setup-token` printed (input hidden), then Enter: ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return fmt.Errorf("reading token: %w", err)
		}
		in = bytes.NewReader(b)
	}
	replaced, err := s.Add(name, in, time.Now())
	if err != nil {
		return accountUsageErr(err, "add <name>")
	}
	verb := "added"
	if replaced {
		verb = "replaced"
	}
	p, _ := s.TokenSource(name)
	fmt.Printf("✓ account %s %s (token 0600 at %s; expires in a year)\n", name, verb, p)
	fmt.Printf("  make it the default: gv account use %s\n", name)
	fmt.Println(`  shell claude follows it once ~/.bashrc has: eval "$(gv account shell-init)"`)
	return nil
}

// accountLs is `gv account ls`; withUsage attaches each account's last
// recorded 5h/7d usage (local files only, design §Decision 8).
func accountLs(s account.Store, jsonOut, withUsage bool) error {
	home, _ := os.UserHomeDir()
	now := time.Now()
	rows, err := s.Rows(home, now, accountTasks())
	if err != nil {
		return err
	}
	if withUsage {
		for i := range rows {
			u := s.Usage(rows[i].Name, now)
			rows[i].Usage = &u
		}
	}
	if jsonOut {
		return emitJSON("accounts", rows)
	}
	width := 0
	for _, r := range rows {
		width = max(width, len(r.Name))
	}
	for _, r := range rows {
		mark := " "
		if r.Active {
			mark = "●"
		}
		line := fmt.Sprintf("%s %-*s", mark, width, r.Name)
		if r.Name == account.Login {
			email := r.Email
			if email == "" {
				email = "interactive /login"
			}
			line += "  " + email
		} else if r.Expired {
			line += fmt.Sprintf("  token EXPIRED (%dd old) — %s", r.TokenAgeDays, account.ExpiredFix(r.Name))
		} else {
			line += fmt.Sprintf("  token %dd old", r.TokenAgeDays)
		}
		if r.Pinned > 0 {
			line += fmt.Sprintf(" · %d pinned", r.Pinned)
		}
		if !r.Connectors {
			line += "  " + dim(account.ConnectorsNote)
		}
		fmt.Println(line)
		if r.Usage != nil {
			fmt.Printf("  %-*s  %s\n", width, "", account.UsageText(*r.Usage, now, time.Local))
		}
	}
	return nil
}

func accountRm(s account.Store, name string, force bool) error {
	if err := account.ValidateName(name); err != nil {
		return accountUsageErr(err, "rm <name> [--force]")
	}
	tasks := accountTasks()
	if pinned := account.InUse(tasks, name); len(pinned) > 0 && !force {
		return fmt.Errorf("account %s is pinned by non-done task(s) %s — finish them, or rm --force (shell sessions are invisible to this check)",
			name, strings.Join(pinned, ", "))
	}
	wasActive, err := s.Remove(name)
	if err != nil {
		return err
	}
	fmt.Printf("✓ account %s removed\n", name)
	if wasActive {
		fmt.Println("  it was the active account — the default is now login")
	}
	// Done, untracked and handed-off tasks don't block rm, but adopt
	// honours their pin and would refuse — name them so it isn't a surprise.
	if done := account.PinnedDone(tasks, name); len(done) > 0 {
		fmt.Printf("  still pinned by done/untracked task(s) %s — reviving one needs gv adopt <ticket> --account <name|login>\n",
			strings.Join(done, ", "))
	}
	return nil
}

// accountTasks folds every task an account pin could live in: the global
// state, the ambient workspace, and each alive registered workspace —
// read-only (state.Peek, creation-free paths), unreadable dirs skipped. A
// pin is machine-wide because the store is. Dirs are deduped after
// resolving symlinks, so a workspace reached two ways folds once.
func accountTasks() []*state.Task {
	dirs := []string{config.StateDirPath()}
	if cwd, err := os.Getwd(); err == nil {
		if ws := workspace.Find(cwd); ws != nil {
			dirs = append(dirs, workspace.StateDir(ws.Root))
		}
	}
	list, _ := workspace.LoadRegistry()
	for _, ws := range list {
		if workspace.Alive(ws) {
			dirs = append(dirs, workspace.StateDir(ws.Root))
		}
	}
	seen := map[string]bool{}
	var out []*state.Task
	for _, d := range dirs {
		if r, err := filepath.EvalSymlinks(d); err == nil {
			d = r
		}
		d = filepath.Clean(d)
		if seen[d] {
			continue
		}
		seen[d] = true
		tasks, err := state.Peek(d)
		if err != nil {
			continue
		}
		for _, t := range tasks {
			out = append(out, t)
		}
	}
	return out
}

// resolveLaunchAccount resolves a worker launch's account (design
// §Decisions 1–3) against the global store, preflighting any token before
// a side effect runs. pinned is the task's recorded account (adopt only).
func resolveLaunchAccount(ticket, flag, pinned, profileName string, cfg *config.Config, repo *config.Repo) (account.Resolved, error) {
	return accountStore().Resolve(account.Launch{
		Flag: flag, Pinned: pinned, Profile: profileName,
		ConfigDir: cfg.ClaudeConfigDir, ClaudeCmd: repo.Claude, Ticket: ticket,
	})
}

// wrapWorker applies exactly one launch wrap to a composed claude command:
// the model profile's, else the account's — never both, since each ends in
// `exec <cmd> )` (design §Decision 1). With neither, cmd is unchanged.
func wrapWorker(cmd string, profile *config.ModelProfile, acct account.Resolved) string {
	if profile != nil {
		return config.WrapProfile(cmd, profile, config.SecretsPath())
	}
	return config.WrapAccount(cmd, acct.TokenPath, acct.Name)
}

// grabClaudeCmd is grab's claude limb: the prompt as argv via command
// substitution, wrapped. Never wraps repo.Claude itself (hooks resolve the
// worker's config dir from the stored r.Claude) nor the setup prefix.
func grabClaudeCmd(claudeBin, promptPath string, profile *config.ModelProfile, acct account.Resolved) string {
	return wrapWorker(fmt.Sprintf(`%s "$(cat %q)"`, claudeBin, promptPath), profile, acct)
}

// adoptClaudeCmd is adopt's claude line: resume || fresh when a session is
// known (and not manual), else fresh. Each limb is wrapped separately: a
// wrap ends in `exec`, which replaces the shell, so one wrap around
// `resume || fresh` would make the fallback unreachable and, on a resume
// failure, silently drop to the operator's own login (grove-36 T3).
func adoptClaudeCmd(claudeBin, promptPath, sessionID string, manual bool, profile *config.ModelProfile, acct account.Resolved) string {
	freshCmd := wrapWorker(fmt.Sprintf(`%s "$(cat %q)"`, claudeBin, promptPath), profile, acct)
	if sessionID == "" || manual {
		return freshCmd
	}
	return wrapWorker(fmt.Sprintf("%s --resume %s", claudeBin, sessionID), profile, acct) + " || " + freshCmd
}

// accountLine is the grab/adopt confirmation for a launch's account; ""
// for the login.
func accountLine(acct account.Resolved) string {
	switch {
	case acct.Pin != "":
		return "→ account " + acct.Name + " (pinned to this worker)"
	case acct.Name != "":
		return "→ account " + acct.Name + " (active)"
	}
	return ""
}
