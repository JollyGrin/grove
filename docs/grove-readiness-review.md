# Grove doc-corpus readiness review — are we ready to start the repo?

> **Status: review, 2026-07-03.** Answers the operator's three questions: (1) are
> these docs sufficient for creating the new repo from scratch, taking the
> best from overstory? (2) do they cover the acceptance criteria — grove
> drop-in anywhere, wizard to master-orchestrator, full Grid familiarity
> with zero hardcoding? (3) what's missing?

---

## 0. The corpus under review

| Doc | Covers | State |
|---|---|---|
| [grove-spec.md](../DESIGN.md) | Founding spec: 3 locked decisions, architecture, `TaskProvider`, workspaces/switcher, routing, swarm shape, OSS-readiness, state/config, command surface, phasing, FMA, decision log | Mature; OQ2 + OQ6 now resolved |
| [grove-cockpit-design.md](grove-cockpit-design.md) | UX layer: AGENTS+ACTIVITY left pane, stacked orchestrators, spawn plumbing, mail/review panel removal | Mature draft; the operator's answers to §10 sit inline, not yet promoted to decisions |
| [grove-connections-design.md](grove-connections-design.md) | Wizard, doctor, drift/reconnect, connections manifest, pack system, Grid parity mapping + acceptance test, team sharing | New (2026-07-03) |
| [grove-learnings-design.md](grove-learnings-design.md) | Layered memory: 6 scopes, deterministic activation-filtered retrieval, capture triggers + skill, curation, promotion, lint, prior-art research | New (2026-07-03) |
| ovs itself (`DESIGN.md`, `LEARNINGS.md`, `TASKS.md`, source) | The reference implementation — ~90% of grove's plumbing, field-tested | The living proof; not going anywhere |

Together: the **what/why layer is complete and internally consistent**. The
four docs cross-reference cleanly, the decision logs don't contradict each
other, and every major subsystem (provider, router, bootstrap, connections,
cockpit, learnings, state) has an owner doc.

---

## 1. Q1 — sufficient to create the repo from scratch?

**As the design corpus: yes. As the only input to start coding: not yet.**
Three things stand between these docs and a productive first commit, all
deliberate next steps rather than doc defects:

1. ~~**The name**~~ **Resolved in the 2026-07-03 interview (§5):
   grove / gv, `github.com/JollyGrin/grove`.**
