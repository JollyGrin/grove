package main

// `gv statusline` (claude-accounts design §Decision 8, grove-472): thin
// glue over internal/statusline. Bare `gv statusline` is what Claude Code
// runs on every status line refresh, so main dispatches it before the
// ambient workspace resolves — no workspace lookup, no state dir created
// beyond the usage file it writes, and nothing it does can fail the
// status line.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/statusline"
)

const statuslineUsage = "usage: gv statusline [install | uninstall]"

func cmdStatusline(args []string) error {
	if len(args) == 0 {
		wrap, _ := config.StatuslineWrapFrom(config.PathAt(""))
		os.Exit(statusline.Run(os.Stdin, os.Stdout, os.Stderr, wrap,
			statusline.UsageDir(config.StateDirPath()), os.Getenv("GROVE_ACCOUNT"), time.Now()))
	}
	if len(args) != 1 {
		return errors.New(statuslineUsage)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	settings := statusline.SettingsPath(home)
	switch args[0] {
	case "install":
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		exe, _ = filepath.EvalSymlinks(exe)
		return statuslineInstall(settings, config.PathAt(""), exe)
	case "uninstall":
		return statuslineUninstall(settings, config.PathAt(""))
	}
	return errors.New(statuslineUsage)
}

// statuslineInstall records the current statusLine.command as
// statusline.wrap, then points statusLine.command at the shim. The wrap
// is saved first, so a failed settings write never leaves the shim
// running with nothing recorded. Re-running only refreshes the binary
// path: the recorded command is never overwritten with the shim itself.
// The shim present with nothing recorded is broken (a blank status line),
// and install says how to repair it rather than calling it installed.
func statuslineInstall(settings, cfgPath, exe string) error {
	cur, _, err := statusline.ReadCommand(settings)
	if err != nil {
		return err
	}
	shim := statusline.ShimCommand(exe)
	if statusline.IsShim(cur) {
		if _, recorded := config.StatuslineWrapFrom(cfgPath); !recorded {
			return fmt.Errorf("the status line shim is in %s but %s records no statusline.wrap, so the status line is blank\n"+
				"repair: put your own command back in statusLine.command in %s (or delete statusLine), then run: gv statusline install", settings, cfgPath, settings)
		}
		if cur != shim {
			if err := statusline.WriteCommand(settings, shim, false); err != nil {
				return err
			}
		}
		fmt.Printf("✓ status line shim already installed in %s\n", settings)
		return nil
	}
	if err := config.SaveStatuslineWrap(cfgPath, cur); err != nil {
		return err
	}
	if err := statusline.WriteCommand(settings, shim, false); err != nil {
		return err
	}
	if cur == "" {
		fmt.Printf("✓ status line shim installed in %s (no previous command; it prints nothing)\n", settings)
	} else {
		fmt.Printf("✓ status line shim installed in %s, wrapping: %s\n", settings, cur)
	}
	return nil
}

// statuslineUninstall restores the recorded command exactly — or removes
// statusLine when install found none — and forgets statusline.wrap.
func statuslineUninstall(settings, cfgPath string) error {
	cur, _, err := statusline.ReadCommand(settings)
	if err != nil {
		return err
	}
	if !statusline.IsShim(cur) {
		fmt.Printf("status line shim not installed in %s — nothing to do\n", settings)
		return nil
	}
	wrap, recorded := config.StatuslineWrapFrom(cfgPath)
	if !recorded {
		return fmt.Errorf("no statusline.wrap recorded in %s — set statusLine.command in %s by hand", cfgPath, settings)
	}
	if err := statusline.WriteCommand(settings, wrap, wrap == ""); err != nil {
		return err
	}
	if err := config.ClearStatuslineWrap(cfgPath); err != nil {
		return err
	}
	fmt.Printf("✓ status line restored in %s\n", settings)
	return nil
}
