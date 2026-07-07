# Grove — learnings

> Working docs: [DESIGN.md](DESIGN.md) is the what/why ·
> [TASKS.md](TASKS.md) is the status board · **LEARNINGS.md** is the
> surprises — anything discovered the hard way or verified against source,
> so we never re-derive (or re-break) it.
>
> Entry format: date · context · the fact · what it changed. Newest first
> within each section. If a learning invalidates a DESIGN.md decision,
> update the doc and note it here.
>
> **Seeded 2026-07-03** from overstory-tui's LEARNINGS.md (@ `8c2f4f0`) —
> the *generic* entries only; each was verified live in ovs. Grid-specific
> entries (Linear pipeline rules, monorepo deploy semantics, worktree:setup
> codegen gap) stay with ovs and will move into the Grid pack's L5 layer.

## Claude Code behavior (verified in ovs)

- **2026-07-02 · resume durability** — `claude --resume <id>` still works
  ≥6 days after the tmux window died, provided the transcript dir
  survives; the resumed session fires **SessionStart with the SAME
  session_id**, so hook re-capture needs no special-casing. Transcripts
  key on the encoded cwd (`<CLAUDE_CONFIG_DIR>/projects/<encoded-path>/`):
  reusing the same worktree path preserves resumability; re-creating the
  worktree at a new path orphans the transcript → pickup-prompt fallback.
  A resumed session opens idle awaiting input — it does not auto-continue.
- **2026-06-10 · hook payloads (verified live)** — Stop carries
  `session_id`, `cwd`, `transcript_path`, `permission_mode`, AND
  **`last_assistant_message`** — sentinel classification needs no
  transcript parsing (transcript stays the fallback for the full-message
  view). SessionStart carries `session_id` + `cwd` + `source`. Gotcha:
  `cwd` arrives realpath'd (`/tmp/x` → `/private/tmp/x`) — task matching
  must compare `filepath.EvalSymlinks` of both sides.
- **2026-06-10 · questions arrive via Stop, not Notification** — a
  plain-text question from an agent **ends the turn**. Notification only
  fires for permission prompts (mostly suppressed under
  `--dangerously-skip-permissions`) and the ~60s idle reminder. Question
  detection rides Stop + the `STATUS:` sentinel; Notification is re-pings.
- **2026-06-10 · profiles are separate worlds** — plugins, marketplaces,
  and MCP auth are **per-profile** (`~/.claude` vs any
  `CLAUDE_CONFIG_DIR`). A fresh worker profile has none of the user's
  plugins — workers run conventionless until the profile is wired. (This
  incident created ovs's doctor; grove's connections manifest exists
  because of it.)
- **2026-06-10 · plugins install at user scope** — `claude plugin install`
  from a directory-source marketplace lands at user scope, so skills load
  from any cwd under that profile; worktree placement doesn't matter.
  Directory-source marketplaces update via `git pull` in the source clone.
- **2026-06-10 · `claude -p` fires SessionEnd at exit** — a headless
  session ends as `dead` *after* Stop's classification (fold order is
  correct; the question survives on the task). Don't be confused by dead
  status in `-p` smoke tests; for real tmux workers, `dead` genuinely
  means crashed/exited.
- **2026-06-10 · sessions-index is unreliable for worktrees** — Claude's
  `sessions-index.json` points at the parent repo and silently misses
  sessions. Always resume by explicit `--resume <id>`, never path-based
  lookup. (Inherited stance from parkranger.)

## tmux / git / detector internals (verified against source)

- **2026-07-07 · `$TMUX` beats `TMUX_TMPDIR`** — tmux resolves its socket
  as `-S`/`-L` > `$TMUX` > `TMUX_TMPDIR`, so every isolated-tmux-server
  script **must `unset TMUX` first** (or `env -u TMUX` each call), or when
  run from inside a worker pane the "isolation" is a silent no-op: sessions
  land on the REAL server, gv's attach takes the inside-tmux
  `switch-client` path and yanks the operator's terminal, and a cleanup
  `tmux kill-server` kills every session and worker on the machine (the
  grove-7 crash, 2026-07-07). `tapes/run.sh` now also snapshots the real
  server's session list before/after as a canary. Never run bare
  `kill-server` in any script; scope it with `env -u TMUX` + the scratch
  `TMUX_TMPDIR`.
- **`tmux.SendKeys` is single-line only** and tmux interprets key-name
  lookalikes in the text. Never use it for prose — relay replies via
  `load-buffer` + `paste-buffer` + a separate `send-keys Enter`. If a
  reply is a single character and the pane tail looks like an option
  picker, pass it through raw without Enter-wrapping.
- **Claude's pane is resolved, never assumed** (2026-07-02) — windows lose
  splits, panes renumber, and claude's process title is its bare version
  string; relay/detector/editor-inject all go through `tmux.ClaudePane`.
- **tmux window-name drift is survivable** (2026-07-02) — live windows
  can show name variants (trailing dash), but `-t session:window` lookups
  still hit because tmux prefix-matches targets. Don't rely on exact
  window-name equality; re-derive + re-store on adopt.
- **`worktree.Add` uses one string for branch and dir** — fine with the
  `<id>-<slug>` no-slash branch convention; a slash would force a two-arg
  fork.
- **Squash-merge defeats `git branch -d`** — a squash-merged branch is
  never ancestry-merged, so `-d` refuses every time. Verify merge via
  `gh pr view --json state,mergedAt`, then `branch -D` + remote delete.
