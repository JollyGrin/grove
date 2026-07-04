// The AGENTS.md bootstrap agent (DESIGN.md §6.2 Phase B): a templated
// one-shot headless session that walks the repo and writes its portable
// brain. The probe's deterministic facts are injected as ground truth;
// the agent's prose is advisory, committed to git, human-reviewed,
// regenerable. Never overwrites existing context: with force, a repo that
// already has AGENTS.md gets AGENTS.md.new to diff by hand.
package bootstrap

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

//go:embed agentsmd_prompt.tmpl
var agentsmdTmpl string

// Facts are the probe-derived ground truths rendered into the prompt —
// a plain struct so this file doesn't couple to the probe package's API.
type Facts struct {
	RepoName string
	Stack    string
	Shape    string // single | monorepo | parent
	Setup    string
	Build    string
	Test     string
	Lint     string
}

// AgentsMDTarget decides where a generation run may write: AGENTS.md when
// absent; AGENTS.md.new when present AND force; error otherwise.
func AgentsMDTarget(repoPath string, force bool) (string, error) {
	target := filepath.Join(repoPath, "AGENTS.md")
	if _, err := os.Stat(target); err != nil {
		return target, nil
	}
	if force {
		return target + ".new", nil
	}
	return "", fmt.Errorf("AGENTS.md already exists — grove never overwrites repo context (use --force-agents-md to write AGENTS.md.new for a manual diff)")
}

// AgentsMDPrompt renders the one-shot prompt for the given target file.
func AgentsMDPrompt(f Facts, targetBase string) (string, error) {
	t, err := template.New("agentsmd").Parse(agentsmdTmpl)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, struct {
		Facts
		Target string
	}{f, targetBase}); err != nil {
		return "", err
	}
	return b.String(), nil
}

// GenerateAgentsMD runs the one-shot agent in repoPath and verifies the
// target file appeared. workerCmd is the repo's configured claude command;
// the run appends --dangerously-skip-permissions for THIS invocation only
// (the human's explicit step confirmation / --agents-md flag is the
// consent — DESIGN §9) since a headless writer can't answer permission
// prompts. Runs via $SHELL -ic so alias-based worker commands resolve.
func GenerateAgentsMD(repoPath, workerCmd string, facts Facts, force bool, out io.Writer) (string, error) {
	target, err := AgentsMDTarget(repoPath, force)
	if err != nil {
		return "", err
	}
	prompt, err := AgentsMDPrompt(facts, filepath.Base(target))
	if err != nil {
		return "", err
	}
	cmd := workerCmd
	if !strings.Contains(cmd, "--dangerously-skip-permissions") {
		cmd += " --dangerously-skip-permissions"
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	full := fmt.Sprintf("%s -p %s", cmd, shellQuote(prompt))
	c := exec.Command(shell, "-ic", full)
	c.Dir = repoPath
	c.Stdout, c.Stderr = out, out
	start := time.Now()
	if err := c.Run(); err != nil {
		return "", fmt.Errorf("bootstrap agent failed: %w", err)
	}
	if _, err := os.Stat(target); err != nil {
		return "", fmt.Errorf("agent finished (%.0fs) but %s was not written — inspect the output above", time.Since(start).Seconds(), filepath.Base(target))
	}
	return target, nil
}

// shellQuote single-quotes s for safe embedding in a shell -c string.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
