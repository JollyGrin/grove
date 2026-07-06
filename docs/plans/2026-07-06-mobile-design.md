# Mobile cockpit design — `gv mobile` v2

**Status:** design-reviewed 2026-07-06 (all blocker/major findings resolved below)
**Decision (Dean, 2026-07-06):** the current `gv mobile` is unused — this
is a full replacement, no back-compat constraints on the old behavior.
**Goal:** manage grove swarms from a phone (Termius over Tailscale) —
navigate repos/workspaces, browse backlogs, grab tickets, answer agent
questions, submit new tickets — with a touch keyboard that makes every
typed character expensive.

## The environment we're designing for

Dean's flow today: Tailscale on the phone → Termius (full SSH client) →
`tmux a` into desk sessions. Verified constraints of that stack:

1. **Width.** Portrait phone ≈ 45–55 columns. The dashboard's agents
   table is ~90 cols (TICKET ≤33 + REPO 11 + STATUS 11 + LIVE 8 + PR 8 +
   CI 4 + PREVIEW 9 + AGE — `internal/tui/view.go:77-78`), so `truncPad`
   silently chops PR/CI/PREVIEW/AGE off — the columns you'd most want on
   the go.
2. **Typing is expensive; single keys are cheap.** The TUI's one-key
   bindings (`j/k/enter/n/d/q`) are ideal; commands with flags
   (`gv grab 123 --repo unbrewed`) are the pain. Termius's key bar gives
   Esc/Ctrl/arrows but chorded tmux prefixes are miserable. iOS dictation
   into a text field works well — free-text *replies* are fine, free-text
   *commands* are not.
3. **Connections drop.** iOS suspends backgrounded apps; tmux already
   solves re-entry. Termius supports a per-host **startup snippet** (run a
   command on connect) and **Mosh** for roaming — so the target UX is:
   open Termius → tap host → you're looking at the dashboard, zero typing.
4. **tmux sizes a session to its smallest attached client.** So the phone
   gets its own single-pane session (`grove-mobile`) and must **never
   `switch-client` onto a session the desk is attached to** — not via the
   switcher, not via `a` attach.
5. **Reading long agent output on a phone is bad.** The monitoring surface
   should be the *ticket thread* (GitHub mobile app) plus ntfy push —
   not tmux scrollback. The terminal is for steering, not reading.

## What exists and what's being replaced

- `gv mobile` (`cmd/gv/main.go:464-476`) creates `grove-mobile` (single
  pane, rooted at `$HOME`) running bare `gv`. On a workspace machine
  bare `gv` lands in the switcher, and picking a workspace
  `switch-client`s the phone onto the desktop cockpit — the exact shrink
  trap the session exists to avoid. Unused in practice; replaced wholesale.
- The dashboard TUI (`internal/tui/`) is `WindowSizeMsg`-aware but has a
  single desktop-tuned layout, and its action callbacks close over
  cmd/gv globals (`stateDir()`, `ambient`) resolved once at startup
  (`main.go:77-102`) — a constraint the new design must break (see
  Scope refactor).
- No backlog/grab surface exists in the TUI; grabbing is CLI-only.
- Kickoff verbs for GitHub (`internal/provider/github.go:169-178`) say:
  one "I've started" comment, PR at review. Too quiet for phone
  monitoring.

## Design

### 0. Prerequisite: the Scope refactor (de-globalize the TUI's world)

Everything below hangs off one structural change: the TUI must operate
on an explicit **scope** — `{label, root, cfg, stateDir}` — instead of
cmd/gv package globals.

- A `Scope` value is built at TUI start and rebuilt on workspace switch
  (from `workspace.LoadRegistry` + `config.LoadAt(root)` +
  `config.StateDirAt(root)`).
- Every injected callback (`FinishTask`, `AttachTask`, relay/nudge,
  PR refresh) takes the scope (or is a method on it) — no more
  `stateDir()` closures. Today `finishTask` (`main.go:1828`) and
  `attachTask` (`main.go:1472`) append events via the startup-ambient
  `stateDir()`; after an in-TUI switch they would write `task_done` /
  `attached` into the *wrong* workspace's `events.jsonl`, where the fold
  drops them as unknown tickets (`internal/state/state.go:159`).
- **Refresh epoch guard:** every scheduled `refreshMsg`/`prsMsg` carries
  an epoch int; the model increments its epoch on switch and drops
  messages from older epochs. Without this, in-flight ticks resurrect
  the old workspace's rows *and* duplicate the 1s tick chain
  (`internal/tui/tui.go:173,179-181`). TDD this.
