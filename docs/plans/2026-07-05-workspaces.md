# Workspaces — per-root state/config, registry, ambient walk-up, `gv switch`, scoped cockpits

> Status: revised per plan-reviewer round 1 (REVISE — all findings
> applied) → confirm → execute.
> Driver (Dean, three times now): "I `gv` from inside a repo but there's
> no marker anywhere that it's focusing on that repo — just generic
> grove everywhere, so I have no idea I'm *in* the project I'm working
> on." Design basis: DESIGN.md §6.5/§10/§12, grove-cockpit-design §4.6.

## Acceptance criterion (the driver, verbatim)

Running `gv` (or `gv dash`, `gv ls`) inside a workspace must make the
focus unmistakable: TUI header `❉ GROVE · <label>`, tmux session
`grove-<label>`, mutating verbs echoing `→ workspace: <label>`, and the
fleet/ACTIVITY shown being THAT workspace's state. e2e asserts the
header label (review S-4).

## Scope decisions

- **Workspace = root with `.grove/` marker.** Scopes: `repo` (single or
  monorepo root — probe's `monorepo` maps to `repo`, review I-4) and
  `parent` (folder of sibling repos: thegrid, unbrewed).
- **Layout (§6.5.1):** `<root>/.grove/config.yaml` (committable) ·
  `state/` · `orchestrator/` (seeded brain; cwd there walk-ups to this
  workspace so the orchestrator's `gv` calls hit THIS fleet) ·
  `.gitignore` seeded covering `state/`, `orchestrator/`,
  `config.local.yaml`.
- **Config merge is yaml-level, presence-aware (review I-2):** load the
  global file's yaml map, deep-merge the workspace file's map over it
  (scalars/sequences replace; maps merge; **`repos:` and `provider:`
  replace wholesale when the workspace sets them**), THEN feed the
  merged bytes through the existing `Load` pipeline (defaults run after
  the merge, so `cost_quality: 0`-class zero values survive). `linear:`
  deep-merges field-wise — a workspace sets `team: DEV`, inherits
  `api_key_env` from global. Table tests pin exactly these cases.
- **Legacy fallback:** outside any workspace, behavior is byte-for-byte
  today's (global config, global state) **until the registry is
  non-empty** — from then, bare `gv` outside a workspace opens the
  switcher (DESIGN §6.5.3), while every other verb keeps the legacy
  path. `GROVE_STATE_DIR` still overrides all state resolution.
- **Hook ownership (§12 I-6, review I-1):** the receiver scans
  candidates — registered workspaces in sorted-label order, legacy
  global LAST — using a **read-only** tasks.json membership check (new
  `state.ReadTasks`, no fold, no rewrite: hooks fire on every live ovs
  turn and must not rewrite N derived views). First fleet tracking the
  cwd owns the event; none → silent exit 0 (ovs contract intact).
- **Labels validated:** unique in registry AND not a reserved session
  name (`grove`, `mobile` — review S-1); `[a-z0-9-_]` only (tmux-safe).
- **E2E suites change (review C-2 — the old plan's "unchanged" claim
  was false):** dummy.sh/wizard.sh assertions move to the workspace
  config `gv init` now writes; the legacy no-workspace path gets its own
  explicit leg in e2e/workspace.sh. Listed as files-touched below.
- **Out:** packs, drift TTLs, auto-migration of in-flight legacy tasks
  (they finish where they're tracked; `findTask` miss prints which
  workspace tracks the id + `gv switch` hint — review I-3),
  worktree-placement changes (byte-comparable package).

## Task 1 — `internal/workspace` (new, TDD)

**Files:** `internal/workspace/{workspace.go,registry.go,rollup.go,
workspace_test.go}`.

`Workspace{Root, Label, Scope}`; `Find(cwd)` walk-up, nearest-wins;
registry Load/Add/Remove/List over `~/.config/grove/registry.yaml`
(flock; dup + reserved-label rejection); `Rollup(ws)` reads tasks.json
read-only → `{Working, Waiting, Review}`; `SortByActionability(list,
rollups)` (waiting/review float up — unit-tested, review I-6);
dead-root detection (`.grove/` gone → flagged, skipped).

**Verify:** table tests for all of the above incl. nesting +
label validation.

## Task 2 — config merge + state resolution (TDD)

**Files:** `internal/config/{config.go,merge.go,config_test.go}` (new
test file), `internal/state/grove.go` (+`ReadTasks`), tests.

`config.LoadAt(root)` (yaml-level merge per scope decision; root "" =
today's `Load`), `config.StateDirAt(root)`, `state.ReadTasks(stateDir)`
read-only. No call-site changes yet — behavior-neutral commit.

**Verify:** merge table tests (workspace-wins, zero-value survival,
linear field-wise, repos wholesale, no-workspace passthrough vs
existing tests); ReadTasks never writes (mtime assert).

## Task 3 — `resolveCtx` + call-site migration

**Files:** `cmd/gv/main.go` (every `config.Load()`/`config.StateDir()`
call site — grab/ls/adopt/done/untrack/sweep/relay/attach/diff/audit/
cost/hooks-cmd/run-setup/doctor/init), `internal/hooks` untouched here.

`resolveCtx()` → `{ws, cfg, stateDir}`; mutating verbs echo
`→ workspace: <label>` (silent when legacy); `findTask` miss scans
registered workspaces read-only and hints `tracked in workspace <X> —
gv switch <X>` (review I-3); `run-setup` gets the resolved state dir
passed explicitly via argv (its cwd is a worktree that may not walk up —
review S-3). **Exemptions (review round-2): `init` establishes the
workspace rather than resolving one (Task 6 owns it), and
`hooks install`/doctor's profile derivation UNIONS worker commands
across legacy global + every registered workspace's config — hooks
coverage is machine-wide, never ambient-scoped (round-2 residual 1).**

**Verify:** build + full unit suite; e2e legacy leg (Task 8) pins the
no-workspace path.

## Task 4 — TUI reads the resolved fleet (the visible-focus fix)

**Files:** `internal/tui/{tui.go,view.go}`, `cmd/gv/main.go`.

`tui.Run(cfg, stateDir, label)`; strip every internal
`config.StateDir()`/`config.Load()` (refreshCmd, prsCmd, ReadEvents,
answer/attach appends — review C-3); header renders
`❉ GROVE · <label>` (legacy: today's title); empty-state hint names the
workspace.

**Verify:** unit-testable render check for the header label; e2e
assertion in Task 8.

## Task 5 — hook receiver: read-only registry scan

**Files:** `internal/hooks/hooks.go` (+test), `cmd/gv/main.go` (hook
dispatch builds the candidate list).

`Receive(candidates []Candidate{Label, StateDir}, event, stdin)`:
membership via `state.ReadTasks` + `FindByCwd`; append to the owning
fleet only; sorted-workspaces-then-legacy order; none → exit 0.

**Verify:** unit test with two scratch workspaces + legacy: event lands
only in the owner; no tasks.json rewrites of non-owners (mtime).

## Task 6 — `gv init` creates the workspace (incl. parent scope)

**Files:** `cmd/gv/main.go`, `internal/bootstrap`, `internal/wizard`
(+tests), `e2e/wizard.sh` (assertion targets move — review C-2).

- **Non-repo parent branch (review C-1):** when `git.RepoRoot` fails,
  probe the cwd directly; shape `parent` (≥2 child repos) → parent
  workspace init: confirm label + detected child-repo list (each child
  becomes a repo entry in the workspace config); anything else keeps
  today's error. Inside a git repo: `single`/`monorepo` → scope `repo`.
- New leading wizard step `workspace` (label; `--label` twin; joins the
  `--only` namespace at the END of StepIDs so existing ids stay stable).
- Repo entries/provider/setup land in `<root>/.grove/config.yaml` via
  the same Doc writer; `.gitignore` + orchestrator seed + registry
  registration (idempotent).

**Verify:** wizard tests (parent flow, label validation, monorepo→repo);
init-into-parent-fixture test in bootstrap.

## Task 7 — scoped cockpits: session, spawn, add-dirs

**Files:** `cmd/gv/main.go`.

`grove-<label>` sessions; buildCockpit seeds/uses
`<root>/.grove/orchestrator/`; orchestrator `--add-dir` = this
workspace's repos only; bare `gv`: ambient → its cockpit · none+registry
→ switcher · none+no-registry → legacy cockpit; `O`/`orchestrator new`
target the ambient cockpit (§4.6 happy path), prompting via switcher
when ambient is missing.

**Verify:** e2e/workspace.sh (Task 8).

## Task 8 — switcher + registry verbs + e2e

**Files:** `cmd/gv/main.go`, `e2e/workspace.sh` (new), `e2e/dummy.sh`
(workspace-config edits — review C-2), `e2e/cockpit.sh` (session name).

- `gv switch [<label>] [--print]`: huh picker with rollups sorted by
  actionability; **non-TTY: no picker — with label acts, without label
  prints the rollup list and exits 0** (review S-2); dead roots flagged.
- `gv workspaces [--json | add <path> | rm <label>]`.
- e2e/workspace.sh (isolated tmux): two scratch workspaces (one repo-
  scope, one parent-scope with two children) + one legacy no-marker
  repo. Asserts: separate state files; `gv ls` scoped per cwd; **dash
  pane capture contains `GROVE · <label>`** (the driver, review S-4);
  `grove-<label>` sessions; hook event lands in the owning fleet only;
  legacy repo still grabs/dones on global state; switch --print; init
  re-run idempotent.

**Verify:** all four suites PASS, bare exit codes.

## Task 9 — docs + migration + gate

TASKS.md, seed-manifest rows, CLAUDE.md state-layout note, LEARNINGS.
Migration notes for Dean: drain or finish in-flight legacy tasks from
a non-workspace cwd; `gv init` at `~/git/thegrid` (parent) and — after
his sibling-restructure decision — `~/git/unbrewed`; global config
keeps user-level defaults (linear key env, notify).
**Gate:** `go build ./... && go vet ./... && go test ./...` bare,
`gofmt -l .` empty, four e2e suites bare exit 0.

## Risks / FMA

| Risk | Mitigation |
|---|---|
| Ambient resolution hits the wrong fleet (nesting) | Nearest-`.grove/`-wins + mutating verbs echo the label + tests |
| Hook events misattributed across fleets | Read-only task-membership check, deterministic order (workspaces sorted, legacy last), first-match-wins, unit-tested with two fleets; none → silent no-op (ovs contract) |
| Hook hot path rewrites every workspace's tasks.json | `state.ReadTasks` is read-only; only the owner gets an Append (review I-1) |
| Config merge drops meaningful zero values | yaml-level presence-aware merge BEFORE defaulting; `cost_quality: 0` pinned in tests (review I-2) |
| TUI keeps showing global state | Task 4 threads {cfg, stateDir, label}; e2e asserts the header label (review C-3/S-4) |
| E2E suites silently test the wrong config file | dummy/wizard assertions explicitly moved to workspace config; legacy path gets its own e2e leg (review C-2) |
| Mid-migration split-brain on daily verbs | findTask miss names the owning workspace + `gv switch` hint; migration notes say drain legacy first (review I-3) |
| Label collides with reserved sessions | Registry rejects `grove`/`mobile` + non-tmux-safe labels (review S-1) |
| Registry corruption | flock; rare human-initiated writes; last-writer-wins accepted (§6.5.1) |
| Dean's live Grid flow breaks | Legacy fallback byte-identical outside workspaces; his global config untouched until he inits roots |
