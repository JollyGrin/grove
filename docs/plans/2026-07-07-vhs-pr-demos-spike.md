# vhs-recorded TUI demos for PR review — spike findings (grove-7)

**Verdict: GO.** vhs records the tmux-attached grove cockpit headlessly,
the full question→answer scenario is watchable in an 11-second, 244 KB
GIF, and final-frame output is deterministic to within one header line.
Recommend a follow-up build ticket (scope at the end).

Sibling effort: issue #2 (`demos/`, asciinema+Remotion) makes polished
*tutorials*; this pipeline makes disposable *per-PR review evidence*. They
share the isolation recipe but nothing else — keep them separate.

## 1. Go/no-go: tmux inside vhs

**GO — it just works.** The critical unknown was `gv` (bare) building a
tmux session and exec-ing `tmux attach` inside vhs's ttyd pty. Verified:
the cockpit renders fully — AGENTS table (status glyphs, QUESTION row
selected), ACTIVITY feed, orchestrator pane, tmux status bar — and
`Wait+Screen /regex/` synchronizes against cockpit content reliably.
Evidence: `tapes/out/spike-cockpit.gif` (and every frame of the scenario
tape). No `TERM` fiddling, no `Set Shell` tricks, no attach variants
needed. The asciinema fallback was not needed.

Two real traps found on the way, neither vhs's fault:

