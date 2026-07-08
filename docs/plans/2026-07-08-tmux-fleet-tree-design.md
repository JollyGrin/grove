# tmux fleet tree — making `Ctrl-b w` legible (design draft)

> **Status: design draft / ideation.** Written from operator field feedback
> on the `choose-tree` view (`Ctrl-b w`) + a screenshot of the live tree +
> a code map of `internal/tmux/` and the grab flow. Companion to
> [DESIGN.md](../../DESIGN.md) and [grove-cockpit-design.md](../grove-cockpit-design.md).
>
> **Scope (hard boundary).** This feature is *only* about the **`Ctrl-b w`
> session/window tree** — how the fleet reads when you pop the tmux tree to
> navigate. It changes **session structure and session/window naming** so the
> tree is a legible, at-a-glance fleet board.
>
> **Explicitly out of scope:**
> - The **cockpit-when-attached** UX (the `gv dash` + orchestrator pane
>   layout). That is fine as-is — we do not touch panes, `main-vertical`, or
>   the chat surface. See the cockpit-design guardrail.
> - The **worker window internals** (pane .0 worktree shell + pane .1 claude
>   agent split). Unchanged — the split-screen the operator likes stays.
> - **Mobile** (`grove-mobile`). It is a viewer that rides on top of whatever
>   grove does; it must not drive naming. The only thing this doc says about
>   mobile is that its *own* session name stays `grove-mobile`.
>
> **Coexistence note (retired).** Earlier docs kept worker sessions on ovs's
> `pr-<repo>` naming so a repo tracked by both ovs and grove shared one
> session. **The operator now runs grove exclusively; that constraint is
> retired.** This design is free to rename/restructure worker sessions.
> CLAUDE.md's "worker tmux sessions still use ovs's `pr-<repo>` naming"
> caution should be dropped when this ships.

---

## 1. The thesis (what the tree should be)

`Ctrl-b w` is the operator's fleet map — the fastest way to jump to the
worker that needs them. Right now it's raw tmux defaults over a session
model designed for the ancestor app (`parkranger`), before grove grew
workspaces + a cockpit. The tree should answer three questions at a glance:

1. **Which workspace am I looking at?** (project grouping)
2. **Which of these is the cockpit vs a worker?** (role)
3. **Which worker needs me right now?** (live status)

Today it answers none of them cleanly.

---

## 2. Current model (what the tree shows today)

Two session families, built by different code paths, with **zero status-bar
or window-naming configuration** — everything below is stock tmux default.

| | Worker session | Cockpit session |
|---|---|---|
| Name | `pr-<repo>` (`tmux.go:29`) | `grove-<label>` / `grove` (`main.go:341`) |
| Scope | one **per repo** | one **per workspace** |
| Created by | `EnsureSession` in grab (`tmux.go:140`) | `buildCockpit` (`main.go:360`) |
| Window 0 | `dashboard` — a **bare hostname shell** | the real `gv dash` TUI |
| Other windows | one per ticket, `<ID>-<slug>` | none (single window) |
| Window panes | .0 worktree shell · .1 claude agent | .0 dash · .1+ orchestrator chats |

A parent-scope workspace (e.g. `unbrewed`) fans out to **several** repos
(`unbrewed-p2p`, `unbrewed-pro-server`) → several `pr-*` sessions, none of
which visibly belong to the `grove-unbrewed` cockpit.

### The live tree, annotated

```
(0)  - grove-unbrewed: 1 windows (attached)   ← COCKPIT, but reads like just another repo
(1)     └ 0: 2.1.204*                          ① claude's version string as the window name = noise
(2)  - pr-discovery: 2 windows
(3)     └ 0: dashboard: "Deans-MacBook-Pro"    ② vestigial hostname shell, one per repo, pure clutter
(4)     └ 1: DEV-4695-roll-out-new-profile...  ⑤ long slug, no status — running? blocked? PR-ready?
...
(M-h)- pr-unbrewed-p2p: 3 windows              ┐ ③ unbrewed's cockpit (row 0) and its two worker
(M-l)- pr-unbrewed-pro-server: 2 windows       ┘   sessions are scattered — no visible family
```

### Painpoints

