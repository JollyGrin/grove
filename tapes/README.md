# tapes/ — vhs-recorded cockpit demos for PR review

Declarative [vhs](https://github.com/charmbracelet/vhs) tapes that drive
`gv` headlessly against dummy data and record a GIF/MP4 — so a cockpit UX
change can be *watched* from the PR instead of checked out and clicked
through. Spike findings + CI proposal:
[docs/plans/2026-07-07-vhs-pr-demos-spike.md](../docs/plans/2026-07-07-vhs-pr-demos-spike.md).

Not to be confused with `demos/` (asciinema+Remotion **tutorials**, issue
#2) — `tapes/` is per-PR review evidence, regenerated and disposable.

## Running

```sh
brew install vhs        # pulls ttyd; ffmpeg assumed present
tapes/run.sh tapes/cockpit-question.tape
```

Outputs land in `tapes/out/` (GIF + MP4 + frame-by-frame `.txt`).
Everything runs against a scratch world — a recording can never touch
`~/.config/grove`, `~/.local/state/grove`, overstory state, or your real
tmux server, and `run.sh` fails loudly if that promise breaks (file
canaries + a before/after snapshot of the real server's session list).

## Tapes

| tape | shows | one-liner |
|---|---|---|
| `spike-cockpit.tape` | cockpit boots: AGENTS + ACTIVITY + orchestrator pane | `tapes/run.sh tapes/spike-cockpit.tape` |
| `cockpit-question.tape` | worker question → open detail → answer → feed shows `answered` | `tapes/run.sh tapes/cockpit-question.tape` |

## How it works

- `lib/env.sh` — sourced (off-camera, under vhs `Hide`) by the recorded
  shell: scratch `HOME`/`GROVE_STATE_DIR`/`TMUX_TMPDIR`, seeded
  `events.jsonl` fleet fixture, worker command stubbed to `echo`, a canned
  orchestrator "chat" stub (zero API credits), sanitized prompt/status bar
  (no hostname/username in public GIFs).
- `run.sh` — the entrypoint: pre-builds `gv` into the scratch, strips
  `$TMUX` (see below), runs vhs, asserts live state unchanged, cleans up.

## The $TMUX rule (do not skip)

tmux resolves its socket as `-S`/`-L` > `$TMUX` > `TMUX_TMPDIR`. Run from
inside any tmux pane (i.e. by every grove worker), `TMUX_TMPDIR` alone is
a silent no-op and "isolated" tmux commands — including a cleanup
`kill-server` — hit the **real** server. `lib/env.sh` unsets `TMUX` first
and `run.sh` uses `env -u TMUX` for every tmux/vhs call. Keep it that way.
(LEARNINGS.md, 2026-07-07.)

## Writing a new tape

Copy `cockpit-question.tape`. Rules of thumb:

- Do setup under `Hide` … `Show`; sync with `Wait+Screen /regex/`, never
  raw `Sleep`s, so timing jitter can't skip a beat.
- The cockpit lands focused on the orchestrator pane — `Ctrl+B` `o` hops
  to the dashboard.
- Keep total runtime under ~60s: the fixture's timestamps carry a 30s
  cushion so the AGE column can't flip mid-recording.
- Fixture events live in `lib/env.sh`; extend the seed there if your
  scenario needs more fleet.
