# Model profiles — implementation plan (P1 + P2)

> **Status: execution plan.** Derived from
> [2026-07-08-openrouter-model-profiles-design.md](2026-07-08-openrouter-model-profiles-design.md)
> (Rev 1, design-reviewed). Read the design doc first — this plan does not
> repeat the rationale, only the build steps.
>
> **Goal (why this is priority-1):** the operator is running low on Claude
> credits and needs a working backup. When this ships, they must be able to
> (a) **open an orchestrator on an OpenRouter model**, and (b) **grab a worker
> on an OpenRouter model**. Everything else in the design (cost-page glyphs,
> catalog fuzzy-find, doctor checks) is deferred — see §Deferred.
>
> **Scope of THIS plan:** P1 (worker on a profile) + P2 (orchestrator on a
> profile). P0 (secret hygiene) is already done. P3/P4 are follow-up issues.

---

## Preconditions (done / operator)

- **P0 done:** the OpenRouter key is at `~/.config/grove/.env` as
  `OPENROUTER_API_KEY=…`, perms `600`. **Operator must rotate it** (it was
  plaintext) and re-drop it there — the code only references the var name, so
  rotation needs no code change.
- Target model for the backup: `z-ai/glm-5.2` (OpenRouter, 1M ctx, strong at
  tool use). `haiku` slot can map to a cheaper model (`z-ai/glm-4.5-air`).

## The core mechanism (from design §2.1 — do not deviate)

The worker/orchestrator launch **self-sources the secrets file**; it must not
rely on the tmux server or interactive-shell env (validated by experiment).
The resolved command wraps the *already-`WithModel`'d* claude command:

```
<setup> && ( . ~/.config/grove/.env \
    && export ANTHROPIC_BASE_URL='<base_url>' \
              ANTHROPIC_AUTH_TOKEN="$<auth_token_env>" \
              ANTHROPIC_MODEL='<slot slug>' \
    && exec <modeledClaudeCmd> "$(cat <prompt>)" )
```

Invariants (all from design review — a reviewer will check these):
- **`WithModel` first, env-wrap second** (WithModel inserts `--model` after
  the first token; wrapping the raw string breaks it).
- **`export … ; exec`, never `env NAME=$VAR`** (keeps the token out of argv).
- **Shell-quote `base_url` and the slug**; leave the token a bare `$NAME`.
- **Never persist the wrap into `repo.Claude`** (hooks resolve the config dir
  from the stored `r.Claude`; persisting the wrap breaks `~/.claude`
  resolution — `hooks.go:246-274`).

---

## Tasks (one commit each; TDD for anything with branching logic)

### T1 — `ModelProfile` config type + loader
- Add to `internal/config/config.go`:
  ```go
  type ModelProfile struct {
      BaseURL      string `yaml:"base_url"`
      AuthTokenEnv string `yaml:"auth_token_env"`
      Opus         string `yaml:"opus"`
      Sonnet       string `yaml:"sonnet"`
      Haiku        string `yaml:"haiku"`
  }
  ```
  `ModelProfiles map[string]*ModelProfile` on `Config`; `ModelProfile string
  \`yaml:"model_profile"\`` on `Repo` (per-repo default, empty = none).
- The empty/absent profile ⇒ today's behavior byte-for-byte (no wrap).
- Secrets-file path resolves from grove's config dir (`~/.config/grove/.env`)
  — add a resolver, don't hardcode `$HOME` in the command builder.
- **Tests:** parse a config with `model_profiles`; absent map ⇒ nil, no panic.

### T2 — the launch-wrap builder (core; strict TDD)
- New function (in `config`, next to `WithModel`), e.g.
  `WrapProfile(modeledCmd string, p *ModelProfile, secretsPath string) string`
  that produces the `( . <secrets> && export … && exec <modeledCmd> … )` form.
- Reuse `shellQuote` for `base_url` + slug; token stays `$AuthTokenEnv`.
- `p == nil` ⇒ return `modeledCmd` unchanged.
- **Tests (this is the risk surface):** nil-profile passthrough; ordering
  (WithModel applied before wrap — feed it a `--model`'d command and assert
  the flag stays on the claude token); metachar in `base_url` is quoted; the
  token appears only as `$NAME`, never expanded, in the string; `exec` present.

