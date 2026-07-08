# Per-workspace `claude_config_dir` so cost tracking works under ccwork

**Repo:** grove · **Type:** bug fix (cost display) · **Size:** S

## Problem

The cockpit shows `$0.00` and an empty HISTORY/SPEND for the **thegrid**
workspace, even though every worker transcript exists and is fresh.

Root cause: grove's transcript reader `ProjectDir()`
(`internal/transcript/session.go`) locates transcripts at
`$GV_CLAUDE_CONFIG_DIR/projects/<encoded>`, defaulting to `~/.claude` when the
env var is unset. thegrid is the only workspace whose workers run under
`ccwork` (`CLAUDE_CONFIG_DIR=~/.cc-work`), so its transcripts live in
`~/.cc-work/projects/…`. The `gv dash` process has `GV_CLAUDE_CONFIG_DIR`
unset → it scans `~/.claude/projects` → finds nothing → every ticket totals
`$0.00`. Data is intact; only the lookup path is wrong.

Confirmed fix in one line: `GV_CLAUDE_CONFIG_DIR=~/.cc-work gv cost` returns the
full ledger (~$631 total) immediately.

## Firewall constraint (non-negotiable — read before implementing)

The work/personal **subscription** boundary MUST NOT be affected. Verified facts
that this change must preserve:

- Subscription is governed **solely** by the `claude:` command field
  (`repo.Claude` / `orchestrator.Claude`) — e.g. `ccwork …` vs plain `claude`.
- `GV_CLAUDE_CONFIG_DIR` is read in **exactly one place** (`session.go:38`,
  the transcript reader) and feeds cost display only. It has **no path** to the
  worker-spawn command. A wrong value can only mis-display cost — never move a
  subscription.
- `merge.go` already `delete`s the global `orchestrator` block before merge, so
  a workspace cannot inherit the global claude command (this sealed the
  2026-07-05 ccwork-leak incident). The new knob must inherit the **same
  discipline**.

The change below touches only the cost-reader pointer. It stays on the
display-only code path by construction.

## Changes

### 1. Config: add a workspace-scoped `claude_config_dir` knob

- New optional field, workspace-scoped (alongside `workspace: {label, scope}`),
  e.g. `claude_config_dir: ~/.cc-work`.
- Expand `~` to the home dir on read.
- Set it **only** in `~/git/thegrid/.grove/config.yaml`. Never in global.

### 2. Merge: never inherit the knob from global

- Give `claude_config_dir` the same drop-from-global treatment as
  `orchestrator` in `internal/config/merge.go` (delete from the global layer
  before merge). Belt-and-suspenders: a stray value in global must never reach a
  hobby workspace.

### 3. Inject into the dash pane launch (the single fix site)

- In `buildCockpit` (`cmd/gv/main.go`, ~L359–363), the dash pane is launched via
  `tmux.SendKeys(session+".0", dash)` where `dash = "gv dash"` (or `exe+" dash"`).
- When the resolved workspace config has `claude_config_dir` set, prefix the
  launched command with `GV_CLAUDE_CONFIG_DIR=<value> ` so the `gv dash` process
  (which does both cost-reading and resource/cost recording) inherits it.
- No other pane needs it: the orchestrator/worker panes set their own subscription
  via `claude:` and don't read transcripts.

### 4. (Optional) also honor it in `gv cost` / `gv ls` CLI paths

- Those subcommands, when run outside the cockpit, should resolve the same
  workspace knob and set `GV_CLAUDE_CONFIG_DIR` before scanning, so ad-hoc
  `gv cost` from a plain shell also reports correctly. If low-effort, include it;
  otherwise file as a follow-up.

## Explicitly NOT doing

- **No `CLAUDE_CONFIG_DIR` ambient fallback in `ProjectDir`.** Rejected on
  purpose: it reintroduces inherited-env behavior (the exact mechanism behind the
  2026-07-05 slip-up). Keep resolution explicit and per-workspace only.

## Acceptance criteria

- With `claude_config_dir: ~/.cc-work` in thegrid's config and a rebuilt cockpit
  (`gv`), the dashboard shows non-zero EST $ / SPEND / HISTORY for thegrid tickets.
- grove, unbrewed, waterhouse cockpits still read `~/.claude` (unchanged) — verify
  each still shows its own costs, none show thegrid's.
- No workspace's `claude:` / subscription behavior changes. Grep proof:
  `GV_CLAUDE_CONFIG_DIR` still appears only in `session.go` + the new injection
  site; the worker-spawn path is untouched.
- A `claude_config_dir` placed in the global config does **not** propagate to any
  workspace cockpit (merge-drop test).

## FMA

- **Knob leaks to a hobby workspace → reads work transcripts** (Important):
  mitigated by set-only-in-thegrid + drop-from-global in merge.go. Display-only
  even if it did leak — cannot burn the work subscription.
- **Dash prefix breaks the pane launch** (Acceptable): only prepends
  `VAR=val ` to an existing shell command line; verify the pane still starts.
- **Someone later wires the knob into the `claude:`/spawn path** (Critical if it
  happened): guard with the acceptance grep above; document in the field's
  comment that it is cost-reader-only.