- **Subprocess contract:** every subprocess the TUI spawns (`gv grab`,
  `gh issue create`) runs with `cmd.Dir` set from the scope (workspace
  root / repo path) — never inherits the TUI's cwd. A grab child
  inheriting `$HOME` would resolve ambient to the global state dir and
  the task would vanish from every cockpit.

This is the enabling refactor for §3 (switch), §4 (grab), §6 (compose).

### 1. Bootstrap: `gv mobile [label]`

`cmdMobile` (replaced):

1. **Stale-session migration:** if `grove-mobile` exists but pane 0's
   running command isn't `<absolute-exe> dash --mobile …`, kill the
   session and rebuild (it's grove's own session; no hard rule against
   it). Pane-command inspection is a new helper in
   `internal/tmux/grove.go` (keeps `tmux.go` byte-comparable per
   docs/seed-manifest.md).
2. Create `grove-mobile` with pane 0 running
   `<absolute-exe> dash --mobile [--workspace <label>]` (same stale-PATH
   defense as `buildCockpit`, `main.go:352-358`), rooted at the
   workspace root when a label is given, `$HOME` otherwise (cwd is
   belt-and-braces only — the scope contract above is what carries).
3. Attach.

**Startup scope resolution in `dash --mobile`** (no global config
required — the registry is the source of truth):

- `--workspace <label>` → that scope.
- else registry has exactly one alive workspace → it.
- else registry non-empty → boot directly into the workspace overlay
  (§3) as the first screen.
- else (no registry) → legacy global scope via `config.Load()`, same as
  today's `gv dash`.

`dash` dispatch grows args (`cmdDashboard(args)` — today it takes none,
`main.go:234-235`).

### 2. Narrow layout in the dashboard TUI

When `m.width < 70` **or** mobile mode is forced, `viewAgents` renders
two-line cards instead of the table:

```
▸● unbrewed-142        WORKING  2h04m
   unbrewed  PR#88 ✓   ⬡ up
 ◆ waterhouse-7        WAITING  12m
   waterhouse  needs: pick storage engine…
```

Same model, same keybinds, view-layer only (`view.go`). The activity
feed clamps its ticket column to ~14 in narrow mode (at full
`ticketColWidth()` 33+7 for age, a 45-col screen leaves ~2 cols of
message text — `view.go:59-70,145-152`). Bonus: desktops with a skinny
dashboard pane get the same fallback. TDD the card renderer.

### 3. Workspace switching inside the TUI (`w`)

`w` → overlay list of registered workspaces with rollups (reuses
`workspace.LoadRegistry` + `ReadRollup`, same data as `cmdSwitch`).
Selecting one rebuilds the Scope and reloads — **no tmux session
change, ever**. Rendered with the TUI's own list/`textinput` primitives
(no `huh.Form.Run()` inside a live tea program — huh starts its own
program; the embedded-bubble pattern or plain lists only).

### 4. Backlog browse + one-key grab (`g`)

`g` → backlog view:

- **Async fan-out:** one `tea.Cmd` per configured repo calling
  `prov.List()`, gated on `Capabilities().CanList` (Linear returns
  false — those repos are skipped with a visible "linear: no backlog
  listing" row, not silently). Loading spinner per repo; `gh` calls are
  15s-capped (`github.go:40-41`) and must never run synchronously in
  `Update`.
- Grouped by repo, `j/k` + `enter` to grab, `/` filters by number/text
  (one short dictated token covers "ntfy said #142").
- **Grab execution:** a `tea.Cmd` runs `<exe> grab <id> --repo <r>
  --chatty` with `cmd.Dir = scope.Root`, capturing combined output +
  exit code. Success → flash + the task appears via the normal
  `EvTaskCreated` fold (grab emits it at the end, `main.go:671-680`).
  **Failure → flash the last stderr line and keep a `!` row in the
  backlog view until dismissed** — grab emits no event on failure, so
  the TUI owns failure surfacing (it appends nothing itself; events
  stay grab-owned). During the 5–30s setup window the backlog row shows
  a "planting…" state driven by the running-Cmd handle, not by events.

### 5. Chatty tickets: `--chatty` grab flag + provider-level verbs

Mobile-initiated grabs default to chatty; desk grabs opt in with
`gv grab ... --chatty`. Implementation stays inside the existing hard
rule (binary never transitions tasks; agents do), and the seam is
**provider Verbs, not the kickoff template**:

- `provider.Verbs` gains a chatty variant for **github only** (e.g.
  `VerbsChatty()` or a suffix composed at the grab call site): post a
  short issue comment (1–3 lines) at each milestone — plan settled,
  implementation done, tests green, PR opened — and **when raising a
  QUESTION/BLOCKED sentinel, mirror the question as an issue comment**
  so the thread shows why it's stalled. Never close the issue.
- Why not a template conditional: `md_default.tmpl` is shared by
  markdown AND github kinds (`kickoff.go:72-76`) — gh instructions
  would leak into markdown repos, and repo-level `prompt:` overrides
  replace the template wholesale (`kickoff.go:77-83`), silently
  no-op'ing chatty. Verbs are already per-provider and flow into every
  template via `{{.Verbs.Start}}`/`{{.Verbs.Review}}`.
- `--chatty` on a non-github repo: warn and proceed un-chatty.
- Linear keeps its "no comments" stance untouched.
- Manifest: kickoff/provider are already recorded divergences; add rows
  for the new surface (bookkeeping).

Monitoring loop this buys: grab from phone → watch the issue thread in
the GitHub app → ntfy pings on QUESTION/BLOCKED/DONE (already wired,
`internal/hooks/hooks.go:164-194`) → hop back into Termius only to
answer (`enter` → dictate → send).

### 6. Submitting new tickets from the phone (`c`)

`c` in backlog view → compose form (two `textinput`s: title, body —
both dictation-friendly; same embedded pattern as the detail-view
reply) → confirm screen showing the rendered issue → `y` runs
`gh issue create --title … --body …` with **`cmd.Dir = repo.Path`**
(no `-R` flag — config carries `Path`, not an owner/repo slug; this is
the same cwd-based resolution the github provider uses,
`github.go:39-51`) → offer "grab it now? y/n". Creating an issue is not
a terminal-state mutation; the confirm screen satisfies
propose-then-dispose. `gh` auth/network failure → flash stderr, form
content preserved so a retry doesn't retype.

### 7. Mobile-mode keybind diffs

The phone client must never leave `grove-mobile`, so in mobile mode:

- **`a` (attach) is suppressed** — inside tmux it `switch-client`s onto
  the shared `pr-<repo>` worker session (`tui.go:251-266`,
  `tmux/tmux.go:188-191`), shrinking the desk, with no un-chorded way
  back. Steering is covered by the detail view's pane tail + reply; in
  mobile mode the detail view's PANE section grows to fill the screen
  (`CapturePaneBottom` already exists, `tmux.go:108`).
- **`O`/`0` (spawn orchestrator) is suppressed** — it builds/mutates a
  desktop cockpit via startup ambient (`main.go:436-457`); from the
  phone that's an invisible side effect on the wrong session.
- Everything else unchanged: `j/k`, `enter` reply, `n` nudge, `o`/`p`/
  `t` (open URLs on the *Mac* — harmless), `v`, `d` done-confirm, `r`,
  `q`.
- **Idle throttle:** the detached mobile session would otherwise poll
  `state.Load` + `DetectLive` every 1s and gh every 30s forever; when
  `tmux list-clients -t grove-mobile` reports zero clients, stretch the
  tick to 30s (helper in `internal/tmux/grove.go`). Cheap, keeps a
  phone-less Mac quiet.

### 8. Termius/Tailscale recipe (docs only)

`docs/mobile.md`: host = Mac's MagicDNS name; startup snippet
`~/go/bin/gv mobile` (or SSH command `ssh mac -t 'gv mobile'`); enable
Mosh for Wi-Fi↔cell roaming (optional — tmux already covers
reconnect); key-bar essentials; note that answering QUESTION pushes is
the main loop (ntfy → Termius → `enter` → dictate → send).

## Out of scope (v1)

- **Answering via issue comments** (worker polls the thread for replies) —
  closes the loop entirely inside the GitHub app, but needs a polling
  design; revisit after chatty proves itself.
- A web/native UI. The terminal + GitHub app + ntfy triad covers it.
- Multi-pane mobile layouts. One pane, one TUI.
- Programmatic `aggressive-resize` — moot once `a` is suppressed.

## Resolved questions

1. Chatty default: **mobile grabs chatty by default; desk grabs opt in.**
   Revisit making it the global github default after it proves itself.
2. Grab-from-TUI foreground mode: **no** — flash + "planting…" row +
   failure row (§4) is the contract.
3. One global mobile session with in-TUI switching (not per-workspace
   sessions): **yes** — one phone, one session.