1. **The cockpit is unreadable as a cockpit.** `grove-unbrewed → 0: 2.1.204*`
   — tmux's `automatic-rename` latches onto the claude pane's process title
   (Claude Code sets it to its bare version, `2.1.204`). The one session you
   steer *from* has the least identity of anything in the tree.
2. **A dead `dashboard` window in every worker session.** A parkranger
   artifact — back then it was the dashboard; now the cockpit is. Today it's a
   hostname shell adding a noise row per repo.
3. **Two prefixes, no family.** `grove-unbrewed` (cockpit) and
   `pr-unbrewed-p2p` / `pr-unbrewed-pro-server` (its workers) are one project,
   but the `grove-`/`pr-` split + default sort (creation order, *not*
   alphabetical) scatters them.
4. **The tree isn't a status board.** Window names are long branch slugs;
   nothing shows running / blocked-on-question / PR-ready. You must attach to
   a window to discover it's been waiting on you.

Root cause of #1–#3: **grove never tells tmux who each session/window is, and
the session unit (repo) doesn't match the mental unit (workspace).**

---

## 3. The design

Three moves, structural first. Each is independently shippable; together they
turn `Ctrl-b w` into a fleet board.

### Move A — One session per workspace (structural, Idea D)

Collapse a workspace's cockpit **and** all its workers into a single session
`grove-<label>`. The session becomes the workspace; windows become its parts.

```
grove-unbrewed                                 ← top level = the workspace, period
  0: cockpit        [ dash │ orchestrator ]    ← window 0 = the real cockpit (panes unchanged)
  1: p2p · 154-pro-undo-button      ⏸ needs you
  2: pro-server · 39-undo-backend   ● live
grove-thegrid
  0: cockpit        [ dash │ orchestrator ]
  1: monorepo · 4759-gridimporter   ● live
  2: monorepo · 4772-chunkloaderror ✔ PR ready
  3: discovery · 4695-profile-page  ● live
grove-waterhouse
  0: cockpit
  1: landing · 12-hero-copy         ● live
```

Why this is the keystone:
- **Grouping is structural, not sort-dependent.** tmux `choose-tree` orders
  sessions by creation index by default (not name), so any prefix-based
  "make them sort adjacent" scheme is fragile. One-session-per-workspace makes
  the grouping a fact of the tree, immune to sort order.
- **Kills painpoint #3 entirely** — cockpit and workers are the same node.
- **Kills painpoint #2** — window 0 *is* the cockpit dash, so the vestigial
  `dashboard` hostname shell disappears; `EnsureSession`'s bare window goes
  away.
- **Repo distinction survives** in the window name prefix (`p2p ·`,
  `pro-server ·`) — a parent-scope workspace's repos stay legible.

Blast radius is small: `gv attach/adopt/sweep/untrack/detect` all resolve the
worker via the `tmux_session` + `tmux_window` stored on the task event
(`main.go:767`), so they keep working as long as **grab writes the new
session name**. The change is essentially localized to *which session grab
targets*.

**Grab flow change.** Instead of `tmux.SessionName(repoName)` +
`EnsureSession(repoName…)`, grab resolves the repo's workspace (walk up from
`repo.Path` to the nearest `.grove/` marker — the same `workspace.Find` walk),
targets `grove-<label>`, ensures it exists, and `CreateWindow`s the worker
there. Window internals (split + claude in pane .1) are unchanged.

#### Why one session — not a cockpit session + a separate agents session

A reasonable instinct is to keep the cockpit in its own session and put the
worker agents in another (`grove-<label>` + `grove-<label>-agents`) for
"failure isolation." It buys less than it looks, because of how tmux's
failure domains actually work:

- **The tmux *server* is the failure domain, not the session.** Every session
  in a workspace lives in one tmux server process. If that process dies — an
  OOM/jetsam kill under memory pressure, or the `kill-server`-class bug that
  already bit grove (grove-7, `$TMUX`/`TMUX_TMPDIR`) — **all** sessions die
  together regardless of how they're split. Splitting sessions gives zero
  protection against the crash that actually happens.