### T3 — wire the worker grab
- In the grab path (`cmd/gv/main.go` ~771): after `claudeBin :=
  config.WithModel(repo.Claude, *modelFlag)`, resolve the effective profile
  (from `--profile` flag → repo `model_profile` default → none) and, if set,
  `config.WrapProfile(...)` the composed claude command **inside** the
  `setup && …` seam so the wrap covers only the claude portion.
- Add `--profile <name>` flag to grab. Absent ⇒ repo default ⇒ none.
- Slot selection: map `--model`/default to the profile's opus/sonnet/haiku
  slugs via `ANTHROPIC_MODEL` (and set the three `ANTHROPIC_DEFAULT_*_MODEL`
  so the in-session `/model` picker behaves — design open-Q#2).
- **Do not** write the wrap back into `repo.Claude` (invariant).
- **Test:** grab with `--profile` produces a command containing the sourced
  export + the profile's slug; without it, the command is unchanged from today.

### T4 — open an orchestrator on a profile
- **Must-have (CLI, guaranteed):** a flag/subcommand to launch the
  orchestrator with a profile, wrapping the **whole** `orchestratorCmd`
  (`main.go:424-458`) — **both limbs of the `--continue … || …` retry** — so a
  `--continue` failure can't relaunch on Anthropic (design §3.1 / S6).
- **Stretch (TUI):** the `)` (Shift-0) hidden selector in the cockpit that
  lists configured profiles and opens an orchestrator on the pick; `0` stays
  Anthropic, unchanged. If the TUI work risks the deadline, ship the CLI path
  and leave `)` as a fast-follow — the *capability* is what matters for the
  backup.
- **Test:** orchestrator launch string with a profile wraps both `||` limbs.

### T5 — cost attribution stays sane (light)
- Ensure `Cost.Pricing` (already exists, `config.go:47`) can carry OpenRouter
  slugs; add a row for `z-ai/glm-5.2` (`{input: 0.42, output: 1.32}`) in the
  workspace config as an example.
- **Verify** the transcript records the slug as the pricing key; unknown ⇒
  `CostKnown:false` (safety net, no code needed). No glyph work here — that's
  deferred (P3).

### T6 — verify + document
- `go build ./... && go vet ./... && go test ./...` green; `gofmt -l .` empty.
- Run `e2e/dummy.sh` (task lifecycle must still pass with profiles absent).
- **Manual smoke (the real acceptance):** with a `openrouter-glm` profile
  configured, `gv grab <a test ticket> --repo grove --profile openrouter-glm`
  → worker pane authenticates to OpenRouter and runs GLM (confirm via the pane
  / a trivial task). Then open an orchestrator on the profile and confirm it
  answers. Spends a few cents of OpenRouter credit — expected.
- Note the new `model_profiles` + `--profile` in `AGENTS.md`/README.

---

## Deferred (separate follow-up issues — NOT this worker)

- **P3:** cost-page glyph registry (persisted slug→glyph, single-cell palette,
  scoped legend) + estimate-vs-actual `$` marker. Keeps `cost.go` unchanged
  for now.
- **P4:** `+ add model` catalog fuzzy-find (`/api/v1/models`, cached; raw-slug
  entry always available); `gv doctor` profile preflight; credit-exhaustion
  pane detection; `)` frequency-pinning.

---

## Acceptance (what "backup ready" means)

1. A `model_profiles: { openrouter-glm: … }` block + `OPENROUTER_API_KEY` in
   `~/.config/grove/.env` is all the setup required.
2. `gv grab DEV-X --repo grove --profile openrouter-glm` runs the worker on
   GLM, fully tracked in `gv ls` (hooks/detection unchanged).
3. The operator can open an orchestrator on `openrouter-glm` (CLI at minimum;
   `)` selector if it lands) and converse.
4. With **no** profile configured, grove behaves exactly as today.
5. Build/vet/test/gofmt green; `e2e/dummy.sh` passes.

## Guardrails for the worker

- **Open a PR; do not merge.** The operator merges + `go install ./cmd/gv`
  when back. (Merge checks via `gh`, never git ancestry.)
- Decision logic in tested `internal/` packages; `main.go` stays thin glue.
- If blocked on a real ambiguity, ask via STATUS rather than guessing on an
  irreversible choice.
