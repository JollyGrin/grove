# Surface plugins — the contract (v1)

Grove is driven today through one surface: a terminal. Surface plugins —
a reMarkable dashboard, a Notion board, a Discord bot — are **separate
repos, any language, distributed via plain git**, built against the
contract on this page and nothing else. There is no plugin runtime, no
ABI, no registry: grove's machine-readable output *is* the API, and the
`gv` CLI *is* the mutation interface. A plugin is a sidecar process that
reads JSON, tails a log, and shells out to `gv`.

A copyable authoring kit (this contract distilled, plus repo conventions
and polling guidance) lives in
[`.claude/skills/plugin-authoring/`](../.claude/skills/plugin-authoring/SKILL.md)
— drop it into a fresh plugin repo and start.

## What is public, what is not

**Public (stable, versioned):**

- The `--json` output of every read command (envelope + keys below).
- `events.jsonl` records (append-only log, typed vocabulary below).
- The `gv` command-line interface of the steer verbs (`answer`, `nudge`,
  `grab`).

**Not public (unstable, may change any release):**

- Human/TUI output — tables, glyphs, colors, the cockpit. Never parse it.
- `tasks.json` — a derived view grove rewrites at will; read it only as a
  convenience snapshot, never treat its shape as contractual and NEVER
  write it.
- Go packages under `internal/` (this is not a linkable library).
- tmux session/window/pane layout. Plugins never call tmux.

## Versioning: additive-only (the Terraform rule)

- Every `--json` payload carries a top-level `schema_version`; every
  events.jsonl record written since v1 carries `"v"`. Records without `v`
  (written before the stamp existed) read as v1.
- Grove only ever **adds** fields and event types. Consumers MUST ignore
  unknown keys and unknown event types — that is what makes additions
  safe.
- Removing or renaming a field, changing a field's type, or changing an
  envelope key is a **major schema bump** — the number changes and it is
  announced as a breaking release. Expect this rarely or never.
- Absent optional fields and `omitempty` zero-values are equivalent; do
  not distinguish them.

## Read: `--json` on every read command

Every payload is one JSON object: `schema_version` plus the command's
payload under one named key.

| Command | Payload key | Shape |
|---|---|---|
| `gv ls --json` | `tasks` | array — one row per active task (task fields + `live`, `pr`, `cost`, `host`); plus, after the active rows, one row per handed-off task (`done: true`, `handed_off_to: <host>`, `live: "handed-off"`) — skip them if you only want local workers |
| `gv audit --json` | `report` | object — per-task classification (incl. the report-only `handed_off` class for tombstones) + orphan worktrees + orphan/worktree processes |
| `gv sweep --json` | `report` | object — `{items, orphan_processes, worktree_processes, stale_prompts}` proposed-action dry-run |
| `gv cost --json` | `rows` | array — per-ticket token/cost estimates |
| `gv cost --ledger --json` | `rows` | array — durable per-ticket history |
| `gv cost --analyze --json` | `report` | object — outcome-priced ledger + flags |
| `gv workspaces --json` | `workspaces` | array — `{root, label, scope}` |
| `gv doctor --json` | `rows` | array — connection checks |

```sh
$ gv ls --json --no-pr --no-cost
{
  "schema_version": 1,
  "tasks": [ { "ticket": "grove-75", "agent": "working", ... } ]
}
```

Flags that matter to plugins: `gv ls --json --no-pr --no-cost` skips the
`gh` and transcript scans (fast enough to poll); with them on you also get
PR state and cost estimates per row.

Since grove-178 (the remote-overflow train) two additive row fields:

- `host` — where the row's task runs: `"local"` for this grove, a host
  name from `config.yaml`'s `hosts:` map for rows merged in by
  `gv ls --json --remote` (every configured host asked over ssh, 5s each;
  an unreachable host is one `gv ls: warning:` line on stderr and never a
  non-zero exit). Without `--remote` every live row is `"local"`.
- `handed_off_to` — the remote host's name on a **tombstone** row: a task
  `gv handoff` moved to another host (grove-177). Tombstones trail the
  live rows, have `done: true`, and `live` is `"handed-off"` (or
  `"handed-off?"` when `--remote` asked the named host and it no longer
  lists the task). With `--remote`, a tombstone whose task the host
  reports live is replaced by that live row (`host` = that host).
  Plugins that only want runnable rows: skip rows with `handed_off_to`.

## React: tail `events.jsonl`

Each workspace's `.grove/state/events.jsonl` is an append-only,
flock-guarded JSONL file — tailing it is the sanctioned subscription
mechanism (`tail -F`, or remember your byte offset and read new lines on
a timer). One record per line:

```json
{"time":"2026-07-13T10:00:00Z","type":"agent_status","ticket":"grove-75","data":{"status":"waiting","sentinel":"question","question":"Tabs or spaces?"},"v":1}
```

