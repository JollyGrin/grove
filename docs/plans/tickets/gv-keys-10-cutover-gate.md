# gv-keys 10: cut-over, PreToolUse guard, full e2e gate, and the PROPOSED merge to main

## Scope

Design §7 step 4, §9 risk 1, §12's final gate. The ticket that makes
the branch mergeable and then stops:

- Delete the legacy path: `secrets.Resolve` step 4, `openrouter.Key`,
  `openrouter.SaveKey`, `openrouter.Mask` (its last caller, the ACCOUNT
  footer `key …` line at account.go:575-577, switches to the sidecar
  preview or is dropped), `config.SecretsPath`, and
  `connections.envFileHasVar`. The wizard import step and `gv keys
  import` stay (the file may still exist on other hosts); doctor's
  `keys:legacy-env` row becomes "plaintext `.env` still present — import
  and remove it" with no fallback behind it.
- PreToolUse guard: `gv hooks install` (hooks merge-installer,
  `internal/hooks`) adds a `gv hook secrets-guard` PreToolUse receiver
  that returns a deny decision when a tool payload mentions
  `age/identity.txt`, `gv keys env`, or `.config/grove/secrets/`; it
  never denies `gv keys ls`, `gv keys exec` (that is the brain's rule,
  not the hook's — a chat lane that abuses it gets `exec` added here
  later). Installed with the same shared-settings respect the existing
  installer is tested for (`isGvEntry`, other entries preserved). Both
  `~/.claude/settings.json` and `~/.cc-work/settings.json` paths, as
  today.
- Run `e2e/all.sh` on the branch and paste the per-suite results into
  the PR body.
- The PR body ends with a **proposal** for the operator: the exact
  `git merge --ff-only`/PR command for `feature/gv-keys-secrets → main`,
  the rebase it needs first if main moved, and the post-merge steps
  (`gv update --yes` on both hosts, `gv keys init` + `push` to
  groveremote, `gv keys import --rm` on each). **This ticket never
  merges to main and never runs those steps.**

## Acceptance criteria

- Gate green; `grep -rn 'SecretsPath\|openrouter\.Key(\|SaveKey\|envFileHasVar' internal cmd` → no matches.
- Hook tests: deny on each of the three patterns, allow on `gv keys ls
  --json` and `gv keys exec X -- …`; installer test shows the new entry
  next to the existing ones with foreign entries preserved byte-for-byte.
- `e2e/all.sh` green, with the run's output in the PR body.
- A worker launched with a profile whose secret is missing still fails
  before `exec` with the naming error (re-run that `e2e/keys.sh` case —
  the fallback removal must not have changed the failure shape).
- TASKS.md row; LEARNINGS.md row for anything the full gate surprised.
- The PR body's last section is titled "Proposed merge to main —
  operator action" and contains no `gh pr merge` that was run.

## Dependency

Depends on gv-keys-09 merged into `feature/gv-keys-secrets` (everything
else transitively).

## Suggested model

`claude-opus-5-5` — final validation judgment across the whole train,
a hook that runs inside every Claude Code session on the host, and the
deletion of the path production currently depends on.

## Feature-train kickoff (goes verbatim in `--brief`)

FEATURE TRAIN feature/gv-keys-secrets — this ticket does NOT target main.
1. Before ANY work, in your worktree run:
     git fetch origin feature/gv-keys-secrets && git reset --hard origin/feature/gv-keys-secrets
   (safe: your branch has zero commits at this point). Build on that tip.
2. Open your PR with:
     gh pr create --base feature/gv-keys-secrets ...
   NEVER against main. If you must catch up, rebase on origin/feature/gv-keys-secrets, never on main.
3. Read docs/plans/2026-09-27-secure-keys-design.md first. This is ticket gv-keys-10 of the train; its scope is docs/plans/tickets/gv-keys-10-cutover-gate.md — build only what it lists.
4. Gate before the PR: go build ./... && go vet ./... && go test ./... green and gofmt -l . empty — run bare, never piped. Then e2e/all.sh under the tmux-discipline isolation rules, results in the PR body.
5. You PROPOSE the feature→main merge in the PR body. You never run it, never `gh pr merge`, never push to main.
