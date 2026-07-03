# Grove connections — wizard, doctor, drift detection, and the pack system (research + design draft)

> **Terminology (decided 2026-07-03):** the shareable overlay of team
> conventions/checks/fragments is a **pack** (formerly "profile" in earlier
> drafts). "Profile" now always means a Claude Code profile
> (`CLAUDE_CONFIG_DIR`, e.g. `~/.cc-work`).

> **Status: investigation / design draft. No code.** Companion to
> [grove-spec.md](../DESIGN.md) — this deepens §6 (bootstrapping) and §9
> (OSS-readiness) and resolves Open Question 6 (orchestrator overlay
> mechanics). Sibling of [grove-learnings-design.md](grove-learnings-design.md)
> — the two meet at the *pack* concept (§6 here; L5 there).
>
> **The ask (Dean, 2026-07-03):** the wizard should run when grove does not
> detect connections for a repo. Configured with the Linear provider + the
> Grid skills/MCP, grove must behave **exactly** like the customized
> overstory does today — the Grid stops being hard-coded and becomes the
> first pack. Grove must detect when an important connection has gone
> missing and prompt reconnection.

---

## 1. The parity inventory — what "Grid hard-coded" concretely is

Everything that makes `ovs` Grid-shaped, from a full source read (2026-07-03).
This is the checklist: grove reaches parity when **every row is expressible
as pack/config data** and the Grid pack expresses it.

| # | Surface | Where in ovs | What it encodes |
|---|---|---|---|
| 1 | Kickoff templates | `internal/kickoff/{default,manual,pickup}.tmpl` | Linear ticket framing · "move to In Progress / In Review via dev-linear MCP" · `wrapping-up-task` skill + `pr-reviewer` agent invocation · never target `deploy/*` · ticket-prefix commits · STATUS sentinel (generic) |
| 2 | Doctor checks | `internal/doctor/doctor.go` | 4 Grid plugins present in `~/.cc-work` · universal CLAUDE.md symlink at the repos' parent · `LINEAR_API_KEY` env · `ccwork` alias resolvable via `zsh -ic whence` · dev-linear MCP auth (manual reminder) · terminal-notifier (macOS) |
| 3 | Orchestrator brain | `orchestrator/CLAUDE.md` | 7 duties (fleet summary, triage, dispatch, unstick, ticket sharpening, cleanup, cost analysis) · team guardrails (propose-only, never-Done, never Linear comments, never edit code) · Linear MCP + team `DEV` · Dean by name |
| 4 | Config defaults | `internal/config/config.go` | `linear:` section baked into the struct · worker default `ccwork --dangerously-skip-permissions` · `linear_labels` repo inference |
| 5 | Manual onboarding | `ONBOARDING.md` §4 | Create `~/.cc-work` profile + `ccwork` alias · register workspace marketplace · install 4 dev plugins · symlink universal CLAUDE.md · one-time dev-linear MCP OAuth ("open a session, call a Linear tool once") |
| 6 | Provider | `internal/linear/` | The GraphQL client itself (grove-spec §5.3 already moves this behind `TaskProvider`) |
| 7 | Implicit conventions | grab flow | Branch `DEV-1234-<slug>` · `.env`/`.envrc` copy · serialized setup · monorepo codegen-artifact copy |

**The key observation:** rows 2 and 5 are *the same knowledge twice* — the
doctor checks what ONBOARDING.md told the human to do, and both drift
independently (the doctor's plugin list is already hard-coded separately from
the onboarding prose). Row 1's transition verbs restate what row 6's provider
knows. Grove's job is to make each fact exist **once, declaratively**, and
derive the wizard step, the doctor check, and the reconnect prompt from it.

---

## 2. Prior art — what the field converged on

*(Digest of the 2026-07-03 research sweep; URLs in the appendix.)*

### 2.1 Wizard archetypes