- **Pane detector vs CC chrome (two incidents)** — the spinner glyph
  changed once (✢ → ✽), and current CC's bottom chrome pushes the live
  spinner >15 lines above the pane bottom. Spinner/activity checks must
  scan the full ~30-line capture, not a bottom window; both markers are
  transient so the wide scan is safe. Hooks were right both times — the
  scraper is liveness garnish, hooks are truth.
- **Detector reads `unknown` for a plain shell pane** — LIVE shows
  `unknown` until claude actually boots (e.g. during setup). Expected; the
  task status column carries the truth.
- **`session.EncodePath`** replaces `/` and `.` with `-`; transcripts live
  under `<CLAUDE_CONFIG_DIR>/projects/<encoded-cwd>/`.

## Go / CLI

- **2026-07-04 · never pipe the test gate** — `go test ./... | tail` (or
  `| grep -c FAIL`) reports the PIPE's exit status, not the tests'; two
  red runs merged to main that way in one evening. Gates run bare and
  check $? — filtering happens on a saved log, never inline.
- **2026-07-04 · `/dev/null` IS a character device** — detecting "am I
  interactive?" via `os.Stdin.Stat()` + `ModeCharDevice` passes for
  `< /dev/null`, so the wizard tried to render forms headless and
  "aborted". Use `golang.org/x/term.IsTerminal(fd)`; a non-TTY `gv init`
  must silently become `--yes`, never hang or abort.
- **2026-07-04 · commands typed into tmux panes resolve via PATH, not via
  the invoking binary** — the cockpit's dashboard pane ran whatever `gv`
  was first on PATH (a stale installed build), not the binary that created
  the session. Any pane/hook command must use the absolute
  `os.Executable()` path (`buildCockpit` now does; hooks already did).
- **2026-07-04 · `cmd | grep -q` flakes under `set -o pipefail`** —
  grep -q exits at its first match, the producer SIGPIPEs writing the
  rest of its output, and pipefail reports the pipeline failed even
  though the assertion matched. E2E assertions capture to a file first,
  then grep.
- **2026-07-04 · shared tmux namespaces during ovs coexistence** — worker
  sessions keep ovs's `pr-<repo>` naming (byte-comparable `internal/tmux`),
  so a repo tracked by BOTH ovs and gv lands windows in the SAME tmux
  session. Window names differ per branch and each tool finds its own by
  stored names, so it works — but don't be surprised seeing gv windows
  inside an "ovs" session. Same class of clash made us rename the relay
  buffer (`ovs-relay` → `gv-relay`): tmux buffers are server-global, and
  a shared name would let one tool's relay clobber the other's mid-paste.
- **2026-07-04 · settings.json hook matching must be basename-precise** —
  the ovs-era installer predicate matched `"ovs"` as a substring anywhere
  in the command. The gv equivalent would have CLAIMED (and replaced) ovs
  entries in the shared `~/.cc-work/settings.json` on `gv hooks install`.
  Match on the hook command's binary basename (`gv`, `*grove*`) — never
  substring-across-the-path. Table-tested against a real transition-window
  settings fixture.
- **2026-07-04 · `yaml.Node` keeps flow style when you append** — seeding
  a config with `repos: {}` and appending via node surgery emits the whole
  map single-line (`repos: {r: {path: …}}`) because the `{}` scalar's
  flow style sticks. Set `node.Style = 0` after lookup/creation to force
  block style. (yaml.v3 round-trip via Node DOES preserve comments —
  that part worked as hoped in `gv init`.)
- **stdlib `flag` stops at the first positional** — `gv grab <url> --repo
  x` silently ignores `--repo` without a re-parse loop (`parseAnywhere`).
  Flags-after-positionals is table stakes; stdlib doesn't give it to you.
- **Hooks reference the absolute binary path** (`~/go/bin/<bin>`);
  `go install` refreshes that binary in place, so rebuilds don't require
  re-installing hooks (path unchanged).
- **Cost estimates: dedup + cache asymmetry** — transcript pricing follows
  ccusage's rules: dedup entries by `message.id`+`requestId`, price cache
  reads at 0.1×, 5-minute cache writes at 1.25×, 1-hour at 2×. Costs are
  ESTIMATES of relative effort, never billing.

## Field notes (ovs, kept for judgment)

- **The full loop is proven** (2026-06-10, DEV-4556): grab → worktree +
  setup → autonomous work → PR + CI green + previews + self-review →
  ticket transitioned by the agent via MCP → clean `STATUS: DONE`
  sentinel. Sentinel compliance was 1/1 on day one; when an agent drops
  the sentinel, classification degrades to `stalled` — correct behavior.
- **Label→repo inference misses in practice** — real tickets carry
  type-labels (`[Feature]`), not surface-labels; treat `--repo` as the
  common path and inference as a bonus.
- **The `api_key_env` indirection earned its keep on day one** — users'
  keys live under personalized env names; never hardcode the env var.
- **When demoting a data source, wire the replacement into the same code
  path** — ovs's preview column went blind because the cheap poll dropped
  comments while only the on-demand path kept them.
- **2026-07-05 · a git-inited $HOME shadows parent-folder detection** —
  `git rev-parse --show-toplevel` from ~/git/unbrewed returned /Users/dev
  (dotfiles repo), so `gv init` made HOME the workspace. Parent-of-repos
  detection must test the cwd ITSELF (≥2 direct child repos AND cwd is not
  the git root) before trusting any enclosing repo root. Field-hit on
  the operator's machine within minutes of shipping.