Task-scoped types: `task_created`, `session_started`, `agent_status`,
`notification`, `answered`, `human_status`, `session_ended`, `attached`,
`task_done`, `task_untracked`, `task_adopted`, `task_paused`,
`task_handed_off` (grove-177: data `{host, branch}` — an untrack that keeps
a forwarding pointer to the remote grove host; a later `task_untracked`
for the same ticket drops the pointer for good).
Workspace-scoped (empty `ticket`): `workspace_parked`,
`orchestrator_closed`. New types will appear over time — skip what you
don't know.

The last line may be torn mid-write; skip lines that fail to parse (grove
itself does the same).

Polling vs tailing: at e-ink/bot cadence, polling `gv ls --json` every
30–60s and tailing events.jsonl for wake-ups is sufficient. There is no
streaming API and none is planned until more than one plugin needs it.

## Steer: shell out to `gv`, never around it

Mutations go through `gv` commands **only** — they resolve the worker's
tmux pane, do bracketed-paste injection, and append the event for you:

- `gv answer <ticket> [text]` — reply to a waiting agent's question.
- `gv nudge <ticket> [text]` — unsolicited steer to a working agent.
  (Single-character text is sent as a raw key for option pickers; longer
  text is pasted with Enter.)
- `gv grab <ticket>` — start a new task from the backlog.

Never write `tasks.json` (derived), never append to `events.jsonl`
yourself (gv owns the lock and the `v` stamp), never inject tmux keys
directly (direct injection has killed real fleets — see the
tmux-discipline skill).

## Push: ntfy

Outbound notifications ride the existing channel: `notify.ntfy` in the
workspace config names an ntfy topic URL that fires on
QUESTION/BLOCKED/DONE/needs-input. A plugin that wants its own pushes can
subscribe to the same topic, or run its own — grove claims nothing here.

## Workspace enumeration: one plugin, all groves

1. `gv workspaces --json` (backed by `~/.config/grove/registry.yaml`)
   lists every registered workspace: `{root, label, scope}`.
2. Each workspace's state lives at `<root>/.grove/state/` —
   `events.jsonl` lives there; run read commands with the workspace as
   cwd (ambient walk-up scoping) so `gv ls` answers for that grove.
3. `GROVE_STATE_DIR` overrides the state path (test isolation); the
   legacy no-workspace layer is `~/.local/state/grove/`.

## Long-form findings (reading-surface convention)

Grove persists no per-ticket narrative in its own state — the richest
task-state signal is `last_message` (one turn). Reading surfaces that
want long-form content render it from where it already durably lives:

- **PR bodies and issue threads via `gh`** (`gh pr view --json body,...`,
  `gh issue view --comments`) — already external, no grove involvement.
- **`.grove/reports/<ticket>.md`** — the documented (optional) drop spot
  for long explorations: an orchestrator or kickoff prompt can instruct
  agents to write findings there, and reading surfaces render whatever
  they find. A convention, not a binary feature; absence is normal.

## Guardrails every plugin inherits

These are grove's non-negotiables (CLAUDE.md / DESIGN.md); a surface
extends them, it doesn't escape them:

- **Never mutate a task backend's terminal state** — never close or
  complete a ticket/issue/PR. Agents transition; humans finish.
- **Propose, then dispose** — anything outward-facing or irreversible
  requires explicit human confirmation *on that surface* (e.g. a
  checkbox the human ticks on the tablet is the confirmation).
- **One-way mirror** — projected grove state on your surface is
  read-only, clearly marked as projection, and reversible;
  human-authored fields stay human-owned.
- **events.jsonl via gv only; tasks.json never written** (as above).
- **Live outside the cockpit process** — plugins are sidecar processes;
  cockpit RAM is reserved for workers, and no plugin runs inside the TUI.
- **tmux only via gv** — and never attach/switch-client onto a session
  the operator's desk is attached to (tmux resizes every client to the
  smallest).

## Repo, naming, distribution

- **One repo per plugin**, named `gv-<surface>` (e.g.
  `JollyGrin/gv-remarkable`) — the gh-extensions convention. Bundling
  your own plugins in one repo is allowed; one-repo-per-plugin is the
  documented default because it keeps releases and issues with the
  author, not the grove core.
- **Distribution is git.** Install = `git clone` + run (sidecars). No
  curated index, no installer — if `gv plugin install` ever exists it
  will be a thin veneer over exactly this.
- **Discovery = GitHub topic `grove-plugin`** plus a short list in
  grove's docs. Tag your repo and it is findable.

## Proof

`e2e/plugin.sh` is the executable form of this page: an external script
that knows only the `gv` path enumerates workspaces, polls `ls --json`,
tails `events.jsonl`, and delivers a `gv nudge` — against scratch
everything (dummy-data pattern), asserting the envelope, the `v` stamp,
and that exactly one gv-appended event resulted. If the contract drifts,
it goes red.