Three poles, all legitimate: **zero-question** (turborepo infers everything
from lockfiles and fails with an explicit instruction when it can't;
`terraform init` is promptless and idempotent — "safe to run multiple
times"), **detect-then-confirm** (`nx init` detects repo shape and asks only
what it can't infer), and **ask-everything** (`firebase init` checkbox
wizard, but merge-re-runnable — adding a feature later merges into config
rather than clobbering). The good ones share: answers recorded in committed
config, secrets kept out of it, **every prompt has a flag twin** plus a
`--CI` mode (create-t3-app), and re-running is safe.

Two peer patterns stand out:

- **Backlog.md**: `backlog config` with no args relaunches the full wizard
  *pre-populated with current values* — **init and reconfigure are the same
  UX.** Its init also explicitly asks "how do you want to connect AI tools?"
  (instruction file / MCP connector / skip).
- **task-master**: `task-master models --setup` is a scoped re-runnable
  wizard that **also repairs a corrupt config** — wizard-as-repair-tool.

### 2.2 Doctor taxonomy

`flutter doctor` is the reference: per-check `success / partial /
notAvailable / missing / crash` → rendered `✓ / ! / ✗`, each failure paired
with a **copy-pastable fix command**, exit code reflects errors only.
`brew doctor` adds temperament: warnings framed as "if everything works,
don't worry," and a positive terminal state ("Your system is ready to
brew" — ovs's 🌳 already does this). `expo-doctor` adds **committed
per-check excludes** so a team's doctor stays green-by-default and red means
something.

### 2.3 Config layering

- **The two-file repo pattern is settled practice:** committed team config +
  same-name `.local` personal override, auto-gitignored at creation (Claude
  Code's `settings.json` / `settings.local.json`; mise's `mise.toml` /
  `mise.local.toml`).
- **ESLint's negative lesson:** implicit directory-cascading config was
  confusing enough that ESLint abandoned it for single-file flat config with
  explicit composition. Keep to a few *named* layers; never invisible
  cascade.
- **Codex CLI** contributes trust-gating: project-level config is loaded
  **only for trusted projects**. **git config** contributes `includeIf
  "gitdir:..."` — activating a whole config layer for a directory tree.
- **Secrets consensus:** never in committed config. Env-var indirection with
  documented names (task-master), OS keychain with plaintext fallback (gh),
  and **delegation** — gh extensions call `gh auth token` instead of storing
  their own copy.

### 2.4 Drift / reconnection detection

**Nobody polls with a daemon.** The state of the art is:

- **Lazy verification at command boundaries** — terraform detects stale
  `.terraform/` on every command and says "run terraform init"; flutter runs
  a validation subset before builds.
