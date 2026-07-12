# The Living Grove — cockpit scene, cast, day cycle, almanac

**Status:** approved by Dean 2026-07-12 (interactive design session, orchestrator).
**Ticket:** grove-NN (filled in by the issue).
**Builds on:** grove-22/24/56 (joy v1/v2), grove-60 (footer), docs/plans/2026-07-08-cockpit-joy-design.md.

## What / why

The cockpit's grove metaphor is told, not shown: trees exist in copy
("plants one", "the canopy grows") but the screen is mostly empty black on a
quiet fleet. This work turns the dead space between ACTIVITY and the footer
into a **scene**: a landscape where every tree is a real tracked task, every
figure a real signal. Plus an **almanac** mode (`g`): one garden per day,
browsable history. Purely visual — zero feature regressions, zero new data
sources beyond one read-only tmux call.

Everything below was decided with Dean; do not re-litigate choices, do not
substitute glyphs (this exact set is verified visible in his terminal font).

### Target render — day, working fleet

```
 ⁂ GROVE · grove-repo    ☼ 3.8G avail · 4w    ♠×7 · 2 working · 1 mail · 0 review

╭ AGENTS ─────────────────────────────────────────────────────────╮
│▸ ● grove-63  grove  growing    #71 ◌    3m   codex adapter      │
│  ◆ grove-58  grove  QUESTION   #69 ✓  1h02m  auth flow          │
╰─────────────────────────────────────────────────────────────────╯
╭ ACTIVITY ───────────────────────────────────────────────────────╮
│   2m  ◆ grove-58  needs input — asked about auth flow           │
│  14m  ↩ grove-63  answered                                      │
╰─────────────────────────────────────────────────────────────────╯

                                       ˙✧
        ♠♠♠         ♠♠♠♠              ·  ψ            ∆ ◆
         ┃           ┃┃              ♟   │            │ ♛
   ▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁
      #49 ⬢       #51·#56 ⬢         #63 3m          #58 ?

 O new chat · ) profiled chat  │  ? help · L layout · $ costs · …
```

### Target render — almanac (`g`)

```
╭ THE ALMANAC ── friday · 2026-07-11 ─────────────────────────────╮
│                                                                 │
│          ♠♠♠            ♠♠♠♠            ♠♠♠                     │
│           ┃              ┃┃              ┃                      │
│    ▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁                   │
│      #56 ⬢           #57 ⬢            #59 ⬢                     │
│                                                                 │
│    3 shipped · 5 planted · 12 answered · 2 questions            │
╰─────────────────────────────────────────────────────────────────╯
 h/← older · l/→ newer · esc back · q quit
```

## Glyph vocabulary (LOCKED — verified in Dean's font)

| Role | Glyph | Notes |
|---|---|---|
| Mature/merged tree | `♠` canopy, `┃` trunk | canopy row is `♠♠♠` (plotW≥8) or `♠` |
| Established plant (≥2h) | `♣` canopy, `│` trunk | |
| Young tree (30m–2h) | `∆` over `│` | |
| Sapling (<30m) | `ψ` | no trunk row needed |
| Fresh planting (<1m) | `✿` | matches J4 plantGlyph |
| Seed (setup/queued) | `◌` on the soil | |
| Soil | `▁` repeated | |
| Worker pawn | `♟` | canopy green (sWorking) |
| Dean queen | `♛` | amber bold (sWaiting) |
| Orchestrator fairy | `✧` + trail `˙` `·` | sky blue (sDelivery) |
| Sky | `☼` day, `☾` night, `✦` fireflies (amber) | |
| Hover markers | `◆` question (amber), `⚠` blocked (rust), `✗` dead (fog) | |

Every glyph MUST be `lipgloss.Width == 1` (existing hard constraint — no
emoji, no double-width). **Do not use `⸙` anywhere — it renders as tofu in
the operator's font** (see S0).

## Feature specs

### S0 — Repairs (ship first, own commit)

1. **`⸙` → `♠`**: in `internal/tui/joy.go` change `const forestGlyph = "⸙"`
   (line ~283) to `"♠"`. Update every test that asserts the old rune
   (`joy2_test.go` J3 cases). Nothing else about J3 changes (cap 12, `×N`
   condensation, moss style).
2. **viewHeader width clamp**: `view.go:41-72` never applies a final clamp —
   a long workspace label + gauge + counts can exceed `m.width` and
   hard-wrap the alt-screen (known gap; only the strip-shedding path is
   tested). Fix: `return truncPad(line, m.width)` as the final statement.
   Add a test: narrow width + long label + no strip ⇒
   `lipgloss.Width(header) <= width`.

### S1 — Scene core (fxCalm+, modeList only)

