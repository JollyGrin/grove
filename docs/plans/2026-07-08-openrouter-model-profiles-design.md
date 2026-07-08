# Model profiles — OpenRouter (and any Anthropic-compatible backend) in grove (design draft)

> **Status: design draft / ideation.** Written from an operator ask
> ("spice in GLM/other models to reduce load on my Claude sub"), a code map
> of the worker/orchestrator launch seam, and verified facts about Claude
> Code's `ANTHROPIC_BASE_URL` behavior + OpenRouter's Anthropic-compatible
> endpoint. Companion to [DESIGN.md](../../DESIGN.md) §7 (model routing) and
> the [multi-model-orchestration ideation](../multi-model-orchestration-ideation.md).
>
> **Rev 1 (2026-07-08):** design-review fixes folded in — `WithModel`-then-wrap
> ordering (§2.1), self-sourcing launch validated by experiment (§2.1, M4),
> ovs byte-comparability constraint dropped (obsolete), HTTP-precedent
> correction (§5), orchestrator both-limbs wrap (§3.1). Ready for plan stage.
>
> **Scope.** A **model profile** abstraction that lets grove open an
> orchestrator or grab a worker against a non-Anthropic, Anthropic-API-
> compatible backend (OpenRouter first; Z.ai-direct and others fall out for
> free). Plus the cost-page changes needed to keep the ledger legible when
> more than the Claude family shows up.
>
> **Non-goals.** Not a routing/cascade engine (DESIGN §7's `Router` /
> escalate-on-failed-gate is a separate, later thing). Not an eval harness
> (ideation §2/§3). Not a proxy — we use providers that speak the Anthropic
> Messages API natively. Claude on the operator's own sub stays the default
> everywhere; this is **strictly additive and second-class**.
>
> **Naming.** The abstraction is called a **model profile** throughout.
> *Doc-dialect note:* Claude Code's own docs call this whole feature area
> "LLM gateway connect," so "gateway" is the ecosystem term you'll meet in
> upstream docs — but a direct Z.ai/Anthropic endpoint isn't really a
> gateway, and grove already overloads both "backend" (task backends:
> markdown/linear/github) and "provider" (`provider:` config), so we
> deliberately avoid those two words for this model-side concept.

---

## 1. The thesis

grove's worker/orchestrator launch already bottoms out at **one shell
command string** — `repo.Claude` (`internal/config/config.go:19`), run as
`<cmd> "$(cat prompt)"` in a tmux pane (`cmd/gv/main.go:771-772`). grove
never assumes what that command *is*. So "use a different model" is, at the
crudest level, already a per-repo config edit.

The insight this design turns into a feature: **any endpoint that speaks the
Anthropic Messages API works behind the unmodified `claude` binary.**
OpenRouter added an "Anthropic Skin" (native `/v1/messages`, no proxy);
Z.ai/DeepSeek/Kimi ship the same. Because it's still the `claude` binary in
the pane, **every grove telemetry mechanism keeps working**:

- **Hooks** (`internal/hooks`, `~/.claude/settings.json` → `gv hook`) still
  fire — question/status/PR events flow, so `gv ls` fleet state is intact.
- **Transcript parsing** (`internal/transcript/session.go`) still finds the
  JSONL and its per-message model string.
- **Pane-scrape detection** (`internal/detect/live.go`) still matches — it's
  the same Claude Code TUI rendering.

The only thing that's *wrong* out of the box is **pricing** (grove would
apply Claude rates to a GLM run) — and grove already has the hook for that:
`Cost.Pricing map[string]cost.Rates` (`config.go:47`).

**Chosen path: OpenRouter, metered.** One key → any model, native Anthropic
endpoint, and real per-token cost. At GLM-5.2's ~$0.42/$1.32 per MTok it's a
cheap break-glass backend; a flat Z.ai plan only wins at heavy sustained
fleet volume, which is far off. The abstraction is provider-neutral, so
Z.ai-direct remains a one-line addition later.

