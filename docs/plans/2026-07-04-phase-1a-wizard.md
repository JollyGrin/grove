# Phase 1a+ — the visible wizard: probe · connections · doctor-from-manifest · AGENTS.md brain

> Status: revised per plan-reviewer (round 1: REVISE, all findings applied)
> → execute.
> Driver (Dean, 2026-07-04, after first live test): "the wizard is hidden —
> I should know what's available and how to further improve repo
> accessibility" + "does gv analyze stuff in a templated way to build its
> own brain?" Design basis: DESIGN.md §6 (init/probe/agent-written memory)
> and docs/grove-connections-design.md §3–5.

## Re-scoping note (plan-review I-1)

This plan is **1a plus most of 1b**: DESIGN §13 put the connections
manifest + doctor derivation in 1b, but Dean's driver — a summary board of
"what's available and what would improve this repo" — *is* the manifest
rendered, so building the wizard without it would fake the exact feature
he asked for. Pulled forward deliberately: manifest (core kinds) + doctor
rewrite. **Still 1b (remainder):** pack loading + slot merge. **Still 1c:**
TTL caches, failure-signal degradation, `mcp-auth` probing, and
**seeded-file drift** (dropped from this plan per review S-2 — detection
without `gv sync --diff` resolution is half a feature). TASKS.md is
updated in Task 7 to reflect this split.

## Scope decisions

- **New dependency:** `github.com/charmbracelet/huh` (same family as
  bubbletea/lipgloss; design-sanctioned). `go mod tidy` + vet/gofmt
  re-verified after adding.
- **Grid-interim checks** stay compiled in but as *data*: manifest
  instances in their own `grid_interim.go` file tagged `Pack:
  "grid-interim"`, rendered under a separate doctor heading — 1b's pack
  extraction lifts that one file, no re-read (review S-3).
- **Probe deferrals (deliberate):** CI/workflows + PR-template detection
  (DESIGN §6.1) — nothing in 1a consumes them.
- **Out:** per-workspace state/registry/`gv switch` (next plan; that is
  what fixes "orchestrator knows nothing about unbrewed"), packs, all of
  1c above.

## Open questions (resolved here, flagged for Dean)

- **OQ-wizard-fixes:** the wizard *runs* safe mechanical fixes itself
  (write config, install hooks, scaffold task dir) and **prints** anything
  auth/outward (gh auth login, plugin installs) for the human — the
  propose-then-dispose line (connections-design OQ2).
- **`--only` namespace (review S-1):** `--only <step>` addresses **wizard
  step ids** (`repo · setup · worker · provider · ntfy · hooks ·
  agents-md`). Doctor failures name the step when one exists, else the raw
  fix command. Unknown id → error listing valid steps.

## Task 1 — `internal/probe` (new package, TDD, pure)

**Files:** `internal/probe/probe.go`, `probe_test.go` (fixtures under
`testdata/`).

- Stack via lockfile priority chain (`pnpm-lock.yaml` > `yarn.lock` >
  `package-lock.json` > `bun.lockb`; `go.mod`; `Cargo.toml`;
  `pyproject.toml`/`uv.lock`; `Gemfile.lock`) → `Stack` + suggested
  setup/build/test/lint (package.json scripts when present; language
  conventions otherwise).
- Shape: single · monorepo (`pnpm-workspace.yaml`/`turbo.json`/`nx.json`/
  `go.work`) · parent-of-repos (≥2 child `.git`) — messaging only in 1a.
- Agent context: nearest `AGENTS.md`/`CLAUDE.md`/`.cursorrules` up-tree.
- Git: default branch (existing `git.DefaultBranch`), remote host kind.
- Backend signals: `.grove/tasks/` present; linear key env set.

**Verify:** table tests over fixture dirs (`go test ./internal/probe/`),
one fixture per stack + monorepo + parent + no-git.

## Task 2 — `internal/connections` (new package, TDD): manifest, 1a subset

**Files:** `internal/connections/{connections.go,core.go,grid_interim.go,
connections_test.go}`.

- `Connection{ID, Step string, Kind, Severity(error|warn), RequiredFor
  []string, Title, Fix string, Pack string, Check func(Env) Status}`;
  `Status{State: ok|warn|missing, Info string}`; `EvaluateAll(env)` ordered.
