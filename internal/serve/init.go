package serve

import (
	"fmt"
	"strings"
)

// InitPrompt is `gv serve init`'s fixed brief (grove-381, feature trains
// Decision 7 "Drafting"): read the repos' README, package manifests and
// Makefile, write <workspace>/.grove/run.sh to the contract, stop. The
// drafted script is never trusted implicitly — the operator reviews it in
// the cockpit's `s` modal (or `gv serve`'s prompt) before it ever runs.
func InitPrompt(workspaceRoot string, repoPaths []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Draft this workspace's serve script, then stop. One file, nothing else.\n\n")
	fmt.Fprintf(&b, "Write %s — the grove serve contract (feature trains Decision 7):\n", ScriptPath(workspaceRoot))
	b.WriteString("- a POSIX sh script (#!/bin/sh, set -eu) that `gv serve <feature>` runs from a detached worktree of the feature branch;\n")
	b.WriteString("- it reads four env vars gv exports and hardcodes none of them:\n")
	b.WriteString("    GROVE_WORKTREE — the checkout to build and run from (cd into it first)\n")
	b.WriteString("    GROVE_BRANCH   — the feature branch name, for display\n")
	b.WriteString("    GROVE_PORT     — the port to listen on (a web app MUST bind this port)\n")
	b.WriteString("    GROVE_FEATURE  — the feature slug, for naming output (e.g. /tmp/<tool>-$GROVE_FEATURE)\n")
	fmt.Fprintf(&b, "- once the thing is up it prints exactly one line `%s <url-or-path>` to stdout, at the start of the line:\n", ReadyMarker)
	fmt.Fprintf(&b, "    a server prints `%s http://localhost:$GROVE_PORT` after it is listening, then stays in the foreground;\n", ReadyMarker)
	fmt.Fprintf(&b, "    a CLI or library repo does a throwaway build (e.g. `go build -o /tmp/<tool>-$GROVE_FEATURE ./cmd/<tool>`), prints `%s <that path>` and exits 0.\n", ReadyMarker)
	b.WriteString("- install dependencies it needs inside GROVE_WORKTREE only; never touch global state, never deploy, never push.\n\n")
	b.WriteString("To decide what it runs, read — do not run — the README, the package manifests (package.json, go.mod, Cargo.toml, pyproject.toml, …) and the Makefile of:\n")
	if len(repoPaths) == 0 {
		fmt.Fprintf(&b, "- %s\n", workspaceRoot)
	}
	for _, p := range repoPaths {
		fmt.Fprintf(&b, "- %s\n", p)
	}
	b.WriteString("\nIf the workspace holds several repos, pick the one whose app a reviewer would open, and say which in a comment at the top of the script.\n")
	b.WriteString("Make it executable (chmod +x). Do not run it, do not commit it, do not trust it: the operator reviews it in the cockpit (`s` on the feature) and trusts it there.\n")
	b.WriteString("When the file is written, reply with its contents and one line on how you chose, then stop.\n")
	return b.String()
}
