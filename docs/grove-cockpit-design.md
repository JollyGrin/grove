# Cockpit redesign — activity feed + parallel orchestrators (draft, from real usage)

> **Status: design draft / ideation.** Written from the operator's field feedback +
> a screenshot of the live cockpit + the parallel-orchestrator workflow.
> Companion to [grove-spec.md](../DESIGN.md).
>
> **Scope update (2026-07-03, design review S-2):** originally framed as
> applying to both `ovs` and Grove, but `ovs` is now strictly frozen
> (spec §12) — **these features ship in Grove** (spec §13 Phase 4); the
> §7 phasing below reads as Grove work. Any `ovs` backport would be a
> deliberate exception the operator triggers, not the plan.
>
> **Scope guardrail (the operator's rule):** keep the split-pane cockpit — `ovs` TUI on
> the **left**, real ccwork orchestrator chat(s) on the **right**. **Never
> render into or touch a chat pane.** This redesign changes the **bottom of the
> left pane** (activity feed) and adds a **spawn action** for parallel
> orchestrators. Everything stays native **tmux** so `ctrl-b z` and standard
> hotkeys keep working.

---

## 1. The thesis (what usage revealed)

- **~90%:** in the **orchestrator chat** (right).
- **~10%:** glance at the list to **attach** to a worker's window.
- **~rarely:** MAIL / REVIEW panels.
- **Workflow:** the operator **parallelizes** — several orchestrator chats at once, one
  per task/context — by manually `ctrl-b "`-splitting the chat pane and running
  `claude --dangerously-skip-permissions`.

> **Chat is the primary UX. The AGENTS list is an attach launcher. Mail is an
> event substrate, not a browse surface. And parallel work means multiple
> orchestrator chats stacked on the right — the manual split needs to become a
> button.**

---

## 2. The cockpit shape

Left column = TUI (**AGENTS** list + **ACTIVITY** feed). Right column = a
**stack of orchestrator chats**. tmux `main-vertical` layout: the TUI is the
_main_ pane (left), chats tile down the right. Mail + review panels are gone.

```
┌ OVERSTORY · the canopy   6 working · 5▷ · ⚠1 ┐   ┌ CHAT · orch-1 (ccwork) ─┐
│ AGENTS  (open worktrees · attach/manage)     │   │ > grab DEV-4790 …       │
│  ◆ DEV-4781 waiting   —    —    1d            │   │ …                       │
│  ● DEV-4759 working  #985 ✓CI ⬡up 2d          │   ├ CHAT · orch-2 ──────────┤
│  ○ DEV-1259 stalled? #951 ✓   ⬡up 18d         │   │ > find 3 easy tickets   │
├ ACTIVITY (swarm actions · newest first) ──────┤   │ …                       │
│  6m  DEV-4790 ✓ done · grabbed → worktree up  │   ├ CHAT · orch-3 ──────────┤
│  2d  DEV-4759 PR #985 opened · CI green       │   │ > draft reviewer reply  │
│  2d  DEV-4772 nudged (PR #998 fixes)          │   │ …                       │
│  18d DEV-1259 ⚠ stalled                       │   │                         │
└────────────────────────────────────────────────┘   └─────────────────────────┘
  O new orchestrator · a attach · o preview · d done · q      ctrl-b z = zoom pane
   left = TUI (main pane)               right column = stacked orchestrator chats
```

- **Right column** — one or more real ccwork orchestrator sessions. Out of
  scope for rendering; `ovs` only _creates and lays out_ the panes.
- **Top-left, AGENTS** — open worktrees; the attach/manage launcher.
  **Unchanged.** `a` jumps into a worker's tmux window.
- **Bottom-left, ACTIVITY (new)** — newest-first render of `events.jsonl`.
- **Header** — keeps the glance counts (mail count can stay too even without a
  panel).

---

## 3. The activity feed (§ unchanged in intent)

- **Source:** `events.jsonl` — already the append-only truth. The feed is a
  _render_, not new state. Near-free.
- **Curated allowlist:** `grabbed · PR opened · CI green · preview up · question
asked · answered · nudged · done · merged · untracked · adopted ·
stalled/blocked`. Poll-noise excluded (exact set → §10).
- **Not redundant with the chat:** the chat is _selective narration_ (only what
  the orchestrator said, only when you're talking to it); the feed is the
  _objective, complete_ record — including what happened **while you were
  away**. It's the backstop to the chat's story.
- **Not redundant with the AGENTS list:** list = current _state_; feed =
  _history_. Orthogonal axes.
- **Grove:** per-workspace (`<root>/.grove/state/events.jsonl`), so it fits the
  workspace switcher (grove-spec §6.5) directly.

---

## 4. Parallel orchestrators — the spawn action (the new feature)

Replace the manual `ctrl-b "` + `claude` habit with a one-key, _correct_ spawn
initiated from the left pane.

### 4.1 The action

- **`O` in the TUI** (and the CLI equivalent **`ovs orchestrator new`**) spawns
  a **fresh orchestrator chat**, splitting the right column so existing chats
  stay open, then focuses the new pane so you can type the task immediately.
- **Context-aware** (see §4.6): the pane joins — and is _scoped to_ — the
  cockpit/session of the repo you invoked it from. Auto when that repo already
  has an active session; prompts otherwise.
- Because the panes are native tmux: **`ctrl-b z`** zooms the focused chat
  fullscreen, **`ctrl-b ↑/↓`** / **`ctrl-b o`** move between them, and exiting
  claude (`/exit`) closes that pane and the layout re-tiles.

### 4.2 tmux plumbing (so it's buildable)

The cockpit is laid out with **`main-vertical`**: the TUI is the main (top-left)
pane; orchestrator chats stack in the right column. Spawn is:

```
tmux split-window -v -c <orchestrator-dir> '<orchestrator-cmd>'   # new right-column pane
tmux select-layout main-vertical                                  # TUI stays main-left, chats stack right
tmux select-pane -t <new-pane>                                    # focus it, ready to type
# main-pane-width is set once at cockpit build so the left column stays readable
```

`select-layout main-vertical` normalizes regardless of which pane was active
when `O` was pressed (splitting even the TUI pane re-tiles correctly, since
main-vertical always makes the top-left pane the main one).

### 4.3 Correctness — why `ovs` must own the spawn

A bare `claude` in a hand-split pane launches a **vanilla session in the wrong
cwd**: no orchestrator `CLAUDE.md` (so it doesn't know its duties/guardrails).
`ovs` spawns with:

- **cwd = the orchestrator dir** (`orchestrator.dir` in config) so its
  `CLAUDE.md` loads. That dir is _untracked_, so worker hooks ignore every
  orchestrator pane — spawning N orchestrators never pollutes fleet state.
- **command = `orchestrator.claude`** from config (default `ccwork`). If you
  want prompt-free parallel orchestrators, set it to include
  `--dangerously-skip-permissions` there — the orchestrator's guardrails live in
  its `CLAUDE.md` (propose-then-confirm), **not** in permission prompts, so
  skipping them is safe and matches your manual habit. (Open question §10.)

### 4.4 When to spawn vs when to `/clear` (lifecycle guidance)

The orchestrator is **stateless-by-design**: its "how to be an orchestrator"
comes from `CLAUDE.md` (reloads on every `/clear` and fresh session), and its
"overview" is re-derived from `ovs ls/mail/audit` + Linear MCP — **not** from
chat history. So:

- **Sequential unrelated topics** → stay in one pane and **`/clear` at the topic
  boundary** (not at ~50% context). `/clear` _is_ a fresh orchestrator, for
  free — same reloaded `CLAUDE.md`, no new pane.
- **Parallel topics alive at once** → **`O`** a new pane per thread (this
  feature). Reserve it for genuinely concurrent negotiations; most one-shot
  asks (grab, summarize, unblock) don't need a dedicated pane.
- **Don't `/clear`** while a background agent you care about is still running
  (orphans its result) or a propose-then-confirm **draft is pending** (it lives
  only in chat). Finish or act first.

### 4.5 Optional enhancement — lossless clears

Persist orchestrator **drafts/proposals as events** (surfaced in the activity
feed), so `/clear` becomes lossless even mid-negotiation and pending drafts
survive. Ties into the propose-then-confirm model + the feed. Not required;
see §10.

### 4.6 Which cockpit the new orchestrator joins (session/context resolution)

`ovs orchestrator new` (and `O`) is **workspace-aware** — it resolves _where you
invoked it from_ rather than always spawning into the current window:

1. **Resolve the ambient workspace** by walking up from `cwd` to the nearest
   `.grove/` (Grove) / configured repo (`ovs`) — the same walk-up the switcher
   uses (grove-spec §6.5.2). Call it **W**.
2. **Is a cockpit session already running for W** (`grove-<W.label>` / `ovs`)?
   - **Yes → auto-join.** Spawn the new orchestrator **into that session's right
     column**, scoped to W's state/config. This is the operator's happy path: _invoked
     from a repo that already has active sessions → go straight to that
     context._ (Pressing `O` from inside a cockpit is always this case — you're
     already in W.)
   - **No running cockpit, or ambiguous** (cwd under no workspace; a nested
     parent/repo pair where nearest-wins is unclear; or several candidate
     sessions) → **prompt**, never guess: _pick an existing running session, or
     create a new cockpit scoped to the repo you invoked from._
3. **Scoping is the point.** The fresh orchestrator is stateless in _chat_, but
   after resolution its `ovs ls`/`mail`/`audit` report **W's** fleet — so "new
   orchestrator from repo X" operates on X, not a global default. That's what
   "auto go to that session's context" means: not chat history, but the
   workspace the CLI is pointed at.

This converges `orchestrator new` with the workspace switcher — from a bare
terminal it behaves like _"enter this repo's cockpit (creating it if needed) and
add an orchestrator."_ What counts as "active" (running cockpit vs. tracked
tasks in state) is an open question (§10).

---

## 5. "Does removing mail + review make the repo more focused?" — the read

| Layer                                                                                                     | Verdict    | Why                                                                                                                                                |
| --------------------------------------------------------------------------------------------------------- | ---------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| **MAIL + REVIEW panels**                                                                                  | **Remove** | Redundant with header counts + AGENTS columns (`waiting` = pending question; `PR/CI/PREVIEW` = review-ready) + chat. Focus gained, no signal lost. |
| **Mail subsystem** (STATUS→question/blocked classification, `answer`/`nudge`/`feedback` relay, ntfy push) | **Keep**   | Load-bearing: sets `waiting` state, fires your phone, lets you reply.                                                                              |

**Optional deeper simplification:** collapse `mail.jsonl` into `events.jsonl`
(mail is just "question/blocked/done are event types"). One log, feed and mail
signal become the same data, trims a package. Separate, tested, **not required**.

**Recommendation:** ship the ACTIVITY feed; **drop REVIEW** (list + header carry
it); **drop MAIL** but keep `ovs mail`/`--json` + an optional `m` popover for a
few weeks, then delete if unused; **later, optionally** unify the logs.

---

## 6. Explicitly NOT touched

The chat panes' contents · the state model / hooks / relay / ntfy push · the
AGENTS list. **Blast radius:** the bottom-left panel, two panel deletions, and
the cockpit layout/spawn plumbing. All native tmux; all reversible.

---

## 7. Phasing

- **P1 — ACTIVITY feed.** Render `events.jsonl` in the left pane below AGENTS. Small.
- **P2 — parallel-orchestrator spawn.** `main-vertical` cockpit layout;
  `ovs orchestrator new` + the `O` keybind; focus + re-layout. _Replaces the
  manual `ctrl-b "` habit._ Small–medium.
- **P3 — drop REVIEW panel.** Lean on AGENTS columns + header. Small.
- **P4 — drop MAIL panel.** Keep `ovs mail` + optional `m` popover. Small.
- **P5 (optional) — lossless clears + log unification.** Persist proposals as
  events; fold `mail.jsonl` → `events.jsonl`. Medium; separate, tested.
- Consider making bare `ovs` default to the split cockpit. Minor.

All independently shippable; P1 and P2 are the two that change daily use most.

---

## 8. Impact on grove-spec.md

- **Principle #5** revised to "chat primary; mail is the event substrate; left
  pane = AGENTS + ACTIVITY; right column = stacked orchestrator chats." (Applied.)
- **Command surface** gains `gv orchestrator new` (and the `O` keybind);
  `gv ui` builds a `main-vertical` cockpit from day one. (Applied.)
- Retires the earlier "push events into the chat as cards" idea — it would
  violate the don't-touch-the-chat rule; the activity feed delivers that value
  non-invasively.

---

## 9. FMA

| Risk                                                                                                           | Criticality | Mitigation                                                                                                                                                                                                     |
| -------------------------------------------------------------------------------------------------------------- | ----------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Manual spawn launches a context-less claude in the wrong cwd                                                   | Important   | `ovs` owns the spawn: correct cwd (orchestrator dir → `CLAUDE.md` loads) + configured command. `O` replaces raw `ctrl-b "`.                                                                                    |
| New orchestrator spawns into the wrong workspace/cockpit (e.g. a global default instead of the repo you're in) | Important   | Ambient walk-up resolves the invoking repo's workspace (§4.6); auto-join only when exactly one active session matches, else prompt — never guess. Scope the new orchestrator to that workspace's state/config. |
| Many stacked orchestrators get cramped/unreadable                                                              | Important   | `main-vertical` keeps the left column at a fixed width; `ctrl-b z` zooms any chat fullscreen; consider a soft cap / warn past ~4 (§10).                                                                        |
| Dropping the mail panel hides a pending question                                                               | Important   | `waiting` in the AGENTS list + header count + ntfy push + it appears in the feed. Signal survives in four places.                                                                                              |
| Activity feed too noisy                                                                                        | Important   | Curated event allowlist; poll/refresh events excluded from the render, not the log.                                                                                                                            |
| `/clear` mid-background-agent or mid-draft loses work                                                          | Important   | Documented lifecycle rule (§4.4); optional proposal-persistence (§4.5) makes clears lossless.                                                                                                                  |
| Orphaned orchestrator panes accumulate                                                                         | Acceptable  | Exiting claude closes the pane + re-tiles; optional `ovs orchestrator ls`/cleanup later.                                                                                                                       |
| skip-permissions on orchestrators removes a guardrail                                                          | Acceptable  | Orchestrator guardrails are `CLAUDE.md`-based (propose-then-confirm), not permission-prompt-based; opt-in via config.                                                                                          |

---

## 10. Open questions

1. **Feed event allowlist** — milestones only, or routine PR/CI transitions too?
   Glance-only, or does a row expand (`enter` → event + pane context)?
2. ~~**Spawned-orchestrator command**~~ **Resolved (2026-07-03 interview):
   `--dangerously-skip-permissions` by default** — guardrails are
   CLAUDE.md-based (propose-then-confirm), matching the manual habit;
   config can revert to prompting.
3. **Soft cap / layout past ~4 orchestrators** — warn? auto-zoom? move overflow
   to new windows instead of more stacked panes? - after 4 then eat up the left side
   3b. **What counts as an "active session" for §4.6 auto-join** — a running
   cockpit tmux session for the workspace, or also a workspace with tracked
   tasks in state but no cockpit up? And when prompting, what's the default —
   create-new-for-this-repo, or the most-recently-used session? - im not sure but i want to know when a tmux has a chat in progress, when it's done, when it has a question, when it's not connected but worktree exists
4. **Pane orientation** — set tmux pane titles (`orch-1/2/3`) for legibility? - yes
5. **`m`/`r` popovers** — keep as insurance, or drop clean? - i think we can drop to reduce complexity?
6. **Lossless clears (§4.5)** — build proposal-persistence, or rely on the
   discipline rule? - yes persist
7. **Default surface** — make bare `ovs` open the split cockpit? - yes. And when i attach to a session, it should not clear the acitivity feed when i come back to ovs window