---

## 2. The core abstraction — a model profile

A **model profile** is the bundle that makes a model reachable: endpoint +
credential-reference + which slug fills each Claude-Code model class. Because
`ANTHROPIC_BASE_URL` is **process-global** (it redirects the whole `claude`
process, not one model), the base URL, key, and slug must always travel
together — a bare `--model` flag can't express this, which is why the profile
is the unit, not the model.

```yaml
# ~/.config/grove/config.yaml   (global; secrets NOT stored here)
model_profiles:
  anthropic:            # the default — empty body = the operator's own sub
  openrouter-glm:
    base_url: https://openrouter.ai/api        # Skin appends /v1/messages
    auth_token_env: OPENROUTER_API_KEY         # env VAR NAME, never the key
    opus:   z-ai/glm-5.2
    sonnet: z-ai/glm-5.2
    haiku:  z-ai/glm-4.5-air
```

### 2.1 Secret handling (load-bearing)

- **Store the env-var *name*, not the key.** This mirrors grove's existing
  Linear convention (`Linear.APIKeyEnv` = `api_key_env`, `config.go:33`) — the
  secret lives in the environment or a gitignored file, config carries only
  the pointer. Getting the operator's OpenRouter key **out of the repo** and
  into `~/.config/grove/` (as an env var, not a committed value) is the
  urgent first step, independent of everything else here.
- **Inject via a per-command `env …` prefix on the `claude:` command
  string — never via `~/.claude/settings.json`'s `env` block or the shell
  profile.** Those are process-global and would silently reroute *all* the
  operator's Claude Code (daily-driver included) to OpenRouter. The prefix
  scopes the backend to exactly the one worker/orchestrator process. This is
  the same "don't globalize the base URL" rule, enforced structurally.

Resolved command (profile `p`, role default `d` = opus/sonnet/haiku). The
worker **self-sources the secrets file** so it never depends on the tmux
server's or the launching shell's environment:

```
<setup> && ( . ~/.config/grove/.env \
    && export ANTHROPIC_BASE_URL='<p.base_url>' \
              ANTHROPIC_AUTH_TOKEN="$<p.auth_token_env>" \
              ANTHROPIC_MODEL='<p.d>' \
    && exec <modeledClaudeCmd> "$(cat <prompt>)" )
```

Four constraints this form encodes, each from design review:

- **Order: `WithModel` first, env-wrap second.** `WithModel` inserts
  `--model` after the *first token* (`config.go:208-212`), so the wrap must
  apply to the *already-modeled* command — wrapping the raw string would put
  `--model` on `.`/`env`, not `claude`. Apply the wrap at the launch seam,
  after `WithModel`, around only the claude portion (not `gv run-setup`).
- **Self-sourcing, not inherited (validated).** An experiment in an isolated
  tmux server started *without* the key confirmed a fresh pane does **not**
  inherit an interactive-shell export (`NOSRC: EMPTY`), and that sourcing
  `~/.config/grove/.env` inside the pane fixes it (`SRC: PRESENT`), value
  never printed. The pane sources the file itself — no dependence on
  `~/.zshrc` having run or on the tmux server predating the export. The
  secrets-file path resolves from grove's config dir.
- **`export … ; exec`, not `env NAME=$VAR`.** Keeps the expanded token out of
  any process argv (it lives only in the shell/child env); the subshell +
  `exec` leaves no leftover env in the pane shell. The command sent via
  `send-keys` contains only the literal `$NAME`, so `tmux capture-pane` /
  `gv ls` never show the value.
- **Quote interpolated fields (S1).** `base_url` and the slug go through
  `shellQuote` (as `WithModel` already quotes the model, `config.go:220`); the
  token stays a bare `$NAME` so it expands from the sourced file.