- Kinds now: `binary` (LookPath) · `env` · `file` · `cli-auth`
  (subprocess, on-demand, 3s timeout) · `worker-command` (shell-aware:
  `$SHELL` basename == zsh → `zsh -ic 'whence <tok>'`, else
  `command -v` via `$SHELL -ic`, 3s timeout, fallback LookPath of first
  token — review S-5) · `hooks` (via tested `hooks.installed`).
- Core instances: git/tmux/gh/claude binaries · gh auth · config present ·
  per-repo worker command · provider readiness (markdown: task dir → fix
  `gv init`; linear: key env — **only when `provider.kind == linear`**,
  divergence logged) · hooks installed · terminal-notifier (**warn**,
  darwin-only — divergence logged) · AGENTS.md present (warn, fix
  `gv init --only agents-md`).
- Grid-interim instances (`grid_interim.go`): ccwork plugins · universal
  CLAUDE.md symlink · **dev-linear MCP manual reminder** (static
  always-warn row — kept per review I-4; `mcp-auth` probing is 1c).

**Verify:** table tests per kind with fake Env (PATH/getenv/statfs
injectable); `go test ./internal/connections/`.

## Task 3 — doctor = manifest renderer

**Files:** `internal/doctor/doctor.go` (rewrite), `doctor_test.go` (new),
`cmd/gv/main.go` (cmdDoctor: arg parsing for `--json`).

Render `connections.EvaluateAll`: `✓ / ! / ✗` + copy-pastable fix,
grid-interim section, `N/M passed` + 🌳 terminal state, exit code counts
**errors only**, `--json` for the orchestrator.

**Before→after row map (review I-4)** — every current row accounted for:

| Today (doctor.go) | After |
|---|---|
| tmux/gh/git/terminal-notifier/claude installed | `binary` instances; terminal-notifier → **warn**, darwin-only |
| gh authenticated | `cli-auth` |
| config.yaml | `file`+load status |
| LINEAR_API_KEY set | `env`, **conditional on provider.kind==linear** |
| universal CLAUDE.md (grid) | grid-interim `file` |
| ccwork alias resolvable (zsh -ic) | `worker-command`, per configured repo, shell-aware |
| grid plugins in ~/.cc-work | grid-interim (reads installed_plugins.json) |
| dev-linear MCP authed (manual) | grid-interim static warn row |
| gv hooks installed | `hooks` |

**Verify:** doctor_test asserts the full row set + exit-code semantics
(error vs warn); seed-manifest divergence rows for the three behavior
changes.

## Task 4 — config field-merge writer (review I-2 — net-new, not reuse)

**Files:** `internal/bootstrap/bootstrap.go` (+`writer.go`),
`bootstrap_test.go`.

`bootstrap.ensureConfig` refuses to touch existing entries — correct for
P0, insufficient for reconfigure. Add a tested comment-preserving
yaml.Node **field-merge** writer: `SetRepoField(doc, repo, key, val)` /
`SetTopField(path...)` that updates exactly the confirmed fields of an
existing entry and leaves every other node (incl. comments, ordering,
unknown keys) byte-intact. Table test: hand-edited config with comments +
extra keys survives a partial reconfigure changing only `setup`.

## Task 5 — AGENTS.md bootstrap agent (templated brain)

**Files:** `internal/bootstrap/agentsmd.go` (+ embedded
`agentsmd_prompt.tmpl`), `agentsmd_test.go`, `cmd/gv/main.go`.

- Prompt template rendered with probe facts as ground truth + portable-
  format instructions (stack · layout · build/test/lint · test
  conventions · where interesting code lives · gotchas; ≤ ~150 lines,
  factual). Runs headless in the repo root, streams output, verifies the
  file appeared, prints "review + commit".
- **Write capability (review I-3):** the one-shot appends
  `--dangerously-skip-permissions` to the *base claude binary invocation*
  for this run only, independent of the worker's configured posture — the
  human's explicit confirmation of the wizard step (or the explicit
  `--agents-md` flag) IS the §9 consent. Never inherited as a default.
- **`--yes`/non-TTY default: OFF** (review I-3) — a paid LLM run is never
  auto-spawned; only explicit `--agents-md` enables it non-interactively.
- Honors existing context: step skipped by default when
  AGENTS.md/CLAUDE.md exists; `--force-agents-md` regenerates to
  `AGENTS.md.new` when the file exists (never overwrites).

**Verify:** unit test with a stub "claude" script (writes canned file);
template render test asserting probe facts + format rules land in the
prompt.

