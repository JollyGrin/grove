---
name: feature-scoping
description: Use when an operator brings a feature idea, goal, or claimed problem to an orchestrator chat and wants it scoped into a design a drive chat can run — the required mechanics that keep the session from being a yes-man ceremony, the design doc sections a feature train needs (Done when, Decisions, cars), and the handoff to `gv feature drive`. Not for a single ticket (use ticket-writing).
---

# Feature scoping

A scoping session ends with a design the operator signs once and a drive
chat runs without them. The operator's attention is spent here and at the
final PR review, and nowhere in between. The chat's job is to make the
design true, not to make the operator feel agreed with. The journey this
sits in is docs/plans/2026-10-24-feature-drive-design.md §The journey.

## Required mechanics — every one, every session

1. **Verify every premise before the problem statement accepts it.** The
   operator's claims and your own alike. "X is frozen", "nothing reports
   Y", "this happens on every host" each get a tool result (grep, diff,
   `gv … --json`, `gh`) before they appear in the design, and the design
   says how each was checked. A premise that did not survive is said
   plainly, in one sentence, and the design is built on what did.
2. **At least two real alternatives, with costs.** Not the operator's idea
   beside a strawman. A table: what, operator cost, risk, verdict. One
   recommendation, with the line "rests on: …" and the line "would change
   if: …". If the recommendation is not the operator's preferred approach,
   say so first, then why.
3. **`## Done when` before cars.** Every line is checkable by a command or
   a test, in a scratch workspace from a throwaway build, never against a
   real host, config dir, or workspace. Post-merge operator steps are
   listed in their own subsection as not part of done. A line you cannot
   make checkable is a design gap, not a line.
4. **Cars derived from Done when.** Each car's acceptance lines cite the
   Done-when line they discharge; sizing, sequencing and the rote test per
   the ticket-writing skill. The chain is written as `depends on #N`.
5. **`## Decisions` settled or listed.** Every question a worker or a
   drive could plausibly ask is either decided here, in the one-line form
   the drive brief's Pre-answered block uses, or listed under `## Open
   questions for the operator` with a recommendation. An unsettled
   decision is a push during the drive, the expensive place to settle it.
6. **An independent reviewer that never saw this conversation.** A fresh
   read-only agent (`Agent`, fresh context, no transcript) gets the design
   file and the code and is asked for blockers, majors and minors with
   what each rests on (file:line or command output), and to test every
   premise row, every hard rule, every contract change and every
   Done-when line for checkability. Fold the findings in, write the `## Review log`
   (one line per finding and what changed), and only then ask the
   operator for their yes. The brain-overlay review found two blockers the
   conversation had missed; a reviewer who read the chat inherits its
   blind spots.
7. **Say no.** When the goal is several features, say which and propose
   the first; when the code already meets it, show the evidence and stop;
   when a premise fails and the approach rested on it, say the approach
   is now unsupported. A design that papers over any of these is the
   expensive failure.

## The design doc

`docs/plans/YYYY-MM-DD-<slug>-design.md`, in this order:

- Header: **Status**, **Goal**, **Shape** (merge train or feature train,
  and why — ticket-writing's decision rule).
- `## The problem in one paragraph` — the operator's words, each premise
  verified (§1); a `## Verified premises` table when there are more than
  two.
- `## Options compared` (§2).
- `## The design` — numbered decisions, one heading each, the rule or
  contract each one keeps.
- `## Contract changes (all additive)` — every `--json` field and event
  (docs/plugins.md is the tripwire).
- `## Rules this keeps` — against CLAUDE.md §Hard rules.
- `## Done when` (§3) with its `### Operator step` subsection.
- `## Decisions` (§5).
- `## Tickets` (§4) — drafted here, filed only on the operator's yes. For
  a train this section IS the plan (CLAUDE.md's design → plan → execute);
  the reviewer in §6 reviews it as one.
- `## Open questions for the operator` — with recommendations.
- `## Review log` (§6).

## Conversation shape

Open by restating the goal in one sentence and listing the premises you
are about to check. Check them, report what held. Then options, then a
recommendation. Draft Done when and ask the operator to strike or add
lines — this is where their judgment is worth the most. Derive cars. Run
the reviewer. Present the review log and ask for the yes. Keep each turn
to one step; a session that delivers a finished design in one message has
skipped the mechanics.

## Handoff

On yes: file the cars (`gh issue create` with the feature label and
`depends on` lines; check open issues under the label first so nothing is
filed twice), `gv feature new <slug> --repo R --design <path>` (until that
flag ships, note the path in the design header), then `gv feature drive
<slug>`, which prints the grants and caps and launches on the operator's
own typed yes — that second yes is the issue-close authorization, and the
scoping chat never gives it for them. Until `drive` ships: copy
`docs/briefs/feature-drive.md`, fill it from Done when and Decisions, show
the operator the grants, and launch with `gv orchestrator new
--brief-file` on their yes.
The drive chat must start fresh: this session's context is the rejected
alternatives, which is exactly what the drive must not inherit. End the
session by naming the brief file and the chat session id.
