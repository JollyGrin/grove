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

## Running the binary (P0.0 done — safe, with two cautions)

**Resolved 2026-07-04 (Phase 0):** the P0.0 namespace rename is done —
config `~/.config/grove/`, state `~/.local/state/grove/` (env override
`GROVE_STATE_DIR`; since 2026-07-05 a repo/parent with a `.grove/` marker
is a WORKSPACE — its own `.grove/{config.yaml,state,orchestrator}`,
cockpit `grove-<label>`, ambient walk-up; the global paths are the
legacy/defaults layer), `gv hook` commands, `grove`/`grove-mobile` cockpit
sessions. The binary is safe to run and no longer touches overstory
state (`e2e/dummy.sh` asserts it). One live-coexistence caution remains:
`gv hooks install` writes the **shared** `~/.cc-work/settings.json`
(tested to preserve ovs entries — but treat it with respect).

Since grove-29 (P2) a workspace's cockpit **and** its workers share one
`grove-<label>` session (window 0 = cockpit, 1+ = workers); the old
`pr-<repo>` worker sessions and their vestigial `dashboard` shell are
retired, so the ovs `pr-<repo>` coexistence constraint is gone — the
operator now runs grove exclusively.

## Build / test

- `go build ./... && go vet ./... && go test ./...` must be green;
  `gofmt -l .` empty.
- `go install ./cmd/gv` refreshes `~/go/bin/gv` in place (hooks reference
  the absolute path, so no re-install of hooks after rebuilds) — but only
  from main. For operator testing of an unmerged branch, the default is a
  throwaway build: `go build -o /tmp/gv-<ticket> ./cmd/gv`, hand over that
  path — the installed gv and live sessions stay untouched.
- `e2e/dummy.sh` runs the full grab/ls/hook/untrack/done loop against
  scratch everything (the dummy-data pattern) — run it before merging
  anything that touches the task lifecycle. `e2e/all.sh` runs all ten
  suites; no CI covers them, so run it before merging anything that
  touches the TUI as well (grove-79: three TUI PRs merged while
  cockpit.sh + workspace.sh were red).

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