- **Real recovery lives outside tmux.** Durability is grove's append-only
  `events.jsonl` + the git worktrees + `claude --resume <sessionID>`, and
  `gv adopt` already rebuilds a worker window from that state. Pickup after a
  crash is session-topology-agnostic — adopt reconstructs windows into
  whatever session we name, so one-vs-two sessions doesn't change how well we
  recover.

What a separate cockpit session *would* genuinely buy:
- **Independent client sizing.** A session sizes to the smallest client
  attached to it; a narrow client attaching to a combined session would shrink
  the worker windows too. But this is exactly why **mobile is already its own
  session** — the phone case is handled — and desktop attach is uniform width.
- **Kill/restart blast radius.** `kill-session` the cockpit to reload the
  orchestrator brain without touching running agents (or shed the whole worker
  fleet under memory pressure while keeping your control surface). With one
  session these become window-level ops (`kill-window`/`respawn-window`) —
  grove scripts them either way, so it's ergonomics, not capability.

**Both real benefits cut *against* this feature's entire goal** — one legible
`Ctrl-b w` node per workspace. A separate agents session reintroduces the
two-nodes-per-workspace scatter (§2 painpoint #3), which name-based clustering
can't reliably fix (choose-tree sorts by creation order, not name).

**Recommendation: one session, cockpit as window 0.** Split only if the
sizing / kill-isolation needs prove real in practice — and if so, prefer the
lighter naming fallback (§5 Q6) over a hard session split. Harden the
*server* instead, where the actual failures live:
- `set -g exit-empty off` — the server survives its last window closing.
- `set -g destroy-unattached off` — sessions survive detach.
- Lean on `gv adopt` as grove's native resurrect; no tmux-resurrect plugin
  needed.

### Move B — Real names, no auto-rename leak (Idea A/C, confirmation #1)

- **Name the cockpit window `cockpit`** and turn **`automatic-rename off`**
  for it so claude's `2.1.204` process title can never clobber it. This is
  confirmation #1 — a real name replacing the version string. It's scoped
  **per window** (`set-window-option -t <win> automatic-rename off`), so it
  cannot affect any other process or any window outside grove's sessions.
- **Name worker windows `<repo-short> · <ID>-<slug>`** and set
  `automatic-rename off` on them too — a worker window's name should be the
  ticket, never whatever the pane's foreground command happens to be.
- `<repo-short>` is the repo name with the workspace label stripped when it's
  a redundant prefix (`unbrewed-p2p` → `p2p` inside `grove-unbrewed`), else
  the bare repo name.

### Move C — Live status glyph in the window name (Idea C, painpoint #4)

Append a status glyph to each worker window name so the tree reads as a board:

```
● live        agent actively working
⏸ needs you   waiting on a question / plan approval  ← the row that must jump out
✔ PR ready    PR open, CI green
✗ stalled     no activity / crashed / disconnected
```

The state already exists — it's exactly what `gv ls --json` computes and what
the cockpit dash renders. The only new thing is pushing it into the window
name via `tmux rename-window`.

**Driver (respecting the RAM guardrail).** Per
[cockpit-ram-reserved-for-workers], we add **no new goroutine, poll, or
cache**. Two candidate drivers, both reuse existing work — reviewer to pick:
- **(preferred) event-driven** — the worker sentinel/hook path already fires
  on Stop / Notification / PR events; have it `rename-window` on transition.
  Live, zero polling.
- **piggyback the dash refresh** — the dash already recomputes fleet state on
  its existing tick; emit renames as a side effect of data it already holds.
  No new tick, but couples window names to the dash being open.

---

## 4. Before / after

```
BEFORE (stock tmux over a repo-keyed model)      AFTER (workspace-keyed, named, live)
─────────────────────────────────────────        ──────────────────────────────────────
- grove-unbrewed: 1 windows                       grove-unbrewed
    └ 0: 2.1.204*                                   0: cockpit      [dash │ orchestrator]
- pr-discovery: 2 windows                           1: p2p · 4695-profile-page   ⏸ needs you
    └ 0: dashboard: "Deans-MacBook-Pro"             2: pro-server · 39-undo-be   ● live
    └ 1: DEV-4695-roll-out-new-prof...            grove-thegrid
- pr-unbrewed-p2p: 3 windows                         0: cockpit
    └ 0: dashboard: "Deans-MacBook..."               1: monorepo · 4759-gridimp   ● live
    └ 1: unbrewed-p2p-154-pro-undo...                2: monorepo · 4772-chunkld   ✔ PR ready
- pr-unbrewed-pro-server: 2 windows                  3: discovery · 4695-profile  ● live
    └ ...
```

Bottom-bar side effect (not the focus, but it comes for free): attached, the
window name is now `cockpit` or `p2p · 4695-… ⏸`, so the default
`status-left`/window list finally reads as "you are here" instead of
`grove-unbrewed | 0:2.1.204`.

---

## 5. Open questions for design review

1. **Grab before the cockpit is open.** If `grove-<label>` doesn't exist yet
   when you grab, what happens to window 0?
   - (a) grab creates the session with the *worker* as window 0; `gv` later
     inserts the cockpit and the worker shifts down. (reorder cost)
   - (b) grab creates a reserved placeholder window 0 (`cockpit: run gv`
     hint) so the slot is always the cockpit's; `gv` upgrades it in place.
   - (c) grab spins the full cockpit (dash + orchestrator). (heavy —
     spawns claude unexpectedly on a grab)
   Leaning **(b)**.
2. **Un-workspaced / legacy path (`ws == nil`, session `grove`).** Repos not
   under any `.grove/` workspace: keep the old `pr-<repo>` behavior for them,
   or route into the global `grove` session? Leaning: **keep `pr-<repo>` as
   the fallback only for un-workspaced repos**; the collapse applies whenever
   a workspace is resolvable (which is the operator's normal case).
3. **Window ordering within a session.** Should workers be grouped by repo
   (all `p2p` windows, then all `pro-server`)? tmux window index is creation
   order. Options: accept creation order (name prefix still groups them
   visually in the eye), or `move-window`-renumber on grab. Leaning: **accept
   creation order** — the name prefix carries the grouping; renumbering is
   fiddly and churns indices other commands may have cached.
4. **Glyph driver** — event-driven (§3 Move C preferred) vs dash-piggyback.
   Reviewer's call; event-driven keeps the tree live even with the dash
   closed.
5. **`closablePane` guard interaction.** The orchestrator self-close guard
   (`grove.go:57`) already whitelists `grove`/`grove-*` and forbids pane
   index 0. With workers now living in `grove-<label>`, worker windows also
   pass the session check — but the guard is only invoked for orchestrator
   self-dismissal in the cockpit window, and each window's pane 0 is
   protected, so no worker pane is at risk. Confirm the guard still reads
   correctly under the collapsed model; likely a comment update, no logic
   change.
6. **Alternative if collapse proves messy: prefix-only.** Keep
   session-per-repo but rename so a workspace clusters — cockpit
   `grove-<label>`, workers `<label>-<repo>` — plus Moves B & C. Lower blast
   radius, but grouping depends on the user's `choose-tree` sort order, so it
   degrades to "adjacent-ish." Documented as the fallback, not the plan.

---

## 6. Phasing

- **P1 — Names (Moves B + partial).** `automatic-rename off` + explicit
  `cockpit` / `<repo> · <ticket>` window names. No structural change; kills
  painpoints #1 and the `2.1.204` leak immediately. Smallest, highest
  "you are here" payoff — good first ticket.
- **P2 — Collapse (Move A).** Grab targets `grove-<label>`; retire the
  vestigial `dashboard` window and the `pr-<repo>` worker sessions (kills #2,
  #3). Resolve open questions 1–3 first.
- **P3 — Live glyphs (Move C).** Wire the rename driver (kills #4). Board is
  complete.

Each phase is independently useful and independently revertible.

---

## 7. Acceptance (what "beautiful `Ctrl-b w`" means)

- Top-level tree nodes are **workspaces**, in the operator's mental order.
- Every session expands to `0: cockpit` + worker windows — role is obvious.
- No `2.1.204`, no `dashboard` hostname shell, no orphan `pr-*` scatter.
- Each worker window shows repo · ticket · a live status glyph; the one that
  `⏸ needs you` is visually findable in under a second.
- Nothing in the cockpit-attached pane layout or worker split changed.