New file `internal/tui/scene.go` (+ `scene_test.go`). The scene renders
**only in modeList**, between the ACTIVITY panel and the footer, with **no
border** (open air). Pure function of
`(tasks, prs, events, celebrations, tick, width, rows, hour, fx)` — returns
exactly `rows` lines, each `truncPad`-ed to `m.width`.

**Height budget** — replace the sizing in `viewActivity` (`view.go:225`)
with a shared helper so ACTIVITY and the scene split the same leftover:

```
leftover := m.height - (len(m.tasks)+4) - 5 - m.footerHeight()   // as today
if fx == fxOff { activityRows = min(items, leftover); sceneRows = 0 } // byte-identical today
else {
    sceneRows = leftover - min(items, leftover)   // scene takes what the feed doesn't need
    if sceneRows < 3 && leftover >= 7 { sceneRows = 3 }  // feed yields, keeps ≥4 rows
    if sceneRows < 3 { sceneRows = 0 }             // too short for even a strip
    activityRows = leftover - sceneRows
}
```

View() emits: header, agents, activity, scene (when sceneRows > 0), footer —
the scene lines replace rows ACTIVITY previously filled, so total render
height is unchanged. **Assert total lines ≤ m.height in tests for short,
tall, busy, and empty variants.**

**Tiers** (scene always emits exactly sceneRows lines, blank sky lines pad
the top):
- 3–5 rows: strip — canopy row, soil row, label row.
- 6–8 rows: compact — + trunk row (figures live here) and ≥1 sky row.
- ≥9 rows: full — ≥2 sky rows (sun/moon/fireflies/fairy) above.

**Plot layout** (deterministic):
- Plots: orchard (merged/done) on the left, then one plot per `m.tasks` in
  table order. `plotW = 10` cells; center each tree in its plot with a
  jitter of `hash(ticket)%3 - 1` cells (reuse `hashTicket`).
- Orchard source = `EvTaskDone` events in `m.events` (the loaded 200-event
  window — same season semantics as the J3 strip). Newest 3 get individual
  plots (label `#N ⬢`); any remainder condenses into one plot whose canopy
  is `♠` and label `♠×K`.
- Overflow (nPlots·plotW > width-2): drop the condensed plot → shrink
  orchard to 1 plot → plotW to 8 → 6 → finally keep the leftmost plots that
  fit and make the last plot's label `+K more`. Never truncate mid-plot.

**Plant per active task** — stage by `Label()` and `age = time.Since(t.Created)`:

| Condition | Canopy row | Trunk row | Label |
|---|---|---|---|
| setup/queued | `◌` on soil | — | `#N setup` |
| working, age<1m | `✿` | — | `#N <age>` |
| working, age<30m | `ψ` | — | `#N <age>` |
| working, 30m–2h | `∆` | `│` | `#N <age>` |
| working, ≥2h | `♣` (or `♣♣` at plotW≥8) | `│` | `#N <age>` |
| PR merged (active) | `♠` | `┃` | `#N ⬢` |
| QUESTION | stage glyph + `◆` in sky row above | stage | `#N ?` |
| BLOCKED | stage glyph + `⚠` above | stage | `#N ⚠` |
| dead | `✗` (fog) | — | `#N ✗` |
| idle ✓ | stage glyph in bark (sIdle) | stage | `#N ✓` |

Soil label `#N`: trailing digits after the last `-` of the ticket id
(`grove-63` → `#63`, `DEV-123` → `#123`); fall back to the full ticket if no
digits. Labels are `trunc`-ed to plotW-1.

QUESTION's `◆` reuses the J5 knock: while `celebrations[knockKey(ticket)]`
is live at fxFull, style with `knockStyle(m.tick)`.

**Empty grove** (no tasks, no orchard): render the sky + soil only; at the
night bucket, fireflies fly here (see S3). The existing A4/A7 empty-state
lines inside AGENTS/ACTIVITY panels are untouched.

### S2 — The cast (fxFull only)

- **Pawn `♟`** (sWorking): one per task with `t.Agent == state.AgentWorking`,
  on the trunk row beside the trunk; side alternates every 4 ticks:
  `left if (tick/4 + hashTicket(ticket)) % 2 == 0 else right`.
- **Walk-off**: in `Update`'s `refreshMsg` branch, diff prev/next tasks
  (the J5 `freshQuestions` pattern) for tickets whose Agent flipped
  working→anything-else; store `celebrations["w"+ticket] = walkTicks` (8),
  respecting `maxCelebrations`. While live, the pawn renders in bark
  (sIdle) offset right of its plot by `walkTicks - remaining` cells; gone at
  expiry. Prefix `"w"` cannot collide with J1 (bare ticket) or J5 (`"?"`).
