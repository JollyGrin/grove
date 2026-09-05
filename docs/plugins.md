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
| `gv audit --json` | `report` | object — per-task classification (incl. the report-only `handed_off` class for tombstones) + orphan worktrees + orphan/worktree processes + `chat_sessions` (live detached orchestrator chats, grove-203; each row also carries `n`, `pane`, `dir` and the resolved `session_id` since grove-215) |
| `gv sweep --json` | `report` | object — `{items, orphan_processes, worktree_processes, stale_prompts}` proposed-action dry-run |
| `gv cost --json` | `rows` | array — per-ticket token/cost estimates |
| `gv cost --ledger --json` | `rows` | array — durable per-ticket history |
| `gv cost --analyze --json` | `report` | object — outcome-priced ledger + flags |
| `gv workspaces --json` | `workspaces` | array — `{root, label, scope}` |
| `gv chat ls [--workspace L] [--json]` | `chats` | array — one row per orchestrator chat, from EVERY registered workspace unless `--workspace` narrows it: `{session, workspace, n, kind, session_id, label, command, busy, attached, created, last_active, writable}` (grove-215). `kind` is `chat` (a live detached `grove-chat-<label>-<n>`), `cockpit` (the cockpit's own orchestrator pane) or `archived` (a transcript with no live pane); `session_id` is the Claude session id — minted by grove at spawn and stamped on the pane before the agent boots (grove-222), so a chat grove started carries it from second zero; it is **null** only when grove cannot know it without guessing (a pane grove did not spawn, sharing a project dir with another such pane) — a null is honest, never a placeholder to fill in from the newest transcript; `label` is the transcript's first prompt; `created` is BIRTH (a live row's tmux pane age, an archived row's transcript mtime) and `last_active` is the transcript's mtime on every kind — the last time the chat was actually spoken to, zero (`0001-01-01T00:00:00Z`) when the row has no transcript to read, where a client falls back to `created` (grove-228). **Age and order a chat list on `last_active`, not `created`** — a cockpit pane born four days ago and steered ten seconds ago is otherwise the oldest-looking row on the list. **Disable input off `writable`, never off your own reading of `kind`** — only a live `chat` row takes input |
| `gv chat tail <s> [--follow] [--since N]` | *(none — a stream)* | JSONL, one transcript entry per line: `{seq, role, kind, text, tool, ts}` (grove-216). `role` is `user`/`assistant`; `kind` is `text`, `tool_use`, `tool_result` or `thinking`; `tool` is the tool's NAME (a `tool_result` is paired back to the `tool_use` it answers); `ts` is null on a line that carries no timestamp. `seq` is 1-based over EMITTED entries and stable for an append-only transcript, so `--since N` resumes exactly where a client stopped; `--follow` streams appends (~250ms poll). Read on any kind — an archived transcript and a cockpit pane are readable, only writing is gated. Entries are never truncated: a 200KB `tool_result` arrives whole |
| `gv doctor --json` | `rows` | array — connection checks |
| `gv brains --json` | `brains` | array — one row per REGISTERED workspace (not just the ones behind): `{label, root, state, have, want, command, note}` (grove-236). `state` is `current` · `stale` · `unstamped` · `absent` · `missing-root`; `have` is the seed stamp found on disk (empty when unstamped, absent or missing-root) and `want` is the stamp of the seed the running binary embeds; `command` is the `gv init --only orchestrator-md` line to run **from `root`**, empty when there is nothing to run (current, or a root that is gone). Pure read — the sweep never writes, and grove never overwrites a brain |
| `gv watch --json` | *(none — a stream)* | one raw `events.jsonl` record per line, flushed as it lands; see React below |

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
  reports live is replaced by that live row (`host` = that host). Live
  rows are unique per ticket: a locally live task wins over a remote row
  with the same ticket (a stale answer after a take-back never shows
  twice). Plugins that only want runnable rows: skip rows with
  `handed_off_to`.

Since grove-205 one more additive row field:

- `sentinel_at` — when the `agent_status` event that set the row's CURRENT
  `sentinel` landed (RFC3339). `updated` moves for *any* event, so it
  cannot distinguish "done just now" from "done an hour ago";
  `sentinel_at` lets a **poll-based** consumer (a phone plugin, a remote
  monitor that cannot hold a `gv watch` stream) edge-detect against a
  cutoff it already has, with no baseline of its own. Present only while
  there is a sentinel to date: a plain idle stop reports `sentinel:
  "none"` — the absence of a sentinel — and omits the field, as do adopt
  and handoff, which clear the sentinel. Absent on rows from older logs.
  If you can hold a stream, prefer `gv watch`.

Since grove-191 (workspace transparency) one more additive row field:

- `workspace` — the label of the workspace that owns the task. `gv ls`
  run at the global layer (no ambient workspace) aggregates every alive
  registered workspace's rows into one fleet view; those rows carry
  `workspace: "<label>"`, while tasks living in the global layer itself
  omit the field. Inside a workspace `gv ls` stays scoped to it (no
  field, byte-identical to before). Over `--remote` a row can carry both
  `host` and `workspace` — the host says which machine, the workspace
  which grove on it owns the task.

Since grove-249 one more additive field, on `gv cost --analyze --json`'s
`report` object only:

- `unpriced_models` — array of `{model, tickets, turns}`, one row per
  model id with no pricing table entry across `report.rows`, empty (`[]`,
  never `null`) when every row is priced. The human `gv cost` and
  `gv cost --analyze` output ends with one `⚠ unpriced: <model> — N
  tickets, M turns (add cost.pricing.<model> in config.yaml)` line per
  such model, and nothing when there are none.

Since grove-251 (supervisor train 1/4) the `pr` object gains five additive
fields, and rows gain one more additive field:

- `pr.draft` — the PR's `isDraft`.
- `pr.mergeable` — gh's `mergeable`: `MERGEABLE` | `CONFLICTING` | `UNKNOWN`.
- `pr.merge_state` — gh's `mergeStateStatus`, passed through verbatim:
  `BEHIND` | `BLOCKED` | `CLEAN` | `DIRTY` | `DRAFT` | `HAS_HOOKS` |
  `UNKNOWN` | `UNSTABLE`.
- `pr.failing` — sorted names of the checks whose conclusion is `FAILURE`,
  `ERROR`, `TIMED_OUT`, `CANCELLED` or `ACTION_REQUIRED` (a CheckRun's
  `name`, or a StatusContext's `context`). Omitted when nothing is failing.
  Note the widened CI derivation this rides on: `pr.ci` now counts
  `TIMED_OUT`/`CANCELLED`/`ACTION_REQUIRED` as a failure too, so a PR whose
  only failing check was cancelled reads `ci: "fail"`, not `"pass"`.
- `pr.checks` — total `statusCheckRollup` entries.
- `pr_known` — present on a row only when a PR lookup was actually
  attempted for it (`--no-pr` and rows with no matching repo config never
  carry it); `false` means the lookup errored or timed out, so `pr: null`
  there means "lookup failed", not "no PR" — the human table renders `?`
  in the PR column for that case instead of the usual blank. Plugins that
  treat an absent `pr` as "no open PR" should check `pr_known` first.

Since grove-252 (supervisor train 2/4) two more additive row fields, and
eleven new `events.jsonl` types:

- `delivery` — `{state, pr?, url?, ci?, failing?, merge_state?, at}`, the
  transition engine's folded PR-facing state. `state` is
  `none|opened|ci_failed|conflicting|ready|merged|closed`. Absent (not
  present in the payload) means `none` — a task with no PR yet — exactly
  like `sentinel_at`'s absent-means-no-sentinel convention.
- `liveness` — `{state, reason?, at}`, folded worker liveness beyond what
  the Stop hook can see. `state` is `ok|waiting|vanished|errored`. Absent
  means `ok`.

`internal/supervise` derives both from `github.PR` (the #251 fields) and a
tmux pane read, and emits one event per **state change** (never per
observation — re-observing the same state emits nothing, so two pollers or
a restart never double-fire). The events:

| type | data | fires when |
|---|---|---|
| `pr_opened` | `pr, url, draft` | a PR appears, or a closed PR reopens |
| `pr_updated` | `pr` | re-entering `opened` from any other non-none state (a fresh push put checks back to pending) |
| `pr_ci_failed` | `pr, failing` (comma-joined check names) | CI goes red |
| `pr_conflicting` | `pr, merge_state` | the PR is open and `mergeable: CONFLICTING` or `mergeStateStatus: DIRTY` |
| `pr_ready` | `pr, url, merge_state, behind?` (`"true"` when `merge_state: BEHIND`) | open, not draft, CI green, not conflicting — `BLOCKED`/`BEHIND` still count as ready |
| `pr_merged` | `pr` | the PR merges |
| `pr_closed` | `pr` | the PR closes unmerged |
| `worker_waiting` | `marker` | the pane has shown a question/menu/permission-prompt marker continuously for ≥10s (debounces the busy/idle flap) |
| `worker_vanished` | *(none)* | the window exists, claude is gone from the pane, continuously for ≥60s, **and** ≥120s since the task's last session start/adopt (a boot grace — the pane legitimately shows a shell while claude boots) |
| `worker_errored` | `reason, line` (`reason` one of `usage_limit`/`sleep`/`api_error`; `line` the matched pane line, ≤200 runes) | a usage-limit/429, a sleep-cut, or another `API Error:` line appears in the pane — immediate, no debounce |
| `worker_recovered` | `from` (the prior liveness state) | liveness returns to `ok` from anything else |

`gv supervise` (grove-253) is the poller that emits them — see the next
section.

## React: `gv watch`, or tail `events.jsonl`

`gv supervise [--interval 30s] [--once] [--json]` is what PRODUCES the
delivery/liveness stream above: a headless loop (one per workspace, guarded
by a `<state>/supervise.lock` single-emitter flock — a second `gv
supervise`, or the cockpit once it drives this itself, exits 1 naming the
pid already emitting) that reads one `tmux.SnapshotSession` per session,
one `github.FetchAll` round-trip, and one `internal/detect.DetectLiveFrom`
per task, feeds them through `internal/supervise.Transitions`, and appends
whatever fired. `--once` runs a single pass then exits 0 — hysteresis
(the 10s waiting-debounce, 60s vanished-debounce + 120s boot grace) lives
in-process, so a single pass can still emit delivery and `worker_errored`
transitions, but never `worker_waiting`/`worker_vanished` (those need a
continuously running loop to accumulate the debounce window). Every
emitted event is printed the same way `gv watch` would show it (or the raw
record with `--json`), and pushed to ntfy/desktop per the table below — so
running `gv supervise` on a headless host (no desk cockpit open) is the
whole "produce" half; `gv watch` below is the "consume" half.

| event | ntfy priority · tag | desktop |
|---|---|---|
| `worker_waiting` / `worker_vanished` / `worker_errored` | high · `warning` | yes |
| `pr_ci_failed` / `pr_conflicting` | high · `x` | yes |
| `pr_ready` | default · `white_check_mark` | yes |
| `pr_merged` | default · `tada` | yes |
| `pr_opened` / `pr_updated` / `pr_closed` / `worker_recovered` | none | no |

`gv watch` (grove-205) is the supported subscription: grove does the
tailing, the offset bookkeeping and the torn-line handling, and hands you
one event per stdout line.

```
gv watch [--json] [--ticket X]... [--type agent_status,notification,…]
         [--sentinel done,question,blocked] [--since <RFC3339> | --replay]
         [--until done|pr_merged|worker_waiting|…]
```

- **Pure read**, workspace-scoped: it follows the ambient workspace's log,
  resolved exactly as `gv ls` resolves it. Run it from inside the
  workspace (at the global layer it says so on stderr and names the
  workspaces).
- **From-now by default** — only events appended after the process
  started. `--since <RFC3339>` resumes from a cutoff, `--replay` includes
  the whole history. This is what makes a "before" snapshot impossible to
  sample late; do not rebuild one yourself.
- **`--until <sentinel or event type>` exits 0 exactly when that transition
  lands** — a sentinel (`question|blocked|done|none`) as before, or
  (grove-252) a bare event type: `--until pr_merged`, `--until
  worker_waiting`. One notification, no polling arithmetic. A non-zero
  exit means the wait ended some other way — exit 0 always means the
  transition happened.
- **Default type set** — the terminal/actionable states, so a crashed or
  wedged worker is never silent: `agent_status` (every sentinel,
  *including* an idle stop with no STATUS line), `notification`,
  `session_ended`, `task_done`, `task_untracked`, `task_paused`, and
  (grove-252) all eleven delivery/liveness types — every one is
  actionable or terminal. `--type` takes an explicit list (or `all`); an
  unknown type or sentinel is an error, never an empty stream.
- **Line-flushed** — os.Stdout is unbuffered, so each event is on the pipe
  as it lands (`gv watch | cat` shows it immediately). This is the
  contract a per-line notification consumer needs.
- `--json` emits the **raw record, byte-for-byte**, so a field a newer
  grove adds passes straight through. The default is a human row:
  `HH:MM  ticket  done  <first line of the message>`.

**Never derive a task's completion from its tmux pane.** The kickoff
prompt ends with all three `STATUS: QUESTION|BLOCKED|DONE — …` lines
verbatim, so every worker's pane contains all three sentinels from second
zero; a pane grep fires instantly, on every task, forever (it produced two
false DONEs in one minute on 2026-08-29 — grove-205). What `gv watch`
streams is the Stop hook's classification of the agent's OWN last message,
which never sees the prompt.

If you would rather do it yourself: each workspace's
`.grove/state/events.jsonl` is an append-only, flock-guarded JSONL file,
and tailing it directly stays supported (`tail -F`, or remember your byte
offset and read new lines on a timer). One record per line:

```json
{"time":"2026-07-13T10:00:00Z","type":"agent_status","ticket":"grove-75","data":{"status":"waiting","sentinel":"question","question":"Tabs or spaces?"},"v":1}
```

Task-scoped types: `task_created`, `session_started`, `agent_status`,
`notification`, `answered`, `human_status`, `session_ended`, `attached`,
`task_done`, `task_untracked`, `task_adopted`, `task_paused`,
`task_handed_off` (grove-177: data `{host, branch}` — an untrack that keeps
a forwarding pointer to the remote grove host; a later `task_untracked`
for the same ticket drops the pointer for good), and (grove-252) the
eleven delivery/liveness types in the table above: `pr_opened`,
`pr_updated`, `pr_ci_failed`, `pr_conflicting`, `pr_ready`, `pr_merged`,
`pr_closed`, `worker_waiting`, `worker_vanished`, `worker_errored`,
`worker_recovered`. `answered` may carry an
optional `data.op_id` (grove-186, additive): relayed `answer`/`nudge`
hops (`--host`) stamp a client op id so a retried hop is a no-op on the
remote — same id seen again ⇒ nothing pasted, no second event. Local
relays and the cockpit keep appending `answered` with no `data`, exactly
as before. `agent_status`, `notification` and `session_ended` carry an
optional `data.session_id` (grove-250, additive): the Claude session id
of the process that fired the hook — always the task's recorded worker
now that the receiver drops these three events when the id at the
worktree's cwd is NOT the recorded one (an orchestrator whose shell
`cd`'d into a worker's worktree used to overwrite that worker's status).
`session_started` keeps registering whatever id arrives, so an adopt's
fresh pickup session still takes over. Records written before grove-250
have no `session_id`; treat a missing one as unknown, never as foreign.
Workspace-scoped (empty `ticket`): `workspace_parked`,
`orchestrator_closed`, `orchestrator_spawned` (grove-198, additive: data
`{workspace, session, profile?, op_id?, resume?, brief?}` — a detached
orchestrator chat started for a workspace by `gv orchestrator new
--workspace <label>`, the receiving half of `--host`; `session` is its
`grove-chat-<label>-<n>` tmux session and `op_id` the relayed hop's
receipt, so a retried hop reprints the first spawn instead of making a
second one. `resume` (grove-217, additive) carries the Claude session id
when the spawn REVIVED an archived chat rather than starting a fresh one
— that id is stamped on the new pane, so `gv chat ls` reports the revived
chat under the same `session_id` it had while `kind: archived`. `brief`
(grove-271, additive) is the path of the standing brief the chat was
seeded with — `<orchDir>/briefs/<session-id>.md`, the text handed to the
agent as its first message; absent when the spawn carried none). New types will appear
over time — skip what you don't know.

The last line may be torn mid-write; skip lines that fail to parse (grove
itself does the same).

Polling vs streaming: at e-ink/bot cadence, polling `gv ls --json` every
30–60s and using `gv watch` (or your own tail) for wake-ups is sufficient.
`gv watch` is the whole streaming surface — a line-oriented subprocess, not
a socket or an ABI; a consumer that cannot hold a long-lived process polls
`gv ls --json` and edge-detects on `sentinel_at`.

## Steer: shell out to `gv`, never around it

Mutations go through `gv` commands **only** — they resolve the worker's
tmux pane, do bracketed-paste injection, and append the event for you:

- `gv answer <ticket> [text]` — reply to a waiting agent's question.
- `gv nudge <ticket> [text]` — unsolicited steer to a working agent.
  (Single-character text is sent as a raw key for option pickers; longer
  text is pasted with Enter.)
- `gv grab <ticket>` — start a new task from the backlog.
- `gv chat send <session> "<text>"` — relay prose into a live orchestrator
  chat. Refuses any row whose `writable` is false (`kind: cockpit` or
  `archived`) with a reason and the verb to use instead, and exits non-zero
  if the text was delivered but never SUBMITTED — delivered is not
  submitted (grove-144/216). `<session>` is a tmux session name, a Claude
  session id, or 4+ characters of one; an ambiguous prefix is refused, never
  picked.
- `gv chat keys <session> <chars>` — one raw keystroke, no Enter, for the
  permission prompts and option pickers a prose box cannot drive. Same
  `writable` gate; a newline is refused (that is `send`'s job).
- `gv chat restamp <session> [<session-id>]` — operator escape hatch
  (grove-222): re-point a live chat's identity stamp, or clear it (no id)
  so the next `gv chat ls` re-derives one. For the two cases nothing can
  re-derive — a pane mis-stamped before grove minted ids, and a
  conversation replaced inside a living pane. A pane whose agent carries an
  explicit `--session-id`/`--resume` is corrected back from that argv on
  the next report: the running process outranks a typed-in answer.

Never write `tasks.json` (derived), never append to `events.jsonl`
yourself (gv owns the lock and the `v` stamp), never inject tmux keys
directly (direct injection has killed real fleets — see the
tmux-discipline skill).

## Push: ntfy

Outbound notifications ride the existing channel: `notify.ntfy` in the
workspace config names an ntfy topic URL that fires on
QUESTION/BLOCKED/DONE/needs-input, and (grove-253) `gv supervise` pushes
the eleven delivery/liveness types on the same topic per the table in the
React section above. A plugin that wants its own pushes can subscribe to
the same topic, or run its own — grove claims nothing here.

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
