// Package doctor preflights the environment gv depends on. Exists because
// of a real incident: the ccwork profile had zero grid plugins installed,
// which would have spawned conventionless workers (LEARNINGS.md 2026-06-10).
package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/hooks"
)

type Check struct {
	Name string
	OK   bool
	Info string
	Fix  string
}

func Run(cfg *config.Config, cfgErr error) []Check {
	var checks []Check
	add := func(name string, ok bool, info, fix string) {
		checks = append(checks, Check{name, ok, info, fix})
	}
	home, _ := os.UserHomeDir()

	for _, bin := range []string{"tmux", "gh", "git", "terminal-notifier", "claude"} {
		_, err := exec.LookPath(bin)
		add(bin+" installed", err == nil, "", "brew install "+bin)
	}

	if err := exec.Command("gh", "auth", "status").Run(); err != nil {
		add("gh authenticated", false, "", "gh auth login")
	} else {
		add("gh authenticated", true, "", "")
	}

	if cfgErr != nil {
		add("config.yaml", false, cfgErr.Error(), "cp config.example.yaml ~/.config/grove/config.yaml and edit")
	} else {
		add("config.yaml", true, fmt.Sprintf("%d repo(s)", len(cfg.Repos)), "")

		keyEnv := cfg.Linear.APIKeyEnv
		add(keyEnv+" set", os.Getenv(keyEnv) != "",
			"", "create a personal API key at linear.app/settings/api, export "+keyEnv+" in ~/.zshrc")

		// Universal CLAUDE.md at the repos' parent dir (grid convention).
		seen := map[string]bool{}
		for _, r := range cfg.Repos {
			parent := filepath.Dir(r.Path)
			if seen[parent] {
				continue
			}
			seen[parent] = true
			_, err := os.Stat(filepath.Join(parent, "CLAUDE.md"))
			add("universal CLAUDE.md at "+parent, err == nil, "",
				"ln -sn <workspace>/plugins/dev-core/templates/grid-claude-md.md "+filepath.Join(parent, "CLAUDE.md"))
		}
	}

	// The worker command's first word must resolve in an interactive shell —
	// `ccwork` is usually a zsh alias, invisible to LookPath, but panes run
	// interactive shells where aliases work. Probe via `zsh -ic whence`.
	if cfgErr == nil {
		probed := map[string]bool{}
		for _, r := range cfg.Repos {
			word := strings.Fields(r.Claude)[0]
			if probed[word] {
				continue
			}
			probed[word] = true
			err := exec.Command("zsh", "-ic", "whence "+word).Run()
			add("worker command `"+word+"` resolves", err == nil, "",
				"add to ~/.zshrc: alias "+word+"='CLAUDE_CONFIG_DIR=$HOME/.cc-work claude'")
		}
	}

	// Grid plugins in the ccwork profile — workers are conventionless without them.
	pluginsOK, info := ccworkPlugins(home)
	add("grid plugins in ~/.cc-work", pluginsOK, info,
		"CLAUDE_CONFIG_DIR=~/.cc-work claude plugin marketplace add <workspace-clone> && claude plugin install dev-core@workspace ...")

	// dev-linear MCP auth can't be probed cheaply; surface as a reminder.
	add("dev-linear MCP authed (manual check)", true,
		"verify once: open a ccwork session, call a Linear tool", "")

	if installed, err := hooks.Installed(); err == nil && len(installed) == 4 {
		add("gv hooks installed", true, "", "")
	} else {
		add("gv hooks installed", false,
			fmt.Sprintf("%d/4 events wired", countTrue(installed)), "gv hooks install")
	}

	return checks
}

func ccworkPlugins(home string) (bool, string) {
	raw, err := os.ReadFile(filepath.Join(home, ".cc-work", "plugins", "installed_plugins.json"))
	if err != nil {
		return false, "no installed_plugins.json"
	}
	var data struct {
		Plugins map[string]json.RawMessage `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return false, err.Error()
	}
	need := []string{"dev-core@workspace", "dev-superpowers@workspace", "dev-linear@workspace", "dev-safety@workspace"}
	var missing []string
	for _, n := range need {
		if _, ok := data.Plugins[n]; !ok {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return false, "missing: " + strings.Join(missing, ", ")
	}
	return true, fmt.Sprintf("%d plugins", len(data.Plugins))
}

func countTrue(m map[string]bool) int {
	n := 0
	for _, v := range m {
		if v {
			n++
		}
	}
	return n
}

// Print renders checks human-readably; returns true when all pass.
func Print(checks []Check) bool {
	allOK := true
	for _, c := range checks {
		mark := "✓"
		if !c.OK {
			mark = "✗"
			allOK = false
		}
		line := fmt.Sprintf(" %s %s", mark, c.Name)
		if c.Info != "" {
			line += "  (" + c.Info + ")"
		}
		fmt.Println(line)
		if !c.OK && c.Fix != "" {
			fmt.Printf("   → %s\n", c.Fix)
		}
	}
	return allOK
}