- **Queen `♛`** (sWaiting bold) — Dean's presence. New read-only helper in
  `internal/tmux/grove.go`:

  ```go
  // ActiveWindow returns the name of the session's active window ("" on any
  // error). Read-only: list-windows only, never touches window state.
  func ActiveWindow(session string) string   // list-windows -t <session> -F "#{window_active}\t#{window_name}", take the "1"-row
  ```

  In `refreshCmd` (`tui.go:160`), call it once per refresh; inside the
  existing per-task loop the resolved window name is already computed
  (`tmux.ResolveWindowName(...)` at tui.go:171) — when it equals the active
  name, set a new `refreshMsg.focused string` = that ticket; carry to
  `m.focused`. The queen stands on the trunk row of the focused ticket's
  plot, opposite the pawn's current side. No focused ticket ⇒ no queen.
- **Fairy `✧`** (sDelivery) — the orchestrator's touch. Purely derived, no
  new state: for each ticket, the most recent `EvAnswered` in `m.events`
  with `time.Since(ev.Time) < 45s` summons the fairy over that plot. It
  orbits on the lowest sky row: x = plot center + `orbitOffsets[tick % len]`
  where `orbitOffsets = [-2,-1,0,1,2,1,0,-1]`; a dim trail rune (`˙` or `·`
  alternating by tick parity) renders at the previous offset.

Multiple markers on one sky cell: priority `◆/⚠` > fairy > firefly (drop the
lower-priority glyph, never overlap).

### S3 — Day cycle

Reuse `timeOfDay(hour)` buckets (0 morning / 1 day / 2 evening / 3 night)
and the pinnable `nowHour` var (tests depend on pinning it — keep that
pattern).

**Scene palette (fxCalm+):** one package-level table of 4 pre-built style
sets — `var scenePalettes = [4]scenePalette{...}` — built once at package
load (RAM rule: no per-frame style construction):

| Bucket | Canopy | Trunk/soil | Accent |
|---|---|---|---|
| morning | `#76b053` | cMoss | dew `·` dots (cSky) sparse on canopy row, `hash(x)%7==0` |
| day | cCanopy `#87c95f` | cMoss | `☼` (cAmber) top-right sky |
| evening | `#a8b454` olive-gold | `#6b5d3a` | `☼` low-left sky (cAmber) |
| night | `#3f5d3a` dim moss | cFog | `☾` (cSky) top-right + 2 fireflies `✦` (cAmber), `fireflyPos(tick, span)` and `fireflyPos(tick+7, span)` |

**Chrome tint (fxFull only):** 4 precomputed variants of the panel border
color and the `⁂ GROVE` title tint, selected in View():
morning cMoss / day cMoss (unchanged) / evening `#8a7a4a` / night `#3a4a6a`.
Data styles (status colors, text) NEVER shift — only borders + title. At
fx<full, chrome uses today's fixed styles.

### S4 — The Almanac (`g`)

One garden per day, from full history. New file `internal/tui/almanac.go`
(+ test).

- **Mode:** append `modeAlmanac` to the iota block (`tui.go:26`). Wire into
  `View()` and `handleKey` dispatch like modeCosts.
- **Data (off the tea loop, on mode entry only):**

  ```go
  type almanacDay struct {
      date     time.Time // LOCAL midnight
      shipped  []string  // tickets of EvTaskDone that day, in order
      planted  int       // EvTaskCreated
      answered int       // EvAnswered
      questions int      // EvAgentStatus with sentinel=question + EvNotification
      parked   int       // EvWorkspaceParked
  }
  type almanacMsg struct{ days []almanacDay } // oldest → newest, contiguous
  ```

  `almanacCmd(stateDir)` reads `state.ReadEvents(stateDir, 0)` (limit 0 =
  full file), buckets **by `ev.Time.Local()` calendar day** and builds a
  contiguous range from the earliest event's day through today (empty days
  included — h/l walks the calendar), clamped to the most recent 365 days.
  **LOCAL time is non-negotiable: grove-51 shipped a UTC-bucketing bug in
  the spend chart that made evening work look untracked. Do not repeat it.**
- **Keys:** in modeList, `g` → `m.mode = modeAlmanac`, `m.almSel = last`
  (today), fire `almanacCmd`, flash `"leafing through the almanac…"` until
  the msg lands. In modeAlmanac: `h`/`left` older, `l`/`right` newer
  (clamped, no wrap), `g`/`esc` back to list, `q`/`ctrl+c` quit.
- **View:** header + focused panel titled
  `THE ALMANAC ── <weekday> · <yyyy-mm-dd>` (+ ` · today` on the newest
  day) containing: a garden — one `♠` tree per shipped ticket, same plot
  primitives as S1 (canopy/trunk/soil/label rows; condense past the width
  exactly like S1) — or, for a day with nothing shipped, the soil line and
  a dim `a quiet day in the grove` (plus fireflies if that day is browsed at
  night — keep it simple: fireflies key off nowHour, not the browsed day);
  then a stat line `N shipped · N planted · N answered · N questions`
  (omit zero categories; parked days append `· parked`). Trim to
  `m.height-4` rows behind a `…` hint (help.go pattern). Own footer:
  `h/← older · l/→ newer · esc back · q quit`.