2. **An implementation plan.** These docs are deliberately what/why
   (grove-spec's own header says so). The house flow applies: run
   `writing-plans` against this corpus to produce the numbered Phase-0 plan
   (and plan-reviewer it) before any code. The phasing skeleton
   (grove-spec §13) is the plan's table of contents, not the plan.
3. **A seed manifest** — the concrete "take the best from overstory" list.
   §4 of the spec names *packages*; a builder needs the file-and-knowledge
   level. First draft:

   **Copy near-verbatim** (per spec §4): `internal/{tmux, git, worktree,
   detect, transcript, state, cost, audit, hooks, tui, github}` (mail is
   part of `state`, not its own package — corrected per design review
   I-7; re-verify the whole list against the live tree at scaffold time),
   `cmd/ovs/main.go` structure (thin-glue pattern), the dummy-data E2E
   recipe (currently §Dummy-data E2E of
   `docs/plans/2026-07-02-cleanup-and-adopt.md` — **content must be copied
   into the new repo's docs**, since ovs's plan docs won't travel).

   **Generalize while copying**: `internal/config` (layering per
   connections-design §7), `internal/doctor` (manifest-driven per §3–5),
   `internal/kickoff` (assembly per §6.2), `internal/linear` (→ provider
   adapter), `orchestrator/CLAUDE.md` (generic + overlay slot).

   **Seed the new repo's LEARNINGS.md** with ovs's *generic* entries — the
   hard-won facts that live half in code and half in LEARNINGS.md, which a
   fresh repo's agents would otherwise re-break: SendKeys is single-line;
   `worktree.Add` one-string; squash-merge defeats `branch -d` (check via
   `gh pr view`); pane detector spinner/chrome drift; `EncodePath` +
   transcripts keyed on encoded cwd; sessions-index unreliable → always
   explicit `--resume <id>`; hook payload facts (`session_id`/`cwd`/
   `last_assistant_message`, realpath'd cwd); resume survives ≥6 days with
   same session_id; tmux window-name prefix-matching; stdlib flag
   stops-at-first-positional; `claude -p` fires SessionEnd after Stop;
   plugins install at user scope; ccusage dedup + cache-cost asymmetry.
   The Grid-specific entries (Linear pipeline, monorepo main→prod, codegen
   copy) go to the **Grid pack's L5 learnings layer** instead — the
   first real content of grove-learnings-design's ladder.

With those three in hand, the repo can be created and Phase 0 executed
without re-deriving anything. Nothing else in the corpus blocks starting.

---

## 2. Q2 — acceptance-criteria coverage map

the operator's criteria, mapped to where each is designed:

| Criterion | Covered by | Confidence |
|---|---|---|
| Drop into any repo, or a parent folder of repos | spec §6.1 (probe), §6.5 (workspace shapes, ambient walk-up, nesting rule) | High — mirrors proven git/.grove semantics |
| Sane defaults, wizard asks only relevant questions | connections-design §4 (detect-then-confirm, flags-for-everything, scoped re-runs, init-as-repair) | High — every mechanism has named prior art |
| Master orchestrator of the project | spec §3/§8 + cockpit doc (the proven ovs loop, generalized) | High — it's a port, not an invention |
| Full Grid Linear use: PM, transitions, **comment posting** | connections-design §8.4 — via MCP under confirmation guardrails; binary still never mutates (hard rule preserved) | High |
| Grid skills + Grid MCP + diagnostics tools | connections-design §6.4 (worker-env = full capability surface) + §8.0 (audit the live machine, not memory) + §8.5 | High on mechanism; the audit itself is a to-do |
| Exact familiarity, zero hardcoding, purely wizard/setup | §1 parity inventory → §6.1 slot mapping → §8 acceptance test incl. byte-comparison of kickoffs | High — every hard-coded surface has a named slot |
| Grows with people and projects | learnings-design (all) + connections-design §6.5 (team onboarding, pack as shared channel) | Medium-high — newest design, least field-tested; ships grove-only per the 2026-07-03 decision |

No criterion is un-covered. The two Medium spots are honest ones: the
capability-surface audit (§8.0) hasn't been *run* yet, and the learnings
system is designed but unproven — which is exactly why it has an ovs
backport candidate carved out (learnings-design §4).

---

## 3. Q3 — what the review found missing

### 3.1 Patched during this review (already in the docs)

- Parity redefined as the **whole capability surface** (MCPs, comment
  posting, diagnostics), with the live-machine audit as step 0 —
  connections-design §6.4/§8.
- **Team-sharing story** (binary release channel, pack-as-shared-config
  for non-git parent workspaces, per-machine state) — §6.5.
- **Succession framing** (grove replaces ovs after the gate; trial-run →
  successor) — spec §12.
- **Deterministic learnings retrieval** (activation metadata + router-signal
  reuse + `gv learn why` audit) and **capture triggers** (redundant /
  outside-expectations criteria, the `learn` skill, fleet-level redundancy
  detection) — learnings-design §3.3/§3.4.

### 3.2 Remaining gaps — small, none blocking, each with a recommendation

1. **Coexistence + migration mechanics** (the only *operational* gap).
   During the trial window both `ovs hook` and `gv hook` will fire on the
   same worker sessions (harmless — each exits silently for untracked cwds —
   but should be stated and smoke-tested), and the operator's live fleet state
   (`~/.local/state/overstory/`) never migrates: the cutover rule should be
   *drain in ovs, start new grabs in grove; `gv adopt` is the escape hatch
   for anything long-lived*. → one short section in the Phase-0 plan; not
   worth a doc of its own.
2. **The generic orchestrator CLAUDE.md doesn't exist yet.** The overlay
   *mechanism* is resolved (spec OQ6), but nobody has written the
   de-Gridded duties text (what "backlog triage" reads like for the
   markdown provider). → a Phase-4 writing task; flag it in the plan so it
   isn't discovered late.
3. **Portability assumptions inherited from ovs.** The doctor probes worker
   aliases via `zsh -ic whence` (assumes zsh); notifications assume
   `terminal-notifier` (macOS); ONBOARDING says "portable but untested."
   For an OSS tool: probe `$SHELL` interactively instead of hardcoding zsh,
   make desktop notification a connection with per-OS fixes, state
   Linux/tmux as tested-tier-2. → belongs in the connections manifest's
   core declarations; one paragraph in the plan.
4. **Default worker isolation for OSS users.** ovs's separate `ccwork`
   profile (`CLAUDE_CONFIG_DIR`) looks Grid-specific but is generically
   valuable (workers' plugins/auth never collide with the user's personal
   setup) — yet mandating it adds first-run friction. → recommend: wizard
   offers "dedicated worker profile (recommended) / share main profile,"
   default dedicated; record as a spec decision.
5. **Cockpit doc housekeeping.** the operator's answers live inline in §10 (pane
   titles: yes; drop popovers; persist proposals: yes; bare `ovs` opens
   cockpit: yes; >4 orchestrators eat the left column; activity feed must
   survive re-attach; want per-chat state indicators — that last one is a
   real small feature: chat-pane status in the AGENTS/session list). →
   promote to a decisions section; the state-indicator wish should become a
   line item in the cockpit phasing.
6. **Learnings write-contention.** Multiple simultaneous `gv learn` calls
   (N workers finishing together) hit the same inbox — fine, it's
   `events.jsonl` (O_APPEND + flock), but the *files* under
   `.grove/learnings/` are only ever written by the curation step
   (human-gated, serial), which should be stated as the invariant: **agents
   write events, only curation writes files.** → one line in
   learnings-design §3.5; noted here so it lands in the plan too.
7. **Formal design review not yet run.** This corpus grew via investigation
   sessions, not the brainstorming→design-reviewer gate. Before
   `writing-plans`, run the `design-reviewer` agent over grove-spec +
   grove-connections-design + grove-learnings-design as a bundle — cheap
   insurance, and the house flow expects it.

### 3.3 Judged out of scope (deliberately, say no)

- **Shared fleet visibility across teammates** — each teammate runs their
  own fleet; v1 stays per-machine (stated in §6.5).
- **Public learnings commons** — parked (learnings-design OQ6).
- **Non-Claude worker runtimes** — AGENTS.md portability keeps the door
  open; core targets Claude Code (locked decision 2).

---

## 4. Where we are — the path from here

```
[done] grove-spec (founding) → cockpit design → connections design → learnings design
[done] decision interview (2026-07-03, §5 below) — all blockers cleared
[done] design-reviewer pass: APPROVE_WITH_FIXES (0 Critical / 7 Important) — all fixes applied
[now ] scaffold github.com/JollyGrin/grove (docs + skeleton + verbatim package copy)
[then] fresh agent in the new repo: writing-plans (Phase 0) → plan-reviewer → execute
[then] Phase 1 bootstrap (init/doctor/connections) → … → §8 parity test → ovs retires
```

**Design-review outcome (2026-07-03): APPROVE_WITH_FIXES, all applied.**
I-1 parity gate defined against an empty learnings corpus (connections §8.2)
· I-2 local-md write-location rule: event-state authoritative in flight,
frontmatter durable on merge (spec §5.2) · I-3 offline claim qualified —
local-md replaces the tracker, not the delivery loop; no-remote degraded
path designed in Phase 0 (spec §5.2) · I-4 worker autonomy is an explicit
wizard choice, never a silent skip-permissions default without a safety
layer (connections §6.4, spec §9) · I-5/S-1 phasing redrawn with 1a–1c
bootstrap sub-phases and a lean learnings Phase 5 (spec §13) · I-6
dual-hook coexistence contract: task-ownership decides "not my session,"
not workspace resolution (spec §12) · I-7 `internal/mail` manifest error
corrected (mail lives in `state`) · S-2 cockpit doc rescoped grove-only ·
S-3 registry concurrency stated · scope nits fixed.

**Verdict: solid.** The corpus answers what grove is, why every piece is
shaped the way it is, how the Grid becomes pure config, and how the system
compounds — with named prior art behind each mechanism and a falsifiable
parity gate at the end. All interview-level decisions are made (§5); what
remains is process (review → scaffold → plan) and the plan-level line items
of §3.2. Nothing structural is missing.

---

## 5. Decisions locked — the 2026-07-03 interview

| # | Decision | Answer |
|---|---|---|
| 1 | Name / binary | **grove** / **gv** |
| 2 | Repo | **github.com/JollyGrin/grove**, private until parity |
| 3 | Scaffold depth | Docs + skeleton **+ verbatim copy of the generic packages** now; new agent takes over from writing-plans |
| 4 | Review gate | **design-reviewer pass in this session**, before scaffold |
| 5 | Worker isolation | **Dedicated worker Claude profile by default**; share-main is the opt-out |
| 6 | Trust gating | **None** (ovs stance) — Critical-flagged to revisit before public release |
| 7 | Grid pack home | **Subdirectory of the workspace marketplace repo** |
| 8 | Providers | **Native Go** for markdown / github-issues / linear; MCP-shaped for the long tail |
| 9 | L2 learnings file | **LEARNINGS.md** + `AGENTS.md` pointer line |
| 10 | L3 on the Grid | **Upstream PRs to the workspace marketplace only**; no grove-owned L3 file when a marketplace exists |
| 11 | Orchestrator spawn | **`--dangerously-skip-permissions` by default** (guardrails are CLAUDE.md-based) |
| 12 | Learnings trial | **Grove only** — no ovs trial; freeze guarantee stays strict |
| 13 | Distribution | **goreleaser + tagged releases from day one** |
| 14 | Terminology | Overlay concept = **pack**; "profile" reserved for Claude Code profiles |
| 15 | Deferrals | Confirmed: github-issues transitions (Phase 3), repo-map (measure in Phase 1) |
