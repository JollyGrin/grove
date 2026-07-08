# Cockpit joy — flourish & whimsy for the grove TUI

**Status: DRAFT — awaiting Dean's review**

## Why

The cockpit works. Dean wants it to *delight* — enough that showing it to
someone sells grove on sight. Claude Code proved a text box can carry
personality (spinner verbs, "Brewed for 43s"); grove should have its own
dialect. Two hard constraints from Dean:

1. **Weight ≈ zero.** Free memory belongs to agents. No goroutines, no
   extra tick loops, no per-frame allocations. Effects must be toggleable
   and capped.
2. **Never in the way.** The current flows (open, peek, reply, costs,
   activity) are solid. Flourish is a layer *over* them, never a change
   *to* them.

## Design stance: a forest, not an arcade

Claude Code's whimsy is frantic — 10fps spinners, razzle-dazzle verbs.
Grove's counterpart should be **calm**: the metaphor is a forest, and
forests don't strobe. Two registers:

- **Ambient life** — the screen breathes at 1 frame/second (the refresh
  tick we already have). Slow rhythm reads as alive-but-calm, and costs
  nothing: no new timers, ever.
- **Moments of joy** — Dean's motif. Joy concentrates at *events*: a PR
  merges, a task finishes, a sapling is planted. One-shot, a few seconds,
  then gone. Celebration, not decoration.

Everything below piggybacks on the existing 1s `refreshMsg` tick
(`internal/tui/tui.go`): the Model gains a `tick uint64` counter and a
small `celebrations map[string]int` (ticket → ticks remaining, capped);
frame tables are package-level `[...]string` consts.

## Catalog

### Ambient (always-on under `calm`, zero-cost)

| # | Flourish | Anchor | Detail |
|---|----------|--------|--------|
| A1 | **The grove breathes** | `statusGlyph` (styles.go) | Working `●` cycles `◉ ● ◎ ●` on the tick — a 4s breath, not a spinner. Idle/question glyphs stay still (stillness = nothing moving = glanceable). |
| A2 | **Grove verbs** | LIVE column (view.go) | When live status is `working`, replace the static word with a rotating gerund: *photosynthesizing · grafting · pruning · pollinating · rooting · mulching · leafing out · budding*. Deterministic: `hash(ticket) + tick/8` indexes the table — each agent gets its own verb, changing ~every 8s, no strobe. |
| A3 | **Weather gauge** | `memGauge` (view.go) | Prefix the memory reading with weather: `☼` clear (green), `☁` clouds gathering (amber), `⛆` storm (red). The resource metaphor becomes legible at a glance. |
| A4 | **Living empty states** | `viewAgents`/`viewActivity` | Time-of-day variants: morning *"dew on the leaves — gv grab plants one"*, night *"the grove sleeps"*. Pure lookup on `time.Now().Hour()`. |
| A5 | **Quit farewell** | after `Run` returns (cmd/gv) | One dim line post-alt-screen: *"the grove keeps growing"* / *"n trees still working"*. Rotates. Zero runtime cost. |

### Moments of joy (one-shot, event-triggered)

| # | Moment | Trigger | Detail |
|---|--------|---------|--------|
| J1 | **Merge sparkle** | `prsMsg` diff: state flips to MERGED | The row's `⬢` gains `✦ ⬢ ✦` shimmer for ~6 ticks + footer flash *"⬢ grove-18 merged — the canopy grows"*. Detected by comparing old/new `m.prs` in Update; no new I/O. |
| J2 | **Done ritual** | `actionDoneMsg` success | Flash becomes *"✓ grove-18 shipped — tree #24 this season"*. The count is `EvTaskDone` events already in `events.jsonl`. |
| J3 | **The forest strip** | header right / costs page | One `⸙` per task done this week: `⸙⸙⸙⸙`. Your shipped work literally grows a grove. Cap at ~12 glyphs then `⸙×23`. |
| J4 | **Planting** | `EvTaskCreated` fresh in feed | Feed text for a just-planted task (< 1 min old) renders as *"planted 🌱 worktree up"* → after a minute it settles to the normal line. (Glyph from the tested single-cell set; see Constraints.) |
| J5 | **Question knock** | `EvNotification`/question fresh | The amber `◆` row pulses bold↔normal for ~4 ticks when it first appears. Functional whimsy — draws the eye to exactly the thing that needs Dean. |
| J6 | **Milestones** | every 10th J2 | *"that's 30 shipped — quite the orchard."* |

## Effects knob

- Config: `cockpit.effects: full | calm | off` (default **calm**: ambient
  only, no celebrations… or full — Dean's call). Runtime toggle on a key
  (suggest `*`), flash confirms.
- One guard: `if m.fx >= fxCalm { … }` at each render site. `off` renders
  exactly today's output — byte-identical, asserted by a test.
- Celebrations map capped (≤ 16 entries); entries decrement each tick and
  delete at zero. No unbounded growth.

## Hard constraints (from the codebase)

- **No emoji / double-width glyphs in padded columns.** `pad`/`trunc` do
  rune-cell math; the code already fought ANSI-width bugs (see comment in
  `viewAgents`). Every new glyph must pass a `lipgloss.Width == 1` test.
  Candidate set to verify: `✦ ✧ ⸙ ◉ ◎ ☼ ☁ ⛆ ❀ ✽`.
- **No new tick loops.** Animation frame = existing 1s refresh counter.
  If 1s ever feels too slow, the answer is better frame design, not a
  faster timer.
- **Feed strings stay stable.** `feed_test.go` asserts them; J4 wraps the
  presentation layer, it does not rewrite `feedItems`.
- Frame/verb functions are pure (`frame(tick, ticket) string`) — TDD per
  repo conventions.

## Sizing

One package (`internal/tui`), no schema, no new deps, clear acceptance
criteria per item. A1–A4 + J1–J2 + the knob is one comfortable ticket;
J3–J6 + A5 a follow-up. Very grabbable.

## Prior art consulted

- Claude Code spinner verbs — the canonical "fill opacity with feeling"
  move; grove verbs (A2) are the forest dialect of it.
- tui-design skill (box drawing, glyph sets, anti-patterns) — informed
  the single-cell glyph constraint and the "one aesthetic, executed
  precisely" rule; grove's is already established (canopy palette,
  rounded borders, ❉).
