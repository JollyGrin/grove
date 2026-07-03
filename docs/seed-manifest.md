# Seed manifest — what was taken from overstory, and what must change

> **Source:** `overstory-tui` @ `8c2f4f059e841f063e718efd079681f92323228e`
> (`8c2f4f0 add doc for grove`), copied 2026-07-03.
> **Rule:** copied packages stay **byte-comparable with upstream** (only
> the import path `github.com/JollyGrin/overstory-tui` →
> `github.com/JollyGrin/grove` differs) until a plan task deliberately
> generalizes them. Record every deliberate divergence in the table at the
> bottom of this file. When a copied package confuses you, `diff -r`
> against upstream first.

## Copied byte-identical (imports rewritten only)

| What | Notes |
|---|---|
| `internal/tmux` · `git` · `worktree` · `detect` · `transcript` | Twice-proven (parkranger → ovs → grove). Upstream additions live in each package's separate `ovs.go` file — keep that convention (rename to `grove.go` only when generalizing). |
| `internal/state` | Event log + **mail model** (there is no separate mail package — design review I-7) + derived tasks.json. |
| `internal/cost` · `audit` · `hooks` · `tui` · `github` | Provider-neutral as-is. |
| `internal/config` · `doctor` · `kickoff` · `linear` | Copied verbatim but **destined for generalization** (see below). |
| `cmd/ovs/main.go` → `cmd/gv/main.go` | Thin glue; verb table intact. |
| `orchestrator/` (CLAUDE.md + embed.go) | The Grid-flavored brain; becomes generic-plus-pack-overlay in Phase 4. |
| `prompts/` (empty) · `config.example.yaml` · `go.mod`/`go.sum` (module renamed) · `.gitignore` | — |

**Verification at seed time:** `go build ./...`, `go vet ./...`,
`go test ./...` all green; `gofmt -l .` empty.

## NOT copied (deliberately)

- `.env` (secrets stay behind env-var indirection).
- ovs's `DESIGN.md` / `TASKS.md` / `LEARNINGS.md` / `ONBOARDING.md` /
  `docs/plans/*` — grove has its own (LEARNINGS.md seeded with the generic
  subset; the Grid-specific entries belong in the future Grid pack's L5
  layer, per the interview decisions).

## The generalization map (Phase-0+ work, under plan review)

| Surface | Today (as copied) | Target |
|---|---|---|
| **P0.0 — runtime namespaces (BLOCKS running the binary)** | config `~/.config/overstory/` · state `~/.local/state/overstory/` · env `OVERSTORY_STATE_DIR` · hook commands `ovs hook <event>` + absolute `~/go/bin/ovs` · tmux session names (`ovs`, `ovs-mobile`) · notifier titles | `~/.config/grove/` · per-workspace `.grove/state/` (DESIGN.md §6.5.1; global dir is an acceptable P0.0 interim) · `GROVE_STATE_DIR` · `gv hook` + `~/go/bin/gv` · `grove-<label>` |
| `internal/config` | Hard `linear:` struct, global single config, `ccwork` default | Layered config (connections-design §7): flags > `config.local.yaml` > `.grove/config.yaml` > pack > user > defaults; `provider:` section |
| `internal/linear` | Directly-called client | `TaskProvider` adapter behind the interface (DESIGN.md §5); `markdown` provider is new and comes first |
| `internal/doctor` | Grid checks hard-coded (ccwork plugins, universal CLAUDE.md symlink, LINEAR_API_KEY, `zsh -ic` alias probe) | Derived from the connections manifest (connections-design §3–5); Grid checks move to the Grid pack; probe `$SHELL`, not hardcoded zsh |
| `internal/kickoff` | Three Grid/Linear monolithic templates | Assembly: frame + provider verbs + pack fragments + learnings block + sentinel (connections-design §6.2). Sentinel stays core and non-negotiable |
| `orchestrator/CLAUDE.md` | Grid duties, Dean by name, dev-linear MCP | Generic duties skeleton + pack overlay, rendered composed + hash-tracked (connections-design §6.3) |
| `internal/hooks` | Installs into `~/.cc-work/settings.json`; "not my session" = untracked cwd | Same mechanism, gv-namespaced; "not my session" = **cwd not a task in this workspace's tasks.json** (dual-hook contract, DESIGN.md §12) |
| macOS-isms | `terminal-notifier` required in doctor | Desktop notification as a connection with per-OS fixes; Linux = tested tier 2 |

## Dummy-data E2E (the recipe that must not be lost)

Exercise the full grab/ls/untrack/audit/adopt loop with zero risk to any
live state: point `HOME` at a scratch dir (isolates config), set the state
env override (`OVERSTORY_STATE_DIR` today, `GROVE_STATE_DIR` after P0.0)
at a scratch dir, use a scratch git repo with a dummy config whose repo
`claude:` command is a harmless `echo`. The worker "session" echoes and
exits; every state transition, hook path, and cleanup verb is still
exercised for real. This is the Phase-0 acceptance harness (DESIGN.md
§13 Phase 0) and the pattern for all future E2E tests.

## Deliberate divergences from upstream (append-only log)

| Date | Package/file | Divergence | Plan task |
|---|---|---|---|
| 2026-07-03 | all | Module path `JollyGrin/overstory-tui` → `JollyGrin/grove`; `cmd/ovs/` → `cmd/gv/` | seed |