**Invariant: never persist the env wrap into `repo.Claude`.** Hooks resolve
the worker's config dir from `fields[0]` of the *stored* `r.Claude`
(`hooks.go:246-274`); apply the wrap only at the launch seam, never write it
back to config, or hook resolution to `~/.claude` breaks.

### 2.2 Layering (project defaults, override anytime)

grove already layers config (global → per-repo `provider:` override →
per-workspace `.grove/config.yaml`). Model profiles ride the same rails:

- **Global** `model_profiles` list (above) — the menu.
- **Per-repo default**: an optional `model_profile:` on the `Repo` struct,
  mirroring the `ProviderKindFor` precedent (`config.go:228`). Empty =
  `anthropic`. This is "a project can have defaults it likes."
- **Per-invocation override**: `--profile <name>` on grab / the `)` selector.
  "Use anything out of the box" — default unless explicitly asked.

---

## 3. UX

### 3.1 Orchestrator — `)` opens a profile selector; `0` unchanged

- **`0`** stays exactly as today: open a new orchestrator on the `anthropic`
  profile (the operator's sub). Zero friction, muscle memory preserved.
- **`)` (Shift-0)** opens a small overlay: pick a model profile → spawn a new
  orchestrator with that profile's env wrapping the launch. The wrap must
  cover the **whole `orchestratorCmd`** (`main.go:424-458`) — including *both*
  limbs of its `--continue 2>/dev/null || …` retry — or a `--continue`
  failure relaunches on the wrong (Anthropic) backend.
  - **MVP:** the overlay lists configured profiles + a **`+ add model`**
    entry. Frequency-pinning ("most-used to top") is post-MVP — it needs a
    persisted usage counter; defer it.
  - Because base URL is per-process, `)` always spawns a **new** orchestrator
    — you cannot hot-swap a running one's model.

**RAM guardrail** ([cockpit-ram-reserved-for-workers]): the selector is
event-driven — opens on keypress, reads config once, closes. No goroutine,
no poll, no cache, no per-frame allocation (build the list once on open). The
glyph *palette* is a package-level table; only slug→glyph *assignments* are
persisted state (see §4.3). This stays inside the guardrail.

**Second-class by design.** The orchestrator is the *demanding* role — heavy
MCP/tool use, long context, sustained reasoning — which is exactly where a
non-Anthropic model's rough edges show most. GLM-5.2 is explicitly strong at
tool use, so it's a safe pick, but the operator's stated 99%-Claude posture
is the right calibration. Keep `)` a power-user door, `0` the front door.

### 3.2 `+ add model` — catalog fuzzy-find **or** raw slug

Two paths, and the raw-slug path is the resilience escape hatch:

1. **Fuzzy-find** over OpenRouter's model catalog (`/api/v1/models`, cached —
   see §5). Type-to-filter across ~300 models.
2. **Paste an exact slug** directly (`z-ai/glm-5.2`). Bypasses the catalog
   entirely, so a stale/offline cache or a brand-new model (GLM-5.2 was
   exactly this case) never blocks you.

Adding a model writes/extends a profile (or offers to map it into the
opus/sonnet/haiku slots of a new one).

### 3.3 Workers — `gv grab --profile`

- New `--profile <name>` flag on grab. Absent → the repo's `model_profile:`
  default → `anthropic`. "Keep existing as default unless specifically asking
  for an OpenRouter model."
- Note this is a **profile** flag, not the existing `--model` flag: `--model`
  only injects `--model X` into the current command and can't change the base
  URL/key. `--model` may stay as a within-profile slug override; `--profile`
  is the backend switch.

---

## 4. Cost page

The meatiest track, and it splits cleanly into "accuracy" and "legibility."

### 4.1 Accuracy — pricing table now, real-cost API later