## Task 6 — `gv init`, the visible wizard

**Files:** `internal/wizard/{wizard.go,steps.go,wizard_test.go}`,
`cmd/gv/main.go` (cmdInit rewrite: flags + huh loop), `go.mod` (+huh).

- `internal/wizard` builds `[]Step{ID, Title, Detected, Current, Kind,
  Apply}` from probe + current config + connection results — **all
  decision logic tested here; huh rendering is a thin loop in cmd/gv.**
- Steps (ids are the `--only` namespace): `repo` (name/base) → `setup`
  (probe-suggested) → `worker` (options: ccwork-alias-if-resolvable /
  claude / custom; **skip-permissions is its own explicit confirm** with
  the DESIGN §9 note; `--yes` keeps detected/current value and enables
  autonomy only when already configured) → `provider` (markdown default;
  linear iff key present) → `ntfy` → `hooks` (offer; runs tested
  installer) → `agents-md` (offer; Task 5; default off under `--yes`).
- Ends with the **summary board**: every connection `✓/!/✗` + fix — same
  renderer as doctor. This is the "what's available / what would improve
  accessibility" screen.
- Contract: idempotent; re-run = reconfigure pre-populated with current
  values; writes only confirmed diffs via Task 4's field-merge writer;
  flag twins `--base --setup --worker --provider --ntfy --hooks/--no-hooks
  --agents-md/--no-agents-md --force-agents-md`; `--yes`; non-TTY ⇒
  `--yes`; `--only <step>`.

**Verify:** wizard_test covers step-building (fresh repo, configured repo
re-run, --only, --yes autonomy rule); manual TTY smoke.

## Task 7 — E2E + docs + gate

**Files:** `e2e/dummy.sh`, `e2e/wizard.sh` (new), `TASKS.md`,
`docs/seed-manifest.md`, `config.example.yaml` (surface check),
`LEARNINGS.md`.

- e2e/dummy.sh: bare `gv init` → `gv init --yes`.
- e2e/wizard.sh: scratch env — `gv init --yes` on a pnpm-shaped fixture
  asserts detected setup command in config; **pre-seeded hand-edited
  config with comments survives `gv init --yes` byte-comparable except
  confirmed fields** (FMA row 1 promise, now enumerated); `gv init --only
  hooks` wires scratch settings.json exactly once; `gv init --yes` does
  NOT spawn the agents-md stub; `gv init --agents-md` with stub claude
  writes AGENTS.md; doctor exit codes (all-green 0, warn-only 0,
  error 1).
- TASKS.md: 1a/1b split per the re-scoping note; seed-manifest divergence
  rows (doctor rewrite + 3 behavior changes, huh dep, bootstrap growth).
- **Pre-merge gate:** `go mod tidy && go build ./... && go vet ./... &&
  go test ./...` green, `gofmt -l .` empty, `e2e/dummy.sh` +
  `e2e/wizard.sh` + `e2e/cockpit.sh` PASS.

Ordering: 1 → 2 → 3 → 4 → 5 → 6 → 7 (bootstrap agent lands before the
wizard step that offers it — review S-4).

## Risks / FMA

| Risk | Mitigation |
|---|---|
| Wizard clobbers Dean's live hand-edited config | Task 4 field-merge writer, table-tested; only confirmed diffs written; e2e/wizard.sh asserts survival byte-comparable except confirmed fields |
| Doctor rewrite silently drops/changes a check | Task 3 before→after row map; doctor_test asserts the row set; 3 deliberate changes logged in seed-manifest |
| `--yes`/CI auto-spawns a paid LLM agent | agents-md default OFF under `--yes`/non-TTY; explicit `--agents-md` only |
| Headless bootstrap agent blocks on write permission | One-shot run appends skip-permissions itself (consent = the explicit step confirm / flag), independent of worker posture |
| Bootstrap agent overwrites human context | Skip when context exists; `--force-agents-md` → `AGENTS.md.new`; advisory prose; regenerable |
| huh unusable headless/in tests | Decisions in tested internal/wizard; huh loop thin; non-TTY ⇒ `--yes` |
| `$SHELL -ic` probe hangs | 3s timeout; shell-aware command (zsh whence / command -v); LookPath fallback |
| Worker autonomy silently enabled for strangers | Explicit toggle + safety note; `--yes` never upgrades autonomy beyond current config |
