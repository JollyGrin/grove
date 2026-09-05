# grove (`gv`)

Repo-agnostic orchestrator for autonomous Claude Code sessions: one task →
git worktree + tmux window + kickoff prompt → PR, with pluggable task
backends (local markdown default; Linear/GitHub adapters), model routing,
a first-run wizard, and a layered learnings system. OSS-ready successor to
`overstory-tui` (`ovs`, `~/git/thegrid/overstory-tui`, frozen).

## Docs — what to read, what to append

- Fresh pickup: [HANDOFF.md](HANDOFF.md). Rules that were learned from
  incidents live in `.claude/skills/` — load the matching skill before
  touching tmux (tmux-discipline), the test gate/e2e/merge (shipping-
  gates), or hook/session/transcript code (claude-code-facts). When a new
  learning generalizes into a rule, update the skill too.
- **When you ship:** one row at the top of [TASKS.md](TASKS.md) §Now.
  **When surprised:** one dated entry in [LEARNINGS.md](LEARNINGS.md).
  Both files are small heads (current month); older rows/entries are in
  `docs/archive/` — grep there, don't read it in.
- Read only as the task needs: [DESIGN.md](DESIGN.md) (founding what/why),
  `docs/*-design.md` (deep designs), [docs/roadmap.md](docs/roadmap.md)
  (open phases), [docs/seed-manifest.md](docs/seed-manifest.md) (when
  touching a package copied from ovs). Plans go in `docs/plans/`.
- External surfaces (gv-<surface> repos) build against the plugin
  contract, [docs/plugins.md](docs/plugins.md) + the plugin-authoring
  skill. Any `--json` field or events.jsonl record is a contract:
  additive-only; `e2e/plugin.sh` is the tripwire.

## Layout

Config `~/.config/grove/`, state `~/.local/state/grove/` (env override
`GROVE_STATE_DIR`) are the global/defaults layer. A repo or parent dir
with a `.grove/` marker is a WORKSPACE with its own
`.grove/{config.yaml,state,orchestrator}`, found by ambient walk-up. A
workspace's cockpit and its workers share one tmux session
`grove-<label>` (window 0 = cockpit, 1+ = workers). `gv hooks install`
writes the **shared** `~/.cc-work/settings.json` — it preserves other
tools' entries, but treat it with respect.

## Build / test

- `go build ./... && go vet ./... && go test ./...` green; `gofmt -l .`
  empty. Never pipe the gate (a pipe reports the pipe's exit status).
- **Never `go install ./cmd/gv`.** The operator's `~/go/bin/gv` is
  refreshed only by `gv update --yes` (a push to main auto-cuts a release
  within about a minute); `go install` stamps the binary `dev`, which
  `gv update` then refuses. To test an UNMERGED branch, hand over a
  throwaway build: `go build -o /tmp/gv-<ticket> ./cmd/gv`.
- `e2e/dummy.sh` runs the grab/ls/hook/untrack/done loop against scratch
  everything; run it before merging anything that touches the task
  lifecycle. `e2e/all.sh` runs every suite (no CI covers them) — run it
  before merging anything that touches the TUI, tmux, or the lifecycle.

## Hard rules (provider-neutral)

- **The binary never mutates a task backend's terminal state.** Grove
  reads; agents transition; humans finish — for every provider.
- **The binary never deletes worktrees/branches it didn't create** (audit
  reports orphans; removal is the human's call).
- **`events.jsonl` is append-only** (O_APPEND + flock); `tasks.json` is a
  derived view — never writable state.
- Merge checks go through `gh` (`pr view --json state,mergedAt`), never
  git ancestry — squash-merges break ancestry.
- **Propose, then dispose** — orchestrator/autonomy never takes an
  irreversible or outward-facing action without human confirmation.
- **`ovs` is frozen.** Never edit `~/git/thegrid/overstory-tui`; note
  backports instead of applying them.

## Conventions

- Decision logic lives in tested internal packages; `cmd/gv/main.go` is
  thin glue. TDD for anything with branching logic.
- Packages copied from ovs stay byte-comparable with upstream (only the
  import path differs) until a plan task deliberately generalizes them;
  record every divergence in docs/seed-manifest.md.
- Non-trivial work: brainstorm → `docs/plans/YYYY-MM-DD-<slug>-design.md`
  (design-reviewer) → `docs/plans/YYYY-MM-DD-<slug>.md` (plan-reviewer)
  → execute on a short-lived branch in a worktree
  (`~/git/.worktrees/grove/<slug>`), commit per plan task, then a PR
  (or `git merge --ff-only`) to main. Docs go direct to main; code goes
  through a branch.
- E2E without touching anything live: the dummy-data pattern — scratch
  `HOME` + `GROVE_STATE_DIR` + repo `claude:` set to `echo`
  (docs/seed-manifest.md §Dummy-data E2E).