- **MVP (reuse the ledger):** add pricing rows to `Cost.Pricing`
  (`config.go:47`) — `z-ai/glm-5.2: {input: 0.42, output: 1.32}`. Token
  counts already flow through the transcript, so the existing estimate ledger
  becomes accurate with one config line per model. No new integration.
  **Key on the exact model string the transcript records** (`rateFor` looks up
  `Cost.Pricing[model]`, `cost.go:67-73`) — confirming *recorded string ==
  configured slug* is a P1 acceptance check. Safety net: an unknown model
  degrades to `CostKnown:false` ("cost unknown", `cost.go:206-209`), never
  silently-wrong.
- **Later (billing-grade):** OpenRouter's true per-request cost lives at its
  `/api/v1/generation` endpoint (needs the generation ID per response, which
  the transcript doesn't carry) or the aggregate credits/usage API. That's a
  new outbound integration — worth it only if estimate accuracy proves
  insufficient. Not MVP.

### 4.2 Estimate vs actual is apples/oranges — mark it

The operator's Claude usage is a **flat subscription**; grove's own rule is
that its cost numbers are "ESTIMATES of relative effort, never billing."
OpenRouter is **real metered dollars**. Rendering both as one number
conflates them. Mark OpenRouter rows as actual `$` (or otherwise distinguish
metered from effort-estimate) so a $2 GLM run isn't misread against a Claude
"effort" figure.

### 4.3 Model glyphs — a persisted registry, not a pure hash

Today the cost page labels models with a single char via `MixCompact` /
`ShortModel` (`internal/cost/cost.go`), which uppercases the first rune — so
any two `z-ai/*` slugs already collide to `Z…`. That confirms the need.
(`internal/cost` is **free to diverge** — the ovs byte-comparability rule is
retired, ovs is obsolete — so no seed-manifest dance; keeping state-dir I/O
out of `cost.go` and passing a glyph-resolver in from the TUI is a soft
design preference, not a requirement.) The registry keys on the **full slug**,
not `ShortModel`'s output (which collapses distinctions). Design:

- **Keep the Claude letters** (F/O/S/H) for the common, legible case.
- **A package-level symbol palette** (`¤ § ‡ ° …`) for non-Claude models.
  **Single-cell glyphs only** — no CJK/double-width chars, or the MODELS
  column (widened in grove-20) misaligns.
- **Assignment is first-come from a persisted `slug → glyph` table** in the
  state dir, *not* a pure hash of the slug. A pure hash collides (two models,
  one glyph); a persisted registry guarantees uniqueness and stability — a
  model keeps its glyph forever. Assignment *order* is deterministic (next
  free palette slot); the table is the source of truth.
- **Legend at the bottom of the cost page**, scoped to models **present in
  the current view** — never the full 300-model catalog.

*Cross-machine caveat:* per-machine persisted assignment means two machines
could pick different glyphs for the same slug. For a solo tool that's fine; if
shared consistency ever matters, seed the registry with a checked-in
canonical mapping for known models + first-come for the rest.

---

## 5. Model catalog fetch + cache

The `+ add model` fuzzy-find needs OpenRouter's catalog (`/api/v1/models`).
grove **already makes outbound HTTP** — Linear GraphQL (`internal/linear`,
15s timeout) and ntfy push (`internal/hooks`, 1.5s timeout) — so reuse that
`http.Client`+timeout pattern rather than inventing one. Still:

- Fetch once, **cache in the state dir**, refresh lazily/on demand.
- **Offline-tolerant**: a missing/stale cache degrades to raw-slug entry
  (§3.2 path 2), never a hard block.
- Keep it off any hot path — it's an interactive convenience, not a
  dispatch-time dependency.

---

## 6. Preflight & failure modes

- **`gv doctor` profile check (optional but recommended).** For each profile:
  is `auth_token_env` set? is `base_url` reachable? Without it, a mis-set key
  means the worker's `claude` process dies in the pane and you debug blind.
  (The live secret is `OPENROUTER_API_KEY` in `~/.config/grove/.env`.)
  Cheap, high-value.
- **Credit exhaustion.** $10 of credits *will* run dry; OpenRouter then
  returns 402 and the worker errors. Detection (`internal/detect/live.go`)
  currently scrapes for Claude's activity line — a dead OpenRouter pane should
  surface as a distinct "backend error," not a mystery-stuck worker.
  Post-MVP, but likely to bite early.

---

## 7. What does *not* change (reassurances)

- **Hooks** — still fire; it's the `claude` binary. No settings.json change
  (env goes via the command prefix, §2.1).
- **MCP tool use** — passes through the Anthropic Skin; GLM-5.2 is strong at
  tool use.
- **Detection / fleet state / adopt / sweep / audit** — all resolve workers
  via the stored `tmux_session`/`tmux_window` on task events; a profile only
  changes the launched command, not the tracking.

---

## 8. Open questions for design review

1. **Profile struct home.** Add `ModelProfiles map[string]*ModelProfile` to
   `Config` and `model_profile string` to `Repo`? Confirm it composes with
   `WithModel` rather than duplicating it.
2. **Slot semantics.** Claude Code maps `/model` picks to
   `ANTHROPIC_DEFAULT_{OPUS,SONNET,HAIKU}_MODEL`. Do we set all three from the
   profile, or just `ANTHROPIC_MODEL` (single default) for MVP? Leaning:
   set all three so the in-session `/model` picker still behaves.
3. **Glyph palette size + charset.** How many symbols to reserve, and which
   are safely single-cell across the operator's terminals?
4. **Cost real-vs-estimate marker.** A column flag, a legend note, or a
   separate sub-total? Reviewer's call.
5. **Catalog fetch dependency.** Acceptable to add an HTTP client to the
   binary for the (cached, optional) catalog, or keep add-model slug-only for
   v1 and defer fuzzy-find?
6. **Doctor scope.** Reachability check = a real request (costs a token /
   needs the key) vs a cheap TCP/HTTP HEAD. Leaning: HEAD/`/models` ping, no
   inference.

---

## 9. Phasing

- **P0 — Secret hygiene.** ✅ *Done 2026-07-08:* key moved to
  `~/.config/grove/.env` (perms `600`), source removed. **Rotate the key** (it
  was in plaintext) and re-drop it there. Loadable interactively via a
  `set -a; . ~/.config/grove/.env; set +a` line in the shell rc.
- **P1 — Profile + worker grab.** `ModelProfile` struct, `--profile` flag,
  per-repo default, and the **self-sourcing launch wrap from §2.1** (the
  worker sources `~/.config/grove/.env` itself — this is the key-into-pane
  mechanism, folded in here, not left implicit). One config line + one flag =
  workers on OpenRouter, fully tracked. Add `Cost.Pricing` rows so estimates
  aren't garbage.
- **P2 — `)` orchestrator selector.** Overlay with configured profiles +
  raw-slug `+ add`. `0` unchanged.
- **P3 — Cost-page glyphs + legend.** Persisted glyph registry, estimate-vs-
  actual marker, scoped legend.
- **P4 (optional) — catalog fuzzy-find, doctor preflight, credit-error
  surfacing, frequency-pinning.** The polish tier.

Each phase is independently useful and revertible; the whole thing is
additive — with no profile configured, grove behaves exactly as today.

---

## 10. Acceptance

- The operator's OpenRouter key is out of the repo, referenced by env-var
  name from `~/.config/grove/`.
- `gv grab DEV-X --repo Y --profile openrouter-glm` opens a worker on GLM,
  and `gv ls` / detection / adopt treat it as a normal worker.
- `0` opens a Claude orchestrator (unchanged); `)` opens a profile selector
  and can launch a GLM orchestrator.
- Adding a model works by fuzzy-find *or* raw slug paste; a brand-new slug is
  never blocked by a stale catalog.
- The cost page shows each model as a stable, unique glyph with a legend, and
  metered OpenRouter cost is distinguishable from Claude effort-estimates.
- With no profile configured anywhere, grove is byte-for-byte today's UX.