- **Refresh:** almanacMsg data is a snapshot; do NOT re-read on the 1s tick
  (the refreshMsg handler must not fire almanacCmd — unlike costs). Reopening
  the mode re-reads.
- **Legend/help:** add `{"g", "gardens"}` to `globalHints` (footer.go, after
  `{"*", "effects"}`) and
  `{"g", "gardens: the almanac — one garden per day, h/l walk days"}` to
  `helpGlobal` (help.go). **footer_test.go pins wrap layouts — expectations
  will shift; update them deliberately, don't fudge widths.**

## Hard constraints (violating any = do not merge)

1. **fx=off is byte-identical to today.** No scene, no palette shift, no
   cast, activity sizing exactly as before. `TestOffIsStatic` must pass
   unmodified in spirit — extend it to also assert no scene lines appear.
2. **RAM rule** (cockpit-ram-reserved-for-workers): no new goroutines, no
   new timers (the 1s `tickMsg` is the only beat; `almanacCmd` is a one-shot
   tea.Cmd, that's fine), no per-frame style/table construction — every
   palette, glyph table, and orbit table is package-level, built at load.
   The only new Model state: `focused string`, `almSel int`,
   `almanac almanacMsg`, plus `"w"`-prefixed entries in the existing capped
   celebrations map.
3. **Height discipline:** total render ≤ m.height in every mode/tier —
   otherwise the alt-screen soft-scrolls and desyncs (grove-60 #53a). Test it.
4. **Width discipline:** every emitted line `truncPad`-ed; pad-then-style
   (ANSI inside `%-Ns` breaks fmt width math — noted at view.go:123).
5. **Feed strings untouched** (`feed_test.go` asserts them; J4 pattern:
   presentation only).
6. **events.jsonl is read-only here.** The scene and almanac render the log;
   they never Append (except nothing — no new events at all in this work).
7. **tmux: read-only.** `ActiveWindow` uses list-windows only. Never
   send-keys/kill/select from render or refresh paths. Load the
   tmux-discipline skill before touching internal/tmux.
8. **No glyph substitutions.** The vocabulary table is operator-verified.
   `⸙` and CJK-radical/Supplemental-Punctuation runes are banned.
9. **Scene renders only in modeList.** Detail, costs, help, profile-pick,
   confirm footers: unchanged.

## Test plan (all new logic TDD'd — decision logic in tested packages)

- `scene_test.go`: height-budget split (busy/quiet/short/tall), tier
  selection, plot overflow ladder, plant stages per label+age, label
  derivation (`grove-63`→`#63`, no-digit fallback), marker priority,
  walk-off offsets, queen placement from focused, fairy recency window,
  determinism (same inputs ⇒ same string), every line ≤ width, total ≤
  height, fxOff ⇒ empty.
- `joy2_test.go`: J3 expectations updated to `♠`; new header-clamp test.
- `view_test.go`: activity/scene row split; fx=off byte-identity extended.
- `almanac_test.go`: local-day bucketing (pin a UTC+2-style boundary case:
  an event at 23:30 local lands on ITS local day), contiguous range, empty
  days, nav clamping, stat-line composition, height trim.
- `footer_test.go` / `help_test.go`: `g` entry, updated wraps.
- `internal/tmux`: `ActiveWindow` parse test on canned list-windows output
  (no live server needed — parse function split from the exec).

Gate: `go build ./... && go vet ./... && go test ./...` green, `gofmt -l .`
empty, `e2e/dummy.sh` pass (task lifecycle untouched, but run it — the
shipping-gates skill explains why; run the gate as plain commands, never
piped through head/tail).

## Commit plan (one branch, worktree `~/git/.worktrees/grove/<slug>`, ff-only merge)

1. `S0` repairs: `♠` swap + header clamp (+ tests).
2. `S1` scene core behind fxCalm (+ height-budget refactor + tests).
3. `S2` cast: tmux.ActiveWindow, focused plumbing, pawns/queen/fairy (+ tests).
4. `S3` day-cycle palettes + fxFull chrome tint (+ tests).
5. `S4` almanac mode + legend/help entries (+ tests).

Each commit passes the full gate on its own. Update TASKS.md on ship;
anything surprising goes to LEARNINGS.md (and the matching skill if it
generalizes).

## Handoff

Operator testing is by **throwaway build**: `go build -o /tmp/gv-<ticket>
./cmd/gv` and hand over that path. **Never `go install` from the branch** —
the installed gv and live fleet sessions must stay untouched.
