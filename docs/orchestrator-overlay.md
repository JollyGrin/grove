# The orchestrator overlay: `CLAUDE.local.md`

An orchestrator dir (`.grove/orchestrator/` in a workspace, or the global
`cfg.Orchestrator.Dir`) holds two instruction files with two owners:

| File | Owner | What grove does with it |
|---|---|---|
| `CLAUDE.md` | grove | The stamped seed, byte-identical to what the binary embeds. `gv brains --refresh` replaces it in place after every `gv update` while it is pristine (its stamp is the hash of its own body). Never edit it. |
| `CLAUDE.local.md` | you | The workspace's overlay: glossary, overrides, addenda, extra duties. Grove writes it exactly once, as an empty stub on the first seed, and never rewrites it. |

Claude Code loads `CLAUDE.local.md` right after `CLAUDE.md` in the same
directory, from the orchestrator dir and from every profile subdir under
it (`zai-plan-glm-flash/` and friends), with or without git, and
re-injects both after `/compact`. Block HTML comments are stripped before
either file reaches the model, which is why the stub costs zero context
and why `gv brains` counts a comments-only file as no overlay
(`overlay: false, overlay_lines: 0`). Put the overlay in `.gitignore`
unless the workspace deliberately tracks it (thegrid, waterhouse); grove
itself never commits it.

Design: `docs/plans/2026-10-19-brain-overlay-design.md` (issue #508).

## Why the convention matters

Claude Code promises no precedence between instruction files: "if two
instructions contradict each other, Claude may pick one arbitrarily." Two
things make an overlay win anyway. The seed says so and names a floor
(below), and the overlay **names what it overrides** so the model can tell
a deliberate replacement from an accidental contradiction. Live probes
(design §What Claude Code supports, behavioural check) showed an override
written this way won 10 of 10 runs, and the rule it did not touch held.

## The floor no overlay lowers

An irreversible or outward-facing action still needs the operator's
explicit yes in this chat, or a mandate or land order, whatever the
overlay says. Those actions are merging a PR or a branch, closing an
issue, `done`, `untrack`, `adopt`, `sweep`, `handoff`, and any `grab` the
operator did not ask for. An overlay may add a standing pre-authorization
for one of them only by naming it explicitly as one. This is the repo's
"propose, then dispose" hard rule, enforced only by brain text, so the
seed states it and every overlay sits under it. A seed rule the overlay
does not name still applies in full.

## Shape

Four sections, in this order. Leave a section out when it is empty; the
stub keeps all four as comments so the headings are there to uncomment.

1. **Glossary** — names and ids the seed writes generically: who the
   operator is, what a task id looks like, which backend is live.
2. **Overrides** — one bullet per seed rule you replace, each starting
   `**Override — <seed section> › <rule>:**`. Quote the seed rule by its
   section and bullet title, state the replacement, and restate what
   stays forbidden. That last clause is what keeps the neighbouring rule
   intact.
3. **Addenda** — extra steps hung on an existing duty, each starting
   `**Addendum to duty N (Name):**` with the duty's number and name as
   the seed numbers them.
4. **Workspace duties** — duties the seed has no slot for, each starting
   `**Workspace duty: <Name>**`.

## Example: unbrewed

```markdown
# Workspace overlay: unbrewed

## Glossary
- The operator is Dean. Task ids look like `unbrewed-pro-server-17`
  (repo + issue #); wherever the seed writes `<task>`, use that form.
- Backend: GitHub issues on unbrewed-p2p and unbrewed-pro-server. No Linear.

## Overrides
- **Override — Guardrails › Never post ticket comments:** posting
  investigation findings as comments on unbrewed issues is allowed
  without asking (Dean's standing OK, 2026-07-05). Closing an issue
  stays forbidden.

## Addenda
- **Addendum to duty 6 (Cleanup & recovery):** after a merge, check the
  issue closed; if it did not, say so in the chat and wait for the word.

## Workspace duties
- **Workspace duty: Local pair testing** — when a car touches the p2p
  handshake, run both peers locally (`pnpm dev` in two shells) before the
  PR, and paste the pairing log in the PR body.
```

## Moving today's edits across

A brain edited in place still works: a seed move lands `CLAUDE.md.new`
beside it for the usual hand merge, and `gv brains` says "move your edits
to CLAUDE.local.md: `gv brains --migrate`". The migrate verb (brain-overlay
cars 03 and 04) classifies the edit against the seed archive and proposes
the overlay; until it lands, write the overlay by hand in the shape above
and then let `gv brains --refresh` replace the brain once it is pristine
again. Editing `CLAUDE.md` after migration reopens the hole; the overlay
is the only file to touch.