- **Fix-forward errors naming the cheapest remedy** — AWS SSO expiry prints
  the exact `aws sso login --profile <p>` line; the long-standing gh issue
  (#8846) asks that failures recommend `gh auth refresh`, not full re-login.
  Precision of the suggested remedy matters.
- **Content-hash trust** — direnv blocks `.envrc` until `direnv allow`, and
  the approval is a hash of the content, so *any edit re-blocks it*. Drift
  detection and re-consent in one mechanism.
- **Degraded mode over hard stop** — VS Code Restricted Mode: a persistent,
  scoped, one-click-reversible banner; most things still work.
- **Status command as queryable doctor** — `gh auth status` reports token
  source, scopes, validity, machine-checkably.

### 2.5 Pack/plugin patterns (how other tools ship "profiles")

The common shape: **a plugin is a versioned repo/package implementing a small
manifest contract; the core provides discovery, execution, and auth; enabling
it is one declarative line.** Best exemplars: **pre-commit** (provider repo
declares hooks in `.pre-commit-hooks.yaml`; consumers pin `repo` + `rev`),
**gh extensions** (inherit the core's auth — a plugin never re-authenticates),
**ESLint shareable configs** (profile composes *under* local overrides),
and — closest to home — **Claude Code plugin marketplaces**, which is exactly
how the Grid workspace already distributes its conventions. Notably, `flutter
doctor` has *no* public validator-plugin API — a gap grove can do better on.
**Conductor** proves the committed-lifecycle-scripts idea: `conductor.json`
at repo root (`setup` / `run` / `archive` + injected env vars) makes a
stranger's clone reproduce the author's worktree bring-up exactly.

---

## 3. The design centerpiece: the connections manifest

One declarative registry, assembled from core + provider + pack + repo
config, from which **four consumers derive**: the wizard's steps, the
doctor's checks, the reconnect prompts, and the TUI's degraded-state banner.
A connection is declared once; nothing about it is hand-maintained twice.

```yaml
# conceptual shape, not final syntax — lives across core defaults,
# provider adapters, and pack manifests, merged at load
connections:
  gh:
    kind: cli-auth                 # taxonomy below
    required-for: [grab, poll, done]
    check: gh auth status          # cheap, machine-readable
    fix: gh auth login             # cheapest remedy first
    ttl: 24h                       # re-verify cadence (lazy)
  linear-key:                      # contributed by the linear provider
    kind: env
    var: LINEAR_API_KEY            # api_key_env indirection survives
    required-for: [grab]
  worker-plugins:                  # contributed by the Grid pack
    kind: agent-plugins
    profile-dir: ~/.cc-work
    require: [dev-core@workspace, dev-superpowers@workspace,
              dev-linear@workspace, dev-safety@workspace]
    fix: CLAUDE_CONFIG_DIR=~/.cc-work claude plugin install ...
  dev-linear-mcp:                  # contributed by the Grid pack
    kind: mcp-auth
    probe: none                    # can't be probed cheaply —
    degrade-on: worker-blocked     # detected via worker failure signals (§5.3)
    fix: "open a worker session, call a Linear tool once (OAuth persists)"
  universal-context:               # contributed by the Grid pack
    kind: file
    path: <workspace-root>/CLAUDE.md
    expect: symlink -> workspace/plugins/dev-core/templates/grid-claude-md.md
  orchestrator-seed:
    kind: seeded-file              # content-hash tracked (§5.4)
    installed: ~/.config/grove/orchestrator/CLAUDE.md
```

**Connection kinds** (the check/fix machinery is generic per kind; instances
are data):

| Kind | Check mechanism | Cost | Examples |
|---|---|---|---|
| `binary` | `LookPath` | free | tmux, gh, git, claude |
| `env` | getenv | free | provider API keys |
| `file` / `symlink` | stat/readlink | free | universal CLAUDE.md, AGENTS.md present |
| `cli-auth` | subprocess w/ TTL cache | cheap | `gh auth status` |
| `worker-command` | `zsh -ic whence` w/ cache | cheap | the `ccwork` alias |
| `agent-plugins` | read `installed_plugins.json` | free | Grid dev plugins in `~/.cc-work` |
| `hooks` | settings.json inspection | free | the 4 ovs hooks |
| `mcp-auth` | **not probeable** → failure-signal driven | lazy | dev-linear OAuth |
| `seeded-file` | content hash vs embedded seed | free | orchestrator CLAUDE.md |
| `lifecycle` | declared script exit code | on use | repo `setup` command |

Each connection also declares `required-for` (which verbs need it) and a
severity: **error** (blocks those verbs) vs **warning** (degrades — banner,
not stop).

---

## 4. `gv init` — the wizard

### 4.1 Behavior

Detect-then-confirm (the nx/gh archetype), assembled from the same manifest:

1. **Probe phase** (grove-spec §6.1, unchanged): stack, repo shape,
   workspace scope, existing agent context, task-backend signals.
2. **Connection reconciliation:** evaluate every declared connection; for
   each *missing* one, present the detected state + proposed fix, and either
   run the fix (safe, mechanical: install hooks, write config, register
   workspace) or walk the human through it (auth flows, one-time MCP OAuth —
   which **must** happen interactively at init because it can't happen inside
   an autonomous worker later; this is ovs doctor's hard-won lesson).
3. **Record:** answers land in `.grove/config.yaml` (committable) or
   `.grove/config.local.yaml` (personal, auto-gitignored) — see §7.
4. **Positive terminal state:** the doctor's 🌳.

Contract (all from prior art, all cheap to promise early):

- **Idempotent and merge-re-runnable** (firebase/terraform): `gv init` on a
  configured workspace is the *reconfigure* wizard, pre-populated with
  current values (Backlog.md), and repairs corrupt config (task-master).
- **Every prompt has a flag twin** + `--yes`/`--ci` for scripted setup.
- **Scoped runs:** `gv init --only <connection>` fixes one thing (this is
  what reconnect prompts point at, §5).

### 4.2 "It should run when it does not detect connections for a repo"

The wizard is not a first-run event; it's the **reconciliation action** the
ambient check triggers. Concretely:

- Bare `gv` / `gv ui` / `gv grab` in a directory with **no ambient
  workspace** (no `.grove/` up-tree, per grove-spec §6.5.2) → offer init:
  *"no grove workspace here — set one up?"* (bare `gv` already falls back to
  the switcher; the switcher gains an "init this directory" row).
- Any verb whose `required-for` connections are **missing** → don't run the
  verb; print the one-line state + the scoped wizard invocation
  (`gv init --only dev-linear-mcp`). Missing-error connections block;
  missing-warning connections print the banner and proceed.
- A repo added to a workspace later (parent-scope discovery finds a new
  sibling) → the repo-level subset runs for just that repo (setup command,
  base branch, provider mapping).

---

## 5. `gv doctor` + drift detection — noticing when a connection goes missing

### 5.1 Doctor

Same manifest, full evaluation, flutter taxonomy: `✓ / ! / ✗` per check,
copy-pastable fix per failure, "N/M passed" summary, exit code reflects
errors only, committed excludes for known-irrelevant checks (expo pattern —
keeps a team's doctor green-by-default). `--fix` applies the *safe automatic*
subset (reinstall hooks, re-symlink, reseed) and never touches auth.

### 5.2 Lazy verification at command boundaries

No daemon (grove keeps ovs's stance). Instead:

- Every fleet-mutating verb evaluates the **relevant subset** of connections
  before acting, with per-kind TTL caches so `grab` stays fast: free checks
  every time; `cli-auth` cached (e.g. 24h); `mcp-auth` never proactively
  probed.
- The TUI header carries a connection-state glyph next to the existing
  counts; a degraded connection is a persistent one-line banner naming the
  connection and the scoped fix command (VS Code Restricted Mode: visible,
  scoped, reversible — not a hard stop unless a spawn actually needs it).

### 5.3 Failure-signal-driven degradation (the interesting part)

Some connections (MCP OAuth, expired tokens mid-session) can't be cheaply
probed — but grove already has a rich failure-signal channel: **the hook
classifier.** Workers end turns with `STATUS: BLOCKED — <reason>`, and the
Stop hook already parses `last_assistant_message`. Extend the classifier
with a connection-shaped-blocker pattern set (declared per connection in the
manifest — e.g. the dev-linear connection contributes "Linear tool
unauthorized / OAuth"): when a `blocked` mail matches, grove additionally
marks that connection **degraded** in state (an event, naturally — the
append-only model absorbs it). Effects: header glyph flips, next relevant
verb prompts the scoped reconnect, the orchestrator sees it in `--json` and
says so in its fleet summary. One worker hitting an expired auth becomes a
fleet-level "reconnect dev-linear" prompt instead of N workers failing one
by one.

`gh`-family failures get the same treatment deterministically: any grove
shell-out to `gh` that returns 401-shaped errors marks the `gh` connection
degraded with `gh auth refresh` (cheapest remedy — the gh #8846 lesson) as
the fix.

### 5.4 Seeded-file drift (content-hash trust)

Grove seeds files it then never overwrites (orchestrator CLAUDE.md — ovs
rule; kickoff template overrides). The direnv model closes the loop: record
the hash of what was seeded; when the embedded seed evolves in a new grove
version, the doctor reports *"orchestrator CLAUDE.md drifted from seed —
`gv sync --diff` to review"* instead of silently overwriting or silently
staling. (This is IDEAS.md's parked "doctor: orchestrator-drift check,"
generalized — it bit for real on 2026-07-02.)

**Trust gating — decided against (2026-07-03).** The same hash machinery
*could* gate executing a cloned repo's committed lifecycle scripts
(direnv-style `gv trust`, Codex trusted-projects), but grove ships without
it: ovs's stance — you configured the workspace, you own what it runs —
carries over for the private/solo phase. Recorded with a hard
**revisit-before-public-release** flag (see FMA row 1); the hash plumbing
built for seeded-file drift makes adding the gate later cheap.

---

## 6. The pack system — how the Grid becomes config

### 6.1 What a pack is

A **versioned directory or git repo** with a small manifest, referenced by
one line in workspace config (`pack: github.com/the-grid/grove-grid@v1`,
or a local path for private packs — **decided 2026-07-03: the Grid pack
lives as a subdirectory of the existing workspace marketplace repo**, so it
rides the distribution channel every teammate already has). Pre-commit's
repo+rev pinning for versioning; gh-extensions' auth inheritance (pack
checks reuse grove's connections — a pack never re-authenticates anything);
ESLint-shareable-config composition (pack sits *below* project and local
layers, so users can still override).

A pack may contribute — every slot optional:

| Slot | Contents | Grid pack example |
|---|---|---|
| `connections` | extra connection declarations (§3) | worker-plugins, dev-linear-mcp, universal-context symlink |
| `provider` | default provider + its config | `linear`, team `DEV`, `api_key_env` |
| `worker-env` | worker profile spec: `CLAUDE_CONFIG_DIR`, marketplaces to register, plugins to install, **MCP servers required (with auth mode)**, worker command | `~/.cc-work`, workspace marketplace, the 4 dev plugins (which carry dev-linear MCP), `ccwork --dangerously-skip-permissions` |
| `kickoff` | template fragments (§6.2) | wrap-up = "use wrapping-up-task; run pr-reviewer; address CRITICAL/IMPORTANT"; rules = "never target deploy/*; no Linear comments; never Done" |
| `orchestrator` | overlay markdown (§6.3) | the 7 duties' Grid-specific halves + team guardrails |
| `doctor` | extra checks beyond connections | monorepo codegen-artifact presence |
| `learnings` | the team's L5 layer (grove-learnings-design §3.1) | crystallized Grid conventions and gotchas |
| `lifecycle` | per-repo setup/archive script defaults | monorepo codegen copy-from-main-checkout |

**Parity claim, concretely:** the parity-inventory table (§1) maps row-by-row
into these slots — rows 1→`kickoff`+`provider`, 2→`connections`+`doctor`,
3→`orchestrator`, 4→`provider`+`worker-env`, 5→`connections`+`worker-env`
(the wizard *executes* what ONBOARDING.md §4 narrates), 6→`provider`,
7→`lifecycle`+core conventions. Nothing in the table lacks a slot.

### 6.2 Kickoff assembly (replacing monolithic templates)

The ovs templates decompose into: **frame** (task fields — core), **provider
verbs** (`StartInstruction` / `ReviewInstruction` — from the provider,
per grove-spec §5.4), **pack fragments** (wrap-up conventions, team
rules), **learnings block** (grove-learnings-design §3.3), and **sentinel**
(core, non-negotiable — the hooks depend on it). Default assembly renders
Grid-identical output when the Grid pack is active; a repo can still
override the whole template (`prompt:` survives) as the escape hatch.

### 6.3 Orchestrator overlay (resolves grove-spec OQ6)

The generic orchestrator `CLAUDE.md` ships with structure (duties skeleton,
propose-then-dispose, the `gv` tool table) plus an explicit extension point;
the pack ships an overlay markdown (Grid duties' specifics, team
guardrails, Linear MCP usage). At seed time grove **renders the composed
file** into the orchestrator dir — one file at runtime, no import magic to
debug — and records its hash (§5.4), so both generic-seed evolution and
hand-edits are detected rather than clobbered. The existing hand-edit
freedom (ovs's "diff before replacing" rule) is preserved: drift is
surfaced, never auto-resolved.

### 6.4 Worker environment setup — the full capability surface

**Decision (2026-07-03): the wizard defaults to a dedicated worker
profile** (its own `CLAUDE_CONFIG_DIR`, the generalized `~/.cc-work`
pattern), with "share my main profile" as the explicit opt-out. Isolation
is what prevented the conventionless-workers incident class; the one extra
wizard step is worth it.

**Worker autonomy is an explicit wizard choice, never a silent default
(design review I-4).** ovs runs workers under
`--dangerously-skip-permissions` safely *because* the dev-safety plugin's
PreToolUse guards ride in the ccwork profile — a no-pack OSS user has no
such layer. So the wizard presents autonomy as an affirmative decision:
*full autonomy (skip-permissions — recommended only with a safety-guard
plugin or on repos you can afford to reset)* vs *prompting (safe default
posture, but workers block on permission prompts)*. The Grid pack's
worker-env declares dev-safety, so Grid users get full autonomy with
guards; the generic wizard copy names the trade-off and records the choice
in config. A minimal core safety-guard hook (deny-list shaped: no `rm -rf`
outside the worktree, no pushes to default branches) is a candidate for
core — decide during Phase 1, and carry the same revisit-before-public
flag as the trust gate.

The `worker-env` slot turns ONBOARDING.md §4 from prose into wizard actions:
create the config dir, register marketplaces, install plugins, verify the
alias, then walk the one-time MCP OAuth ("open a session, call a tool once")
interactively. Each action is also a connection, so the doctor re-verifies
it forever and a teammate's missing plugin is a reconnect prompt, not a
conventionless worker (the incident that created `ovs doctor`).

**Parity is the whole capability surface, not just worker conventions.**
What makes Dean's setup feel effortless is everything the ccwork profile
carries: the Linear MCP (backlog exploration, transitions, *comment posting
under the confirmation guardrails*), the grid-search MCP (Grid data
queries), slite (team docs), the diagnostics skills
(accessing-database, running-local-stack, deploying-services), the safety
guards. The Grid pack must therefore declare the **complete plugin/MCP
inventory** of today's working ccwork profile — the parity audit in §8.0
enumerates it from the live machine rather than from memory, so nothing
Dean actually relies on is silently missing from the declaration. The
orchestrator session runs under the same profile, so its capability surface
(Linear MCP triage, diagnostics on request) comes from the same
declaration.

### 6.5 Sharing with the team

Grove's succession goal (Dean, 2026-07-03): overstory was the solo trial
run; grove is the version teammates adopt. The onboarding story a pack
enables:

1. **Install the binary** — a release channel (`brew install` /
   `go install` from a tagged release), not "clone my repo and build" —
   plus `gv init`.
2. `gv init` with `pack: <grid pack ref>` → the wizard executes the
   entire worker-env + connections setup that ONBOARDING.md currently
   narrates, on *their* machine, with *their* auth.
3. From then on, drift is self-healing: their doctor and reconnect prompts
   derive from the same pack declaration, and pack updates (new
   plugin, new convention) arrive as a version bump + doctor warning.

Note the Grid wrinkle: the workspace root (`~/git/thegrid/`) is not itself
a git repo, so parent-scope workspaces can't rely on a *committed*
`.grove/config.yaml` for team sharing — **the pack is the shared-config
channel** for that shape; committed workspace config covers repo-scope
teams. Fleet *state* stays per-machine by design (each teammate runs their
own fleet); shared visibility across teammates' fleets is explicitly out of
scope for v1.

---

## 7. Config layering (extends grove-spec §6.5.1)

Named layers only — no ESLint-style invisible cascade. Precedence, highest
first:

1. CLI flags
2. `<root>/.grove/config.local.yaml` — personal (ntfy topic, editor, model
   dial); auto-gitignored at creation
3. `<root>/.grove/config.yaml` — committed team contract (provider, repos,
   lifecycle scripts, pack pin, doctor excludes)
4. **pack** — the pinned pack's defaults
5. `~/.config/grove/config.yaml` — user defaults across workspaces
6. built-in defaults

Secrets: never in any grove file. Env-var indirection with documented names
(the `api_key_env` pattern — it earned its keep on day one); delegation
where a native credential exists (`gh auth token`, never a stored copy).
Committed config may name *which* env vars are required — that's a
connection declaration, so the doctor checks it.

---

## 8. Parity acceptance test

The bar (Dean, 2026-07-03): *"I can use grove instead of overstory, and it
still has full use of the grid linear for project management needs, comment
posting, grid skills, grid mcp, and other tools I use for grid diagnostics
across the system — recreating this exact familiarity without it being
hardcoded in, purely through wizard/setup."*

Grove + Grid pack is at parity when, side-by-side against ovs on a real
ticket, a fresh machine reaches identical behavior with **zero Go edits and
zero manual file edits** beyond running the wizard:

0. **Capability-surface audit first**: enumerate the live ccwork profile
   (installed plugins, marketplaces, MCP servers + auth state, skills) and
   the live orchestrator's toolset on Dean's machine; the Grid pack
   declaration must cover 100% of it. This is done by inspection of the
   working machine, not from memory — the audit *is* the pack's first
   draft.
1. `gv init` on `~/git/thegrid` (parent scope) with `pack: grid` → all
   ONBOARDING.md §4 steps executed/verified; doctor output covers today's
   doctor line-for-line; 🌳.
2. `gv grab DEV-X --repo monorepo` → byte-comparable kickoff prompt to
   `ovs grab` (frame + verbs + fragments compose to the same text).
   **Byte-comparison is defined against an empty learnings corpus** (a
   fresh machine renders an empty learnings block); once learnings exist,
   the injected block is an accepted, documented delta — it's grove's
   *addition over* ovs, not a parity deviation. (Design review I-1.)
3. Worker lifecycle identical: In Progress via dev-linear MCP, wrap-up via
   `wrapping-up-task` + `pr-reviewer`, sentinel classified, mail/push
   identical.
4. Orchestrator parity across the duty list: fleet summary, Linear backlog
   triage via MCP, ticket sharpening, cost analysis — and the *guardrailed
   mutations*: Linear comment posting and status corrections happen through
   the orchestrator/agents with explicit confirmation, exactly per today's
   team rules (grove's binary still never mutates the backend). Composed
   CLAUDE.md ≡ today's, modulo `ovs`→`gv`.
5. Diagnostics familiarity: from an orchestrator or manual session spawned
   by grove, the Grid diagnostic surfaces (grid-search MCP queries, slite
   lookups, accessing-database / running-local-stack skills) work exactly
   as they do in today's ccwork sessions.
6. Deliberately break each Grid connection (unset key, remove a plugin,
   break the symlink, revoke MCP auth) → grove notices at the next relevant
   verb and names the scoped fix.

Until this passes, ovs stays frozen and daily-driven (grove-spec §12) —
after it passes, grove takes over and ovs retires (succession, not
coexistence, is the goal).

---

## 9. FMA

| Risk | Criticality | Mitigation |
|---|---|---|
| Pack executes hostile code paths (fix commands, lifecycle scripts) from a cloned repo | **Critical** | **Accepted, deliberately, for the private/solo phase** (decision 2026-07-03: no trust gate — ovs's "you configured it, you own it" stance). Mechanical fixes stay whitelisted per connection kind; auth fixes never auto-run. **Must revisit before public release / first external workspace clone** — the seeded-file hash machinery (§5.4) makes a direnv-style gate cheap to add later. |
| Manifest-driven wizard/doctor drifts from reality (checks pass, workers still broken) | Important | The failure-signal channel (§5.3) is the backstop — real worker blockage marks connections degraded regardless of what probes claim; parity test §8.5 exercises deliberate breakage. |
| Connection checks slow down hot verbs (`grab`, `ls`) | Important | Per-kind TTL caches; free checks only on hot paths; subprocess checks amortized; `mcp-auth` never probed proactively. |
| Kickoff assembly produces subtly different prompts than ovs's proven templates | Important | Byte-comparison acceptance test (§8.2) against ovs's rendered output for the Grid pack; whole-template override survives as escape hatch. |
| Composed orchestrator CLAUDE.md loses hand-written customizations | Important | Seed-hash drift detection + `gv sync --diff`; never auto-overwrite (existing ovs rule, now enforced by machinery instead of discipline). |
| Degraded-state banners become noise (brew-doctor wolf-crying) | Acceptable | error-vs-warning severity per connection; committed excludes; banner shows only actionable degradations with one fix line. |
| Pack versioning skew (team updates pack, user pinned old rev) | Acceptable | Pin + explicit `gv pack update`; doctor notes when the pin is behind the remote (warning, not error). |
| OSS users without any pack get a worse experience than Grid users | Acceptable | Core defaults are a complete no-pack experience (markdown provider, generic kickoff, generic orchestrator); packs only add. |

---

## 10. Open questions

1. **Manifest merge semantics** — when pack and workspace config both
   declare a connection with the same name: override wholesale, or
   field-merge? (Lean wholesale-override + doctor warning; field-merge
   invites spooky action.)
2. **`worker-env` plugin installs** — should the wizard *run*
   `claude plugin install` itself (mutating a Claude profile it doesn't own)
   or print-and-confirm each command? Propose-then-run-on-confirm matches
   the house rule; `--yes` covers automation.
3. **TTL defaults per kind** — 24h for `cli-auth` is a guess; measure how
   often expired-auth actually bites vs how annoying re-checks are.
4. **Blocker-pattern quality for §5.3** — connection-shaped classification
   of `BLOCKED` reasons will have false positives/negatives; start with
   conservative patterns + orchestrator judgment ("this looks like an auth
   failure — reconnect?") before hard-coding more.
5. ~~**Pack distribution for the Grid**~~ **Resolved (2026-07-03):** a
   subdirectory of the existing workspace marketplace repo — one
   distribution channel the team already has (§6.1).
6. ~~**Trust gating**~~ **Resolved (2026-07-03):** no trust gate — ovs's
   stance carries over; revisit before public release (§5.4, FMA row 1).
7. ~~**Naming**~~ **Resolved (2026-07-03):** the overlay concept is a
   **pack**; "profile" is reserved for Claude Code profiles. "Connections"
   stays the manifest concept name.

---

## Appendix — research sources

**Wizards:** gh auth login/refresh (cli.github.com/manual) · firebase init ·
terraform init · turborepo package-manager inference · nx init ·
create-t3-app CI flags · Backlog.md `backlog config`
(github.com/MrLesk/Backlog.md) · task-master `models --setup`
(docs.task-master.dev).

**Doctor:** flutter doctor ValidationType taxonomy
(docs.flutter.dev/install/troubleshoot) · brew doctor temperament ·
expo-doctor committed excludes (docs.expo.dev/versions/latest/config/package-json).

**Layering:** Claude Code settings hierarchy · Codex CLI config + trusted
projects (developers.openai.com/codex/config-basic) · mise config + trust
(mise.jdx.dev/configuration.html) · git includeIf · ESLint flat-config
lesson · EditorConfig.

**Drift:** direnv allow (content-hash) · VS Code Workspace Trust /
Restricted Mode · AWS SSO / gcloud reauth UX · gh issue #8846
(cheapest-remedy) · terraform doctor-on-failure.

**Profiles:** pre-commit hooks contract (pre-commit.com) · gh extensions
auth inheritance · ESLint shareable configs · nix flakes devShells ·
Conductor conductor.json lifecycle scripts (conductor.build;
afomera.dev/posts/2026-02-03-using-conductor-with-ruby-on-rails) · Claude
Code plugin marketplaces (the Grid's own workspace repo).

**Peers:** Claude Squad config/`cs debug` (github.com/smtg-ai/claude-squad) ·
Warren/os-eco dot-dir primitives (github.com/jayminwest/os-eco) · Vibe
Kanban profiles.json overlay (github.com/BloopAI/vibe-kanban).
