# grove (`gv`)

Repo-agnostic orchestrator for autonomous Claude Code sessions: one task →
git worktree + tmux window + kickoff prompt → PR, with pluggable task
backends (local markdown default; Linear/GitHub as adapters), model routing,
a first-run wizard, and a layered learnings system. Grove is the OSS-ready
successor to `overstory-tui` (`ovs`, at `~/git/thegrid/overstory-tui`),
which stays frozen as the daily-driver reference implementation until
grove passes the parity gate.

Read [HANDOFF.md](HANDOFF.md) first if you are picking this repo up fresh.
[DESIGN.md](DESIGN.md) is the founding what/why spec; deep designs live in
`docs/` (connections/wizard, learnings, cockpit); [TASKS.md](TASKS.md) is
the status board; [LEARNINGS.md](LEARNINGS.md) holds verified surprises —
update both when you ship or get surprised. Plans go in `docs/plans/`.
The recurring traps are distilled into `.claude/skills/`
(tmux-discipline, shipping-gates, claude-code-facts) — load the matching
skill before touching tmux, the test gate/e2e, or hook/session/transcript
code. When a new learning generalizes into a rule, update the skill too;
LEARNINGS.md stays the dated log. External surfaces (gv-<surface> repos)
build against the plugin contract — [docs/plugins.md](docs/plugins.md) +
the copyable plugin-authoring skill; changing any `--json` field or
events.jsonl record is a contract change (additive-only, `e2e/plugin.sh`
is the tripwire).

## Running the binary

Config lives in `~/.config/grove/` and state in `~/.local/state/grove/`
(env override `GROVE_STATE_DIR`). A repo or parent with a `.grove/`
marker is a WORKSPACE: its own `.grove/{config.yaml,state,orchestrator}`,
cockpit session `grove-<label>`, found by walking up from the cwd; the
global paths are the defaults layer. A workspace's cockpit and its
workers share that one `grove-<label>` session (window 0 = cockpit, 1+ =
workers). The binary never touches overstory state (`e2e/dummy.sh`
asserts it). One caution: `gv hooks install` writes the **shared**
`~/.cc-work/settings.json`, which other Claude Code sessions (ovs
included) also read — it is tested to preserve their entries, but treat
it with respect.

## Feature trains

A long-lived feature branch is a first-class thing: `gv feature new
<slug> --repo R [--base main]` opens `feature/<slug>` (or `--adopt`
registers one already pushed by convention); `gv grab DEV-X --feature
<slug>` forks the worktree from it and PRs back into it instead of the
repo base — inferred automatically for a ticket carrying the feature's
label, no flag needed. `gv feature ls [--json]` shows every open train's
cars, how far behind base it is, and its own PR into base. `gv feature
land <slug> [--yes]` runs `gv done` for every car whose PR merged into
the train's branch; it never closes an issue itself — that needs an
explicit operator order, taught to the orchestrator seed as the
exception to "never close any issue" (`orchestrator/CLAUDE.md`). `gv
serve <slug>` runs the train's tip from `.grove/run.sh`, committed with
the workspace — trust is a recorded sha256 (`run_script_trusted`); a
changed script always re-prompts, never runs silently.

## Build / test

- `go build ./... && go vet ./... && go test ./...` must be green;
  `gofmt -l .` empty.
- **`gv update --yes` is the ONLY way to refresh the operator's
  `~/go/bin/gv`. Never `go install ./cmd/gv` for that.** A push to main
  auto-cuts a GitHub release within about a minute, so after a merge
  you wait, then update. `go install` stamps the binary `dev`, which is
  exactly what `gv update` refuses (`ErrDevBuild`, internal/update/update.go) —
  so each `go install` guarantees the next one refuses too. To escape a
  binary already stamped `dev`: `gv update --yes --force` once. Hooks
  reference the absolute path, so no re-install of hooks either way.
- For operator testing of an UNMERGED branch, neither applies — the
  default is a throwaway build: `go build -o /tmp/gv-<ticket> ./cmd/gv`,
  hand over that path — the installed gv and live sessions stay
  untouched.
- `e2e/dummy.sh` runs the full grab/ls/hook/untrack/done loop against
  scratch everything (the dummy-data pattern) — run it before merging
  anything that touches the task lifecycle. `e2e/all.sh` runs every
  suite; no CI covers them, so run it before merging anything that
  touches the TUI as well — a green `go test` has let TUI PRs merge
  with red e2e suites.

## Hard rules (inherited from ovs, provider-neutral)

- **The binary never mutates a task backend's terminal state.** Grove
  reads; agents transition; humans finish. Zero terminal-state mutations in
  Go, for every provider.
- **The binary never deletes worktrees/branches it didn't create** (audit
  reports orphans; removal is the human's call).
- **`events.jsonl` is append-only** (O_APPEND + flock); `tasks.json` is a
  derived view — never treat it as writable state.
- Merge checks go through `gh` (`pr view --json state,mergedAt`), never git
  ancestry — squash-merges break ancestry.
- **Propose, then dispose** — orchestrator/autonomy never takes
  irreversible or outward-facing action without human confirmation.
- **`ovs` is frozen.** Never edit `~/git/thegrid/overstory-tui` from work
  in this repo; learnings that would fix ovs get noted for deliberate
  backport, not applied.

## Conventions

- Decision logic lives in tested internal packages; `cmd/gv/main.go` is
  thin glue. TDD for anything with branching logic.
- **Packages copied from ovs stay byte-comparable with upstream** (only the
  import path differs) until a plan task deliberately generalizes them.
  Record every deliberate divergence in
  [docs/seed-manifest.md](docs/seed-manifest.md).
- Design/plan flow: brainstorm → `docs/plans/YYYY-MM-DD-<slug>-design.md`
  (design-reviewer) → `docs/plans/YYYY-MM-DD-<slug>.md` (plan-reviewer) →
  execute. Non-trivial work on a short-lived branch in a worktree
  (`~/git/.worktrees/grove/<slug>`), commit per plan task,
  `git merge --ff-only` to main, push.
- Repo shape: private `github.com/JollyGrin/grove`, direct-to-main history
  for docs; branches for code.
- E2E without touching anything live: dummy-data pattern — scratch `HOME`
  (config) + state-dir env override + repo `claude:` set to `echo`; see
  docs/seed-manifest.md §Dummy-data E2E.
