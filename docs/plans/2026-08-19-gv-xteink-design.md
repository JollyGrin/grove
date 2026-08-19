# gv-xteink — grove fleet board on the Xteink X4 Pro

**Status: DRAFT — design in progress (2026-08-19).** Not yet through
design-reviewer. Companion to the plugin contract (docs/plugins.md, issue
#75) and the unfinished gv-remarkable surface (#76).

## What this is

A grove surface plugin that turns an Xteink X4 Pro (4.3" e-ink pocket
reader, ESP32-C3, WiFi, opt-in touchscreen, CrossPoint firmware) into a
glanceable fleet monitor with confirm-only steering. Dean checks worker
state and dispatches pre-drafted answers from the couch; no laptop, no
terminal, no typing.

**Grove is not modified.** This is a standard sidecar plugin per
docs/plugins.md: it reads `--json` output, tails `events.jsonl`, and
mutates only by shelling out to `gv`. The device-side code is a thin fork
of the CrossPoint Reader firmware (upstream has no app/plugin mechanism),
kept deliberately tiny so AI-driven rebases stay cheap.

## Prior art / inputs

- **gv-remarkable (#76)** — same one-repo-per-surface pattern; never
  finished. This design supersedes its UX thinking for e-ink.
- **crosspoint-reader-x3 fork** (smalltomatowater-boop) — bridges raw
  tmux panes to the device over BLE keyboard + host Python server.
  Anti-pattern we learn from: terminal-on-e-ink UX, BLE/WiFi coexistence
  problems, ~885 commits diverged from upstream = untrackable. Lesson:
  **no product logic in firmware, no BLE, minimal diff.**
- **CrossPoint Reader** (upstream) — PlatformIO/Arduino on ESP32-C3
  (~380KB usable RAM), FreeInkUI activity system for screens, WiFi
  STA/AP with a real HTTP stack, OTA updates. Xteink is an official
  partner; X4 Pro supported since Beta 4 (Aug 2026), units ship
  unlocked.

## Hardware constraints that shape everything

| Constraint | Consequence |
|---|---|
| ~380KB RAM, no heap to spare | Device never parses grove state; it renders pre-baked screens |
| E-ink refresh (slow, ghosting) | Partial refresh for row updates, periodic full refresh; no scrolling text, no live logs |
| No keyboard; touch is opt-in | Confirm-only actions — every button is a pre-drafted yes/no |
| Battery (pocket reader first) | On-demand refresh while activity open; slow ambient cadence only when docked/charging |
| WiFi STA on home LAN / Tailscale subnet | Host sidecar is the only thing the device ever talks to |

## UX

Three screens. E-ink's strengths are persistence and glanceability; its
weaknesses are refresh and text entry. So: never show conversation,
never accept typing.

### 1. Fleet board (default)

One row per task, needs-you sorted first. Data is `gv ls --json` almost
verbatim; glyph language borrowed from the living-grove scene (grove-63).

```
┌────────────────────────────────┐
│ ⸙ grove          14:32  ▂▂▄▆█ │
│ 2 need you · 3 working · 1 ✓  │
├────────────────────────────────┤
│ ? grove-141  needs answer  12m │
│   "should retry use backoff    │
│    or fixed interval?"         │
│   ▸ draft ready — tap to view  │
├────────────────────────────────┤
│ ⚑ grove-138  PR open, CI ✓  4m │
│   ready for review             │
├────────────────────────────────┤
│ ♦ grove-152  working       38m │
│   turn 41 · implementing tests │
│ ♦ grove-149  working        6m │
│   turn 12 · exploring repo     │
│ ⏸ grove-133  paused         2d │
│ ✓ grove-140  merged         1h │
├────────────────────────────────┤
│ [refresh]   [sweep]   [zzz]    │
└────────────────────────────────┘
```

Glyphs: `?` question (needs Dean), `⚑` review-ready, `♦` working,
`⏸` paused (bookmark — never suggested for cleanup), `✓` merged/done.
Header sparkline: fleet activity over the last hour (from events.jsonl
event density). `zzz` enters ambient mode.

### 2. Task detail (tap a row)

```
┌────────────────────────────────┐
│ ? grove-141        waiting 12m │
├────────────────────────────────┤
│ Q: "should retry use backoff   │
│ or fixed interval? the spec    │
│ says 'retry sensibly' only"    │
│                                │
│ branch: grove-141-retry-fix    │
│ diff: +84 −12 · 3 files        │
│ PR: none yet · turns: 23       │
├────────────────────────────────┤
│ DRAFTED ANSWER (host):         │
│ "Exponential backoff, cap at   │
│ 30s, 5 attempts — match the    │
│ pattern in internal/relay."    │
├────────────────────────────────┤
│ [✓ send draft]  [skip]  [back] │
└────────────────────────────────┘
```

`✓ send draft` → device posts the action token → host runs
`gv answer grove-141 "..."`. The draft is produced host-side (rules or
an orchestrator model call); the device is purely a disposal surface.
This makes propose-then-dispose the literal interaction model — a
tapped confirm on the surface counts as human confirmation per the
plugin guardrails.

### 3. Ambient mode (docked/charging)

Full-screen fleet poster, updates every ~5 min, zero interaction. E-ink
holds the image at no power cost between updates. Living-grove scene as
the visual: each worker a plant, state as growth/wilt. Exit on any
button.

### Actions (exhaustive list, all confirm-only)

- send drafted answer (`gv answer`)
- send canned nudge — "continue", "commit + status", checkpoint template
  (`gv nudge`)
- approve a single sweep row (`gv sweep` equivalent per-item, host-side)
- refresh

Explicit non-actions: no free text, no `gv grab` (dispatch needs ticket
judgment — stays in chat with the orchestrator), no terminal, no ticket
backend mutation ever.

## Architecture

Three layers; grove knowledge decreases to zero as you move right.

```
grove state                 gv-xteink host sidecar            X4 Pro firmware
(untouched)                 (own repo, any language)          (CrossPoint fork)

gv ls --json  ──poll──▸  aggregate across workspaces  ──HTTP──▸  GroveActivity:
events.jsonl  ──tail──▸  draft answers                screens     fetch screen JSON
gv workspaces --json     bake SCREEN json             ◂──────     render via FreeInkUI
gv answer/nudge ◂─shell─ resolve action tokens        actions     post tapped tokens
```

### Layer 1 — host sidecar (`gv-xteink` repo)

Standard plugin per docs/plugins.md: polls
`gv ls --json --no-pr --no-cost` (30–60s), tails events.jsonl for
wake-ups, `gv workspaces --json` for multi-grove. Adds PR/diff columns
only when a detail screen needs them. Drafts answers. Serves a tiny
HTTP API on the tailnet. All product logic lives here; iterating on UX
= redeploy the sidecar, never reflash the device.

### Layer 2 — screen protocol (server-driven UI)

The API serves fully-baked screens, not grove data:

```json
{ "v": 1,
  "screen": "board",
  "full_refresh": false,
  "header": {"title": "⸙ grove", "line": "2 need you · 3 working · 1 ✓"},
  "rows": [
    {"glyph": "?", "title": "grove-141  needs answer  12m",
     "lines": ["\"should retry use backoff…\""],
     "goto": "detail:grove-141"}
  ],
  "actions": [
    {"label": "✓ send draft", "token": "ans-grove-141-8f3a", "confirm": true}
  ]
}
```

Device posts `{"tapped": "ans-grove-141-8f3a"}`; host resolves the
token to a concrete `gv` command. Tokens are single-use and expire —
a stale board can never fire a mis-aimed answer. The firmware never
knows what a ticket, PR, or worker is.

Protocol is additive-only, mirroring grove's own versioning rule: the
device ignores unknown keys; a breaking change bumps `v`.

### Layer 3 — firmware (CrossPoint fork, surgically thin)

One self-contained `GroveActivity` on the FreeInkUI activity base
(fetch → render rows → post input; target ~300 lines in its own
directory) plus a one-line home-menu registration. Config (host URL)
via a file on the SD card next to CrossPoint's existing `.crosspoint/`
cache — no settings-UI surgery.

**This fork is of CrossPoint, not grove.** Grove has a plugin system;
CrossPoint doesn't, so the fork *is* the plugin shim on that side.

## Development sandbox (no device required)

Dean's X4 Pro hasn't arrived; nothing blocks on it. Each layer has a
hardware-free dev loop, and they compose end to end:

1. **Sidecar** — pure grove-plugin work: dummy-data pattern (scratch
   `HOME`, `GROVE_STATE_DIR` override, repo `claude:` stubbed to
   `echo`), copy `e2e/plugin.sh`. Seed fake fleets, assert baked
   screens. No device concepts anywhere.
2. **Screen protocol** — a throwaway local renderer (single HTML page
   drawing screen JSON at 480×800, 1-bit) as the first "device". The
   protocol's JSON fixtures live in gv-xteink and double as conformance
   tests for both sides.
3. **Firmware** — the **official
   [crosspoint-simulator](https://github.com/crosspoint-reader/crosspoint-simulator)**:
   compiles the firmware natively and renders the e-ink display in an
   SDL2 window (macOS supported; `pio run -e simulator -t run_simulator`
   in forks like CrossMux). GroveActivity develops against
   `localhost` sidecar in the simulator — full pipeline (sidecar →
   protocol → real activity render) with zero flashes.
   *Spike:* verify the simulator shims the ESP HTTP client (its deps
   include curl, suggesting yes); if not, put the fetch behind a
   two-line interface with a native curl implementation.
4. **Hardware** — only the last mile: real refresh behavior, ghosting,
   touch feel, battery. By the time the device arrives, everything
   above it is already tested.

## Flashing & recovery (bricking safety)

Short version: the X4 Pro is about as unbrickable as embedded devices
get, and factory restore is a first-class supported flow.

- **Why hard-bricking is ~impossible:** the ESP32-C3's first-stage
  bootloader lives in mask ROM — it cannot be overwritten by any flash
  write. As long as USB download mode is reachable (it's entered via
  the boot strap pin, not firmware), the device can always be
  re-flashed with esptool or the web flasher, no matter how broken the
  flash contents are. Recovery risk is hardware damage, not bad code.
- **Day-0 golden image (the one non-negotiable step):** before flashing
  anything, take a **full flash backup** — the official web flasher at
  crosspointreader.com/#flash-tools offers this in-browser and produces
  a 16 MB `.bin` that is a byte-exact copy of the factory state
  (equivalently: `esptool read_flash 0 ALL factory-x4pro.bin`). Store
  it in two places. Factory restore = the flasher's "Write full flash
  from file" with that image — exact prior state, including stock
  firmware and activation.
- **Stock-firmware fallback:** independent of the golden image,
  official CrossPoint releases (and Xteink stock, on units bought from
  the official site — they ship unlocked) are flashable from the same
  web tool or via esptool with the merged release binary. So there are
  two restore paths: byte-exact personal backup, and clean official
  images.
- **Dev loop flashing:** day-to-day iteration is `pio run -t upload` —
  it rewrites only the app partition; bootloader and partition table
  are rarely touched. Books/config live on the SD card, untouched by
  flash cycles. CrossPoint's OTA (GitHub releases) covers plain
  upstream updates, not dev builds.
- **X4 Pro-specific checks before first flash:** confirm the web
  flasher lists the X4 Pro (touch) variant, and verify the actual flash
  size with `esptool flash_id` before trusting the 16 MB assumption —
  the Pro is new hardware and guides above were written for X3/X4.
- gv-xteink's README carries the recovery runbook: golden-image
  location, restore steps, download-mode button combo.

## Staying current with upstream CrossPoint

Because the firmware is a dumb renderer behind a stable protocol, it
almost never needs to change — so rebases are usually clean.

- Fork keeps a `grove` branch = `upstream/master` + tiny patch series.
- Upstream release → **rote grove ticket**: "rebase gv-xteink firmware
  onto upstream vX; gate: `pio run -e <x4pro-env>` green" — labeled
  `rote`, sonnet-routed. Grove workers maintain grove's own surface.
- CI: PlatformIO build on the firmware repo; dummy-data smoke test on
  the host repo (copy `e2e/plugin.sh` pattern).

## Repo layout

- `gv-xteink` — host sidecar + screen protocol spec + smoke test.
  Tagged `grove-plugin`. The plugin per the contract.
- `crosspoint-reader` fork (`grove` branch) — GroveActivity patch
  series. Referenced from gv-xteink's README with a pinned release.

Two repos because they have different upstreams and different release
cadences; gv-xteink pins a firmware tag.

## MVP scoping (leaning: ship 1 first)

1. **Read-only board** — screens 1 + 3, no actions. Pure `gv ls`
   mirror; zero guardrail risk; proves the whole pipeline (sidecar →
   protocol → firmware render) end to end.
2. **Confirm loop** — action tokens, drafted answers, canned nudges.
3. **Polish** — sparkline, living-grove ambient scene, sweep approval.

## Open questions (design still in progress)

- Draft-answer generation: rules-only at first (canned templates keyed
  on question shape), or a model call in the sidecar from day one?
- Poll vs push to device: plain polling while activity open is fine;
  is ambient mode's 5-min WiFi wake acceptable for battery, or should
  ambient exist only while charging?
- Multi-workspace UX on one small screen: merged board with workspace
  prefix, or a workspace switcher screen?
- ntfy integration: sidecar already sees QUESTION/BLOCKED/DONE via
  events; does the device benefit from ntfy at all, or is that purely
  the phone's channel?
- Firmware config/pairing UX: SD-card file vs a tiny QR/AP flow
  (CrossPoint already ships QR helpers for WiFi).
- Does the X4 Pro CrossPoint build expose partial-refresh control to
  activities, or is refresh policy global? (Determines how live the
  board can feel; needs a spike on real hardware.)
- Does crosspoint-simulator shim the ESP HTTP client so GroveActivity
  can hit a localhost sidecar? (Spike; fallback is a small fetch
  interface with a native curl impl.)
- Which simulator base: official crosspoint-simulator vs the CrossMux
  fork's in-tree `-e simulator` env — whichever tracks upstream with
  less glue.
