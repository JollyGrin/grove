---
name: plugin-authoring
description: Use when building or reviewing a grove surface plugin (gv-<surface> repo) — a reMarkable dashboard, a Notion board, a Discord bot, any external process that reads grove state or steers agents. Contains the full contract digest, so it is self-contained — copy this directory into a fresh plugin repo and build against it alone.
---

# Authoring a grove surface plugin

A surface plugin is a **sidecar process in its own repo, any language**,
that talks to grove through three things only: `--json` output, the
`events.jsonl` log, and the `gv` CLI. There is no plugin runtime, no ABI,
no SDK. The canonical contract lives in grove's `docs/plugins.md`; this
skill is the self-contained digest — if the two ever disagree, the grove
repo wins.

## The four verbs

**Read.** Every read command emits a one-object JSON envelope:
`{"schema_version": 1, "<key>": <payload>}`.

| Command | Key | What |
|---|---|---|
| `gv ls --json [--no-pr --no-cost]` | `tasks` | active fleet, one row per task |
| `gv audit --json` | `report` | task-vs-reality classification |
| `gv sweep --json` | `report` | proposed cleanup (dry-run, `{items, stale_prompts}`) |
| `gv cost --json` / `--ledger` | `rows` | token/cost estimates / durable history |
| `gv cost --analyze --json` | `report` | outcome-priced ledger |
| `gv workspaces --json` | `workspaces` | registered groves: `{root, label, scope}` |
| `gv doctor --json` | `rows` | connection checks |

Human/TUI output is explicitly unstable — never parse it. `tasks.json` is
a derived snapshot — never contractual, NEVER written.

**React.** Tail `<workspace-root>/.grove/state/events.jsonl` — an
append-only JSONL log, one record per line:
`{"time", "type", "ticket", "data"{...}, "v"}`. Records without `v` are
v1. Task-scoped types: `task_created`, `session_started`, `agent_status`,
`notification`, `answered`, `human_status`, `session_ended`, `attached`,
`task_done`, `task_untracked`, `task_adopted`; workspace-scoped (empty
ticket): `workspace_parked`, `orchestrator_closed`. Skip unknown types
and lines that fail to parse (the last line may be torn mid-write).

**Steer.** Mutations shell out to `gv` — it resolves the tmux pane, does
safe paste injection, and appends the event for you:
- `gv answer <ticket> [text]` — reply to a waiting agent
- `gv nudge <ticket> [text]` — unsolicited steer
- `gv grab <ticket>` — start a task

Never append to events.jsonl yourself, never write tasks.json, never
call tmux (direct key injection has killed real fleets).

**Push.** Outbound = ntfy: the workspace's `notify.ntfy` topic fires on
QUESTION/BLOCKED/DONE. Subscribe to it, or run your own topic.

## Versioning: additive-only

Grove only ever **adds** fields and event types; you MUST ignore unknown
keys — that is the whole stability model (Terraform's machine-readable-UI
rule). A removal/rename/type change bumps `schema_version`/`v` and is a
breaking release. Treat absent optional fields and zero-values as
equivalent.

## Serving every grove at once

1. `gv workspaces --json` → the registry (`~/.config/grove/registry.yaml`).
2. Per workspace: state at `<root>/.grove/state/`; run read/steer
   commands with cwd inside the workspace (ambient walk-up scoping).
3. `GROVE_STATE_DIR` env overrides the state path — use it for tests.

## Polling vs tailing

At e-ink/bot cadence: poll `gv ls --json --no-pr --no-cost` every 30–60s
(cheap; add PR/cost columns only when you render them) and tail
events.jsonl (`tail -F`, or byte-offset + timer) for wake-ups. There is
no streaming API; don't build around one appearing.

## Long-form content

Grove state carries at most one turn of text (`last_message`). Render
narratives from where they durably live: PR bodies / issue threads via
`gh`, and — when the operator adopts the convention —
`.grove/reports/<ticket>.md` inside the workspace. Absence of a report
is normal, never an error.

## Guardrails (inherited, non-negotiable)

- **Never mutate a task backend's terminal state** — never close or
  complete a ticket/issue/PR. Agents transition; humans finish.
- **Propose, then dispose** — irreversible or outward-facing actions need
  explicit human confirmation *on your surface* (a tapped checkbox
  counts; silence does not).
- **One-way mirror** — projected grove state on your surface is
  read-only, clearly marked, reversible; human-authored fields stay
  human-owned.
- **Sidecar only** — run outside the cockpit process; no goroutines,
  polls, or caches inside grove.
- **Never attach/switch-client** onto a tmux session the operator's desk
  is attached to (tmux resizes every client to the smallest).

## Repo conventions

- One repo per plugin, named `gv-<surface>` (e.g. `gv-remarkable`).
- Distribution is git: install = `git clone` + run. No registry.
- Tag the repo with GitHub topic **`grove-plugin`** for discovery.
- Ship a smoke test in the dummy-data pattern: scratch `HOME`, scratch
  workspace, repo `claude:` command stubbed to `echo`, isolated tmux
  server (`unset TMUX; export TMUX_TMPDIR=<scratch>` — and never a bare
  `tmux kill-server` outside that scratch). Grove's `e2e/plugin.sh` is a
  working example to copy.
