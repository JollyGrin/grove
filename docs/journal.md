# Grove — journal

> The **why** log: dated narrative of why an era of work happened — the
> field report, the reasoning, the sequencing decisions, the outcome. One
> entry per arc, newest first, linking to the tickets/PRs/plan docs that
> carry the details.
>
> Charter: this file is deliberately **not** loaded into worker or
> orchestrator context (no CLAUDE.md reference). It exists for the agent
> or human who hits a [LEARNINGS.md](../LEARNINGS.md) entry or a
> [TASKS.md](../TASKS.md) row and needs its backstory. Facts go in
> LEARNINGS.md; status goes in TASKS.md; *why* goes here.

## 2026-07-29 · The hygiene arc: making the dash cheap enough for other people's machines

Grove's first real external field report was a complaint: `gv dash` pegged
a user's CPU with just 1 orchestrator + 5–6 workers, and their read was
"not dash, tmux is what creates load." Dean's larger fleet on macOS felt
nothing — which is exactly why the report mattered. The dash's cost is
`spawns/sec × cost-per-spawn`, and grove had only ever been run on
machines where the second factor is tiny. On hosts with EDR scanning
every exec, or WSL1's slow forks, the same loop is ~50x more expensive.
Diagnosis confirmed the reporter's symptom split precisely: the dash pays
the fork/exec (shows as `gv` in top), the tmux server pays the
client-connect flood (shows as tmux load).

Two tickets already existed from the 2026-07-18 farewell audit and turned
out to be the two halves of the same hot path, so they ran as a merge
train rather than in parallel (both rewrite `refreshCmd`; #126's tmux
bullet was a strict subset of #149):

1. **[#149](https://github.com/JollyGrin/grove/issues/149) → PR #152** —
   the tick spawned ~6 tmux processes per worker per second (3×
   `list-windows` + `list-panes` + `display-message` + `capture-pane`),
   re-asking one shared session the same questions per task. Batched into
   one `SessionSnapshot` (one `list-windows` + one `list-panes -s` per
   session per tick); only `capture-pane` stays per task. 6N+2 → ~N+3
   spawns/sec.
2. **[#126](https://github.com/JollyGrin/grove/issues/126) → PR #153** —
   the same tick parsed the append-only events.jsonl **twice** in full
   and rewrote tasks.json to disk every second — costs that grow with log
   age forever, since there's no compaction. The incremental
   `state.Folder` parses only appended bytes (one pass answers both the
   task map and the feed tail), tasks.json writes only when the folded
   view's hash changes, and stop hooks cap stored messages at 2000 runes
   so the log grows slower in the first place. Plan doc:
   [2026-07-29-grove-126-hot-path-state-io.md](plans/2026-07-29-grove-126-hot-path-state-io.md)
   (includes the deliberate *defer* on the `gv ls` on-disk cost cache).

Post-merge measurement on the live dash: tasks.json mtime frozen for the
whole observation window (was: one write per second), dash CPU at
0.2–0.4%. The distilled rules live in LEARNINGS.md §Field notes
(2026-07-29 entries); the deeper "what changed where" is in
[seed-manifest.md](seed-manifest.md) rows grove-149/grove-126.

Deliberate leftovers, all tracked: tmux control-mode client and a
configurable refresh interval stayed follow-up ideas in #149's non-goals;
reader-side truncation is #123; the reporter's environment questionnaire
(#149's open questions) decides whether a relief valve is ever needed.