- **`$TMUX` beats `TMUX_TMPDIR`** — the recorded shell inherits `$TMUX`
  when the recording is launched from inside a tmux pane (i.e. by every
  grove worker), which silently redirects the "isolated" server, gv's
  attach (→ `switch-client`, yanking the operator's terminal), and the
  cleanup `kill-server` onto the REAL server. This crashed the operator's
  entire fleet on 2026-07-07. Fixed everywhere on this branch
  (`tapes/lib/env.sh`, `tapes/run.sh`, and the same latent bug in
  `e2e/cockpit.sh`, `e2e/github.sh`, `e2e/workspace.sh`); `run.sh` now
  also snapshots the real server's session list before/after and fails if
  any pre-existing session disappeared or a fixture-named session
  (`grove`/`pr-demo`) appeared. (Whole-list equality was the first cut; it
  false-positived within minutes when a sibling agent created a session
  mid-recording — fleet machines are never quiet.) See LEARNINGS.md.
- **vhs's headless Chrome dislikes sandboxes** — under a seatbelt/sandbox
  wrapper vhs fails with `could not open ttyd: net::ERR_CONNECTION_REFUSED`
  (and once left an orphaned `ttyd --once` listener). Run recordings from
  a normal shell. Plain CI runners are unaffected.

Also worth knowing: vhs runs the tape's shell with the caller's
environment intact, so `GROVE_TAPE_SCRATCH` passes straight through to the
recorded shell — that's what lets `run.sh` pre-build `gv` off-camera.

## 2. What was built

- `tapes/lib/env.sh` — sourceable isolation wrapper (scratch
  HOME/`GROVE_STATE_DIR`/`TMUX_TMPDIR`, `unset TMUX`, seeded 3-task fleet
  fixture with a 30s timestamp cushion, `claude: echo` worker stub, canned
  orchestrator-chat stub, hostname/username-free prompt + status bar).
- `tapes/run.sh` — entrypoint: pre-build, `env -u TMUX` everything,
  live-state + real-tmux-session canaries, scratch cleanup.
- `tapes/spike-cockpit.tape` — cockpit boots (the go/no-go evidence).
- `tapes/cockpit-question.tape` — the real scenario: question in
  AGENTS/ACTIVITY → detail view → typed answer → relay to the (stub)
  worker pane → feed shows `answered`.
- `.github/workflows/demo-tapes.yml.draft` — CI proposal, NOT enabled.

## 3. Sizes / timings

| output | size | duration | notes |
|---|---|---|---|
| `cockpit-question.gif` | 244–258 KB | 11 s | 1000×600 px, ~118×33 cells |
| `cockpit-question.mp4` | 160 KB | 11 s | |
| `spike-cockpit.gif` | 157 KB | ~7 s | |
| frame-by-frame `.txt` | 46 KB | 73–75 frames | |

Two orders of magnitude under GitHub's ~10 MB inline ceiling; a dozen
tapes per PR would still be fine. Wall-clock per recording ≈ 30–45 s
including the harness. `Output *.ascii` is **silently ignored** by vhs
0.11.0 (no file, no error); `.txt` works and is a per-frame dump, so the
golden-frame idea uses "last frame of the .txt" instead.

## 4. Determinism (3 consecutive runs)

Final frames were byte-identical across all three runs **except one
line**: the header's live memory gauge (`5.9G/6.0G/5.2G avail`). Sources
of nondeterminism, worst first:

1. **Live system readings in the header** — memory gauge (Darwin sysctl;
   simply absent on Linux CI) and the machine-wide worker count (`5w`
   here, whatever the machine is running). Harmless in a GIF; fatal for
   byte-exact golden frames. Mitigation: mask the header line when
   diffing, or (follow-up, needs a 2-line Go change) a `GV_DEMO_FROZEN`
   env that suppresses live gauges.
2. **Random scratch path on camera briefly** — the orchestrator launch
   command (typed into the pane by `SpawnPane`) shows
   `/tmp/grove-tape.XXXXXX` until the stub clears its pane. Mid-recording
   frames therefore differ per run. Cosmetic in GIFs; golden-frame diffs
   must use the final frame only.
3. **Frame-count jitter** — 73/75/73 frames, GIF size ±4 %. Inherent;
   irrelevant to watchability.
4. **AGE column** — stable across all runs thanks to the fixture's 30s
   cushion; flips if a recording crosses ~60 s. Keep tapes short.
5. **Pane geometry** — fully deterministic (fixed `Set Width/Height`,
   fresh server, no user tmux.conf).

Verdict: GIFs are review-stable as-is; golden-frame regression testing is
feasible but needs the header mask, so leave it out of v1.

## 5. CI draft (proposal — enabling is Dean's call)

`.github/workflows/demo-tapes.yml.draft`: on PRs touching
`internal/tui/**`, `internal/tmux/**`, `cmd/gv/**`, or `tapes/**`,
ubuntu-latest re-records the tapes via `charmbracelet/vhs-action@v2` and
`opengisch/comment-pr-with-images` posts ONE sticky comment embedding the
GIFs, pushing images to a bot-managed branch in this repo (no gists, no
second repo).

Costs: ~3–4 Linux runner-minutes per PR push (private-repo minutes are
billable; ubuntu is the 1× tier). Risks, mitigated in the draft:
`continue-on-error` so a broken recording never blocks a merge; per-tape
steps so failures are attributable; gv pre-built outside the recording so
a cold module cache can't eat the `Wait+Screen` budget. Open risks: font
rendering differs Linux vs macOS (GIF looks slightly different from local
runs — fine for review); vhs-action pins vhs `latest` unless told
otherwise (pin a version when enabling); the images branch grows forever
(the action prunes per-PR, but audit occasionally).

## 6. Recommendation: follow-up build ticket

Worth building. Scope for the ticket:

1. Enable the workflow (rename `.draft`, pin vhs version, verify the
   sticky comment on a scratch PR).
2. One tape per cockpit surface as they change: detail view, `O` new
   chat, done-confirm flow (~30 min each to write against the existing
   fixture).
3. `GV_DEMO_FROZEN` env (tiny Go change) to blank the live gauges —
   unlocks golden-frame `.txt` diffs as a real regression net.
4. Decide GIF-on-branch policy: this spike commits sample GIFs on the PR
   branch for review (flagged for removal before merge); with CI posting
   comments, committed GIFs become unnecessary — drop them entirely.

Not worth building: MP4 uploads (GitHub won't inline them from a branch;
GIF is the review format), asciinema fallback (dead code until vhs breaks),
per-frame txt archives.
