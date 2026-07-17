# Codex harness — GPT-subscription workers & orchestrators as a `)` lane (design draft)

Status: **design seed** (for design-reviewer). Refines ticket
[grove-62](https://github.com/JollyGrin/grove/issues/62) with (a) the corrected
selection model, (b) a source-verified answer to the AGENTS.md drift problem,
(c) the outbound-relay seam the ticket waved off, and (d) a re-cut phasing that
puts *chat-to-chat communication* first and glyphs last. The ticket's
six-seam adapter and its source-verified Events (inbound hooks) spec stand —
this doc does not re-derive them; it links and extends.

Research grounding: `openai/codex@main`, read 2026-07-13
(`codex-rs/config/src/config_toml.rs`, `core/src/agents_md.rs`,
`codex-rs/hooks/`), plus Codex docs (CLI reference, AGENTS.md guide,
non-interactive mode, auth). Runtime-only items that need the live GPT sub are
called out in §8.

---

## 1. The thesis

grove is becoming **harness-agnostic**. Today the operator picks a *lane* when
starting a chat: Anthropic is first-class (`O` / default, zero friction);
OpenRouter model profiles ride `)` (a different `base_url`+model under the
**same `claude` binary**). Codex is the next lane — but selecting it swaps the
**binary + subscription auth** (native `codex`, ChatGPT OAuth) instead of
swapping `base_url`. From the operator's seat it must feel identical to picking
a profiled chat: pick the lane, adapters handle the rest.

The goal (Dean, 2026-07-13): make the claude/codex distinction *insignificant
from grove's perspective*. Any orchestrator (claude, openrouter, or codex) can
grab a worker on any lane; any worker reports to any orchestrator; they all
**communicate the same way they do now**. Glyphs/at-a-glance status are polish;
the load-bearing requirement is that the chats talk to each other unchanged.

## 2. Selection model — codex is a `)` lane, not a per-repo pin

**This supersedes grove-62's `harness:` per-repo key.** A per-repo key pins a
repo to one harness and touches per-repo defaults — the opposite of what we
want. Instead:

- Selection is **per-invocation**, through the surfaces that already exist:
  `)` (the profiled-chat picker) for orchestrators, `--profile`-style selection
  for `gv grab`. Defaults are **untouched**: `O`/no-flag = Anthropic, exactly
  as today. **`anthropic` is the default *sentinel*, not a registry entry** —
  `ResolveProfile` short-circuits `name == "anthropic"` → nil before any map
  lookup (`config.go:405`), and `0`/`O` already means Anthropic. It must stay
  off the lane list, or the `)` picker would start showing a default that today
  is implicit.
- The picker enumerates **lanes**, where a non-default lane resolves to
  `(harness, optional model-profile)`:
  - `openrouter-glm`, `deepseek-flash`, … → `(claude, <profile>)` — today's
    profiles, unchanged.
  - `codex-gpt` → `(codex, none)` — the new lane; harness swap, no `base_url`.

### 2.1 Why codex can't just be a `ModelProfile`

`ModelProfile` (grove-36, `internal/config/config.go`) is defined as an
*Anthropic-API-compatible backend reachable by env-wrapping the `claude`
binary*: `base_url` + `auth_token_env` + model slugs, applied via `WrapProfile`.
Codex is a **different binary** with **OAuth** (no API key, no `base_url`), so it
cannot be expressed as a `ModelProfile`. The implementation seam is therefore
real (`internal/harness`, per grove-62) — but the **operator-facing selection
surface must not fork.**

### 2.2 The lane registry

Introduce a thin lane concept that both `model_profiles` and harnesses feed:

```
lane := { name, harness ∈ {claude, codex}, profile? (ModelProfile ref) }
```

`ResolveOrchestratorProfile` / `profileNames` / the `)` picker and the grab
`--profile` path enumerate lanes, not just profiles. Config-informed, so a lane
named `codex-gpt` carries `harness: codex`; the picker shows it beside the
OpenRouter entries. Default-lane resolution
(`ProfileHint`/`ProfileSpawn`/`ProfilePick`) is unchanged in spirit — it just
ranges over lanes. A per-repo *default lane* may still be offered as an
overridable hint; it is **never** a pin.

**Load-bearing branch point (do not hand-wave "adapters do the rest").** The
resolve path must fork on harness *before* the profile is wrapped. Today
`ResolveProfile` (`config.go:400`) returns a `*ModelProfile` that the spawn path
feeds to `WrapProfile` (`config.go:328`), which emits
`ANTHROPIC_BASE_URL=… exec claude …`. If a `codex-gpt` entry reached that path
it would launch a **broken claude**, never codex (empty `base_url`, `"$"`
auth). So the concrete changes M1 must make, named here so review can weigh
them:
> - resolution returns `(harness, *ModelProfile)`; `harness == codex` ⇒
>   `WrapProfile` is skipped entirely (the launch seam calls the codex
>   `LaunchCmd`, not `WrapProfile`).
> - the `ProfileSpawn` handler in `cmd/gv/main.go` and the grab `--profile`
>   path branch on harness before wrapping.
> - `ResolveOrchestratorProfile` candidates (`profileNames()`) grow to include
>   codex lanes.

> Design-review question (§8-Q1): where does the codex lane *live*? (a) a new
> top-level `lanes:` map (honest that harness-swap ≠ backend-swap, no risk of
> a codex entry falling into `WrapProfile`); or (b) an optional `harness:`
> field on `model_profiles` entries (one registry, smaller) — but §2.1 warns
> codex is **not** a `ModelProfile`, so (b) only works if the resolve path
> branches on `harness` *before* the map value is ever treated as a
> backend-wrap. (a) is safer against exactly the broken-claude failure above.
> Lean (a) unless review shows the branch in (b) is clean.

## 3. The adapter layer (from grove-62) + one new seam

grove-62 defines an `internal/harness` interface with `claude` as
extraction-of-current-code and `codex` as the second implementation, across
these seams: **launch, resume, events (install + receive), transcript**. All of
that stands. This doc adds **one seam the ticket wrongly called
"harness-agnostic":**

```go
// RelayInput delivers gv answer / gv nudge text to a live worker.
// Claude: tmux send-keys text + Enter into the pane (today's behavior).
// Codex: tmux send-keys -l <text> + Enter — single-line only (see §4);
//        multi-line must be split (Ctrl+J per newline) or fall back to
//        `codex exec resume`. NOT agnostic — the newline/paste handling
//        differs enough to be a seam.
RelayInput(session Session, text string) (string /*tmux cmd*/, error)
```

Everything else grove-62 lists as agnostic (tmux window/worktree lifecycle,
window-name glyphs written by gv, PR/merge via `gh`, audit/sweep,
events.jsonl) **remains agnostic.**

## 4. Communication — the two halves, spec'd equally

Dean's #3 ("chats communicate the same way they do now") is the whole point, so
both directions get equal rigor. Today's loop (all-claude):

```
orch: gv grab ──► worker spawns (tmux + kickoff)
worker hits a question ─► Stop hook ─► gv hook classify ─► events.jsonl
                                                        ─► tasks.json (question + text + session id)
orch: gv ls --json ◄── reads the question
orch: gv answer grove-N "…" ─► send-keys into worker pane ─► worker continues
```

### 4.1 Inbound (worker → orch): the Events seam — **core, keep**

grove-62's Events spec maps this. **What is source-verified vs runtime-gated,
kept honest:** the `hooks.json` schema, the feature flag (`hooks`, stable,
default-enabled), and the event set (`SessionStart`, `Stop`,
`PermissionRequest`, …) are read from `openai/codex@main` — the *shapes* are
confirmed. That **hooks fire as grove needs under grove-style full-auto** is
**not** confirmed and is an M0 runtime gate (§7). Mapping intent:

- `SessionStart` → session id + `transcript_path` (`transcript_path` on every
  payload solves session-log location).
- `Stop` (nullable `last_assistant_message`) → `classify()` →
  question/blocked/done.
- `PermissionRequest` → needs-input, notify-only, **never** auto-respond
  (propose-then-dispose). **Tension to resolve in M0:** grove runs workers
  full-auto, and full-auto typically *bypasses* approval prompts — so
  `PermissionRequest` may rarely/never fire in grove's actual mode. If so, the
  `?`/needs-input signal must come from the `Stop`+STATUS-sentinel path alone,
  and `PermissionRequest` is a bonus, not a load-bearing seam.
- No `SessionEnd` → dead state via `gv audit` window-liveness fallback.

**"Default-on" ≠ "installed."** The feature being on means the *mechanism*
runs; grove still has to **write its own `hooks.json` entries** pointing at
`gv hook …`, at the codex config layer (`~/.codex/hooks.json` or the config.toml
hooks table — *not* the claude `settings.json` path). That write is the
`InstallEvents` seam, merge-preserving like the settings.json writer, and
**`gv doctor` (§6) must validate it exists and points at the current `gv`.**

**Not deferrable.** The `Stop → gv ls` question path *is* how an orchestrator
reads what a codex worker is asking. It ships in the communication phase, not
the glyph phase. (What defers is only the *visual rune rendering* and the ntfy
push — see §6.) The kickoff template must enforce the same STATUS-line
discipline `classify()` parses, or the `?` relay is silent — so a **codex
kickoff variant is a first-class deliverable**, not fine print.

### 4.2 Outbound (orch → worker): `gv answer` / `gv nudge` — **core, newly spec'd**

Source findings (codex composer internals):

- **Parity path:** persistent `codex` TUI in the pane + `tmux send-keys`, exactly
  like claude. The composer accepts injected text via bracketed paste
  (`handle_paste()`), with a non-bracketed fallback that buffers rapid keystrokes
  and inserts them atomically after ~50 ms. `send-keys -l "<text>"` then `Enter`
  should land as composer text + submit.
- **The catch — newlines:** `Enter` *submits*; embedded newlines need `Ctrl+J`
  (Shift/Alt+Enter unreliable; open regression openai/codex#20580). grove's
  answer/nudge are single-line → fine. Multi-line would submit prematurely — so
  the relay must be single-line by contract, or split on `\n` into `Ctrl+J`
  sequences.
- **Robust non-TUI fallback:** `codex exec resume --last "<msg>"` (or by session
  id) runs headless, resumes the session, prints the final message to stdout
  (`--json` = NDJSON). It's a *separate process* — breaks "attach shows the live
  conversation" — so it's the fallback, not the parity path.

This is the seam in §3. The M0 spike (§8) must confirm send-keys lands cleanly
through tmux (the 50 ms burst timing vs tmux paste is the one thing not provable
from source).

## 5. AGENTS.md drift — solved by config, not a symlink

Codex reads `AGENTS.md`, not `CLAUDE.md`. Dean's instinct was a symlink; source
gives a strictly better answer.

**Confirmed** (`config_toml.rs`): `project_doc_fallback_filenames: Vec<String>`
— *"Ordered list of fallback filenames to look for when AGENTS.md is missing."*

```toml
# ~/.codex/config.toml  (global, one-time — set by gv doctor)
project_doc_fallback_filenames = ["CLAUDE.md"]
```

Result: Codex reads every repo's existing `CLAUDE.md` wherever there's no
`AGENTS.md`. **Zero symlinks, zero per-repo files, zero drift** — one source
file, both harnesses read it. Where codex *needs* different guidance, drop an
`AGENTS.md` (or `AGENTS.override.md`) in that dir and it wins over the CLAUDE.md
fallback — "avoid drift, allow codex-specific stuff, unless unnecessary,"
exactly.

Why it beats the symlink: worktrees are fresh checkouts (a symlink would need
regenerating per grab), and codex-specific content breaks a symlink into drift.
The config-fallback has neither problem.

Caveats (from `agents_md.rs`): discovery walks git-root → cwd and
**concatenates** (separator `--- project-doc ---`); a global `~/.codex/AGENTS.md`
layers into *every* codex session incl. workers — keep it empty/grove-neutral.
`project_doc_max_bytes` caps at 32 KiB (grove's CLAUDE.md ≈ 4 KB; a huge one
truncates). The orchestrator's own `.grove/orchestrator/CLAUDE.md` is picked up
by the same fallback, so a **codex orchestrator needs no separate prompt file**
unless it wants codex-specific behavior.

## 6. Doctor — the setup + preflight home (Dean's idea)

When a codex lane is configured, `gv doctor` checks four things (propose-then-
dispose — doctor reports, a `--fix`/setup path writes only on confirm):

1. `codex` on PATH; record its **version** (the hooks-schema pin lives here).
2. `codex login status` → exits 0 when authed, prints mode (ChatGPT sub vs API
   key). Non-zero = the fleet-blocking failure. (Sub = OAuth; creds in
   `~/.codex/auth.json` or OS keyring via `cli_auth_credentials_store` — doctor
   keys off login status, **not** an env var.)
3. `~/.codex/config.toml` has `project_doc_fallback_filenames ⊇ ["CLAUDE.md"]`
   — the §5 drift setup. Missing → offer to add (merge-preserving TOML writer,
   same discipline as the settings.json / hooks.json writers).
4. The pinned codex version ships the `hooks` feature, **and** grove's
   `hooks.json` entries exist at the codex config layer and point at the current
   `gv` binary (§4.1 — "default-on" is the mechanism, not grove's entries). Only
   meaningful once `InstallEvents` lands (M2); before that, doctor reports it as
   "not yet wired (manual-grade)."

## 7. Phasing (re-cut: communication first, glyphs last, orchestrator core)

Each phase lands independently, gated on the previous; the Claude-only path stays
**byte-identical** throughout (`e2e/dummy.sh` untouched and green).

- **M0 — spike (needs the sub):** source half DONE (this doc + grove-62). Runtime
  half: hand-run a codex worker in a tmux window in a scratch worktree; confirm
  (a) send-keys relay lands (§4.2), (b) `Stop`/`PermissionRequest` fire under
  grove-style full-auto, (c) `last_assistant_message` fidelity on
  interrupt/compaction, (d) `codex resume <id>` by-id + cross-worktree, (e)
  quota-exhaustion signal. Write findings to LEARNINGS.md + a codex-facts skill
  section. Timebox: one session.
- **M1 — launch + lane + doctor:** `codex-gpt` lane in the `)` picker and grab
  `--profile`; launch a codex worker with the kickoff prompt (codex variant,
  AGENTS.md/CLAUDE.md via §5); `gv doctor` codex checks (§6); pin + record the
  codex version. Task tracked manual-grade (no state glyphs yet). Additive; dummy
  e2e proves the claude path unchanged.
- **M1.5 — codex orchestrator (core, not optional):** `)` opens a codex
  orchestrator chat; it runs `gv` like any other and drives claude/openrouter/
  codex workers. Cheap — touches no events/cost/adopt adapter; needs only the
  §5 CLAUDE.md fallback (no separate prompt file) and a codex pane. **What is
  real at M1.5, precisely:** codex-orch ↔ **claude**-worker is fully real (the
  worker is claude, so its state reaches `gv ls` today). codex-orch ↔
  **codex**-worker is **manual-grade only** — the codex worker's question can't
  reach `gv ls` until the inbound path lands, so the codex orchestrator must
  `tmux capture-pane` to read it. Full codex-worker parity waits on M2. (So the
  "bidirectional" headline is: both *directions of orchestration* exist at M1.5;
  full *observability* of a codex worker is M2.)
- **M2 — communication parity (the important phase):** the `RelayInput` seam
  (§4.2) + the inbound `Stop → gv ls` question path (§4.1: `InstallEvents`
  writes grove's `hooks.json`; SessionStart/Stop/PermissionRequest → gv events →
  question/blocked/done states). Parity target: an orchestrator's
  `gv ls`/`answer`/`nudge` loop against a codex worker is **indistinguishable**
  from a claude worker. **Note the honest coupling:** window-name glyphs are
  *state-driven* and harness-agnostic (written by gv from the shared state
  enum), so the moment M2 produces `question`/`working`/`done` state, the
  existing glyph renders **for free** — codex reuses the same state enum and
  runes. What M2 does *not* include is the ntfy push and any *codex-specific*
  rune additions; those are M3. (This corrects the earlier "glyphs are all M3"
  framing: shared-state glyphs come with M2; only harness-specific polish defers.)
- **M3 — polish: push + adopt + cost:** needs-input ntfy push, any codex-specific
  runes, session-id capture for `gv adopt` (`codex resume`), rollout-file parser
  feeding `gv cost` (estimate-only, **segmented by harness — never a blended
  $-per-PR** across harnesses). Dead glyph stays on the `gv audit` fallback
  (no SessionEnd).

**`gv` verbs during the rollout (interim-safety — a verb must never do the
wrong thing to a codex worker before its phase lands):**
- `gv adopt` — must **detect harness** and, for a codex worker before M3,
  refuse/no-op rather than resume with `claude --resume` semantics. (adopt
  gains codex support in M3.)
- `gv cost` — must **exclude codex workers from aggregates** (the
  `$-per-merged-PR` rollup) until M3's harness-segmented parser exists;
  otherwise a codex PR lands in the blended headline at zero/claude price —
  the exact thing Q5 forbids.
- `gv diff` — genuinely agnostic (pure git); safe from M1.
- `gv audit`/`gv sweep` — agnostic (window-liveness + `gh`), and audit is the
  dead-state fallback for codex throughout.

## 8. Open questions for design review

- **Q1 (§2.2):** lane registry shape — new `lanes:` map vs `harness:` field on
  `model_profiles` entries. Lean the latter (one registry, additive).
- **Q2:** does `RelayInput` warrant the seam, or can a single "single-line
  send-keys + Enter" cover both harnesses with only a literal-flag difference?
  (Depends on M0 finding (a).)
- **Q3:** codex kickoff template — STATUS-line discipline (the `?` relay depends
  on it), explicit `gh pr create` instruction (claude's kickoff assumes it; codex
  must be told the same), no `/model`, codex slash-command set. How much diverges
  from the claude kickoff templates (`internal/kickoff/*.tmpl`)?
- **Q4 (needs sub):** quota exhaustion — the false-green-glyph risk. What
  detectable signal marks a quota-dead codex worker so grove never shows it
  working? Tie to the audit dead-state fallback.
- **Q5:** cost incommensurability — codex spend (turns / sub-quota units) vs
  claude token-estimates. Confirm the cost view segments by harness and never
  presents a blended headline number.

## 9. What does NOT change (reassurances)

- `harness` unset / no codex lane = **today's behavior everywhere**, byte-for-
  byte. `e2e/dummy.sh` untouched.
- Guardrails are harness-independent: propose-then-dispose, zero terminal-state
  mutations, append-only `events.jsonl`, never delete worktrees/branches grove
  didn't create, merge checks via `gh`.
- The `claude` seam implementations are extraction-of-current-code — no rewrite
  of hooks/state/kickoff.
- Anthropic stays first-class: `O` and the default spawn are unchanged.

## 10. Acceptance

- A `codex-gpt` lane appears in `)` beside the OpenRouter profiles; picking it
  opens a codex orchestrator (M1.5) or grabs a codex worker (M1).
- A claude orchestrator's `gv ls` / `answer` / `nudge` loop against a codex
  worker is indistinguishable from a claude worker (M2).
- A codex orchestrator drives claude and codex workers (M1.5 + M2).
- `gv doctor` catches a missing binary, unauthed `codex login status`, and a
  missing `project_doc_fallback_filenames` (M1).
- With no codex lane configured, `go build ./... && go test ./...` and
  `e2e/dummy.sh` are green and unchanged.

## Appendix: proxy-profile fallback

Unchanged from grove-62's appendix — the OAuth-sidecar / `ANTHROPIC_BASE_URL`
route stays documented as a ToS-gray fallback, deliberately not built. Native
codex is the first-class route.
