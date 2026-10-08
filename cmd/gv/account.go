package main

// `gv account` (claude-accounts design §Decisions 4, 6, 7): thin glue
// over internal/account. The store is GLOBAL — config.StateDir(), so
// GROVE_STATE_DIR relocates it — never a workspace's, because the shell
// function asks `gv account token-path` from whatever cwd the operator
// is in. No launch path reads it yet (that is car 03).

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

const accountUsage = "usage: gv account add <name> | ls [--json] | use <name|login> | rm <name> [--force] | token-path | shell-init"

func accountStore() account.Store { return account.Open(config.StateDir()) }

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
		jsonOut := false
		for _, a := range rest {
			if a != "--json" {
				return errors.New("usage: gv account ls [--json]")
			}
			jsonOut = true
		}
		return accountLs(s, jsonOut)
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

func accountLs(s account.Store, jsonOut bool) error {
	home, _ := os.UserHomeDir()
	rows, err := s.Rows(home, time.Now(), accountTasks())
	if err != nil {
		return err
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
		} else {
			line += fmt.Sprintf("  token %dd old", r.TokenAgeDays)
		}
		if r.Pinned > 0 {
			line += fmt.Sprintf(" · %d pinned", r.Pinned)
		}
		if !r.Connectors {
			fmt.Println(line + "  " + dim(account.ConnectorsNote))
			continue
		}
		fmt.Println(line)
	}
	return nil
}

func accountRm(s account.Store, name string, force bool) error {
	if err := account.ValidateName(name); err != nil {
		return accountUsageErr(err, "rm <name> [--force]")
	}
	if pinned := account.InUse(accountTasks(), name); len(pinned) > 0 && !force {
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
	return nil
}

// accountTasks folds every task an account pin could live in: the global
// state, the ambient workspace, and each alive registered workspace —
// read-only (state.Peek), unreadable dirs skipped. A pin is machine-wide
// because the store is.
func accountTasks() []*state.Task {
	dirs := []string{config.StateDir(), stateDir()}
	list, _ := workspace.LoadRegistry()
	for _, ws := range list {
		if !workspace.Alive(ws) {
			continue
		}
		if d := os.Getenv("GROVE_STATE_DIR"); d != "" {
			dirs = append(dirs, d)
		} else {
			dirs = append(dirs, filepath.Join(ws.Root, ".grove", "state"))
		}
	}
	seen := map[string]bool{}
	var out []*state.Task
	for _, d := range dirs {
		if d == "" || seen[d] {
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
