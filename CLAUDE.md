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

## ⚠️ Do not run the binary yet (pre-P0.0)

The Go tree is a **verbatim copy of ovs** (only the module path changed).
Every runtime namespace still says overstory: config at
`~/.config/overstory/`, state at `~/.local/state/overstory/` (env override
`OVERSTORY_STATE_DIR`), hooks that install `ovs hook` commands referencing
`~/go/bin/ovs`. **Running `gv` (doctor, hooks install, ui, grab) before the
P0.0 namespace rename would read/write the live overstory fleet's state.**
`go build/vet/test` are safe and expected; `go install` / executing the
binary is not, until TASKS.md P0.0 is done.

## Build / test

- `go build ./... && go vet ./... && go test ./...` must be green;
  `gofmt -l .` empty. (Verified at seed time.)
- After P0.0: `go install ./cmd/gv` refreshes `~/go/bin/gv` in place.

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
