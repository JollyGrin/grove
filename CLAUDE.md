# grove (`gv`)

Repo-agnostic orchestrator for autonomous Claude Code sessions: one task →
git worktree + tmux window + kickoff prompt → PR, with pluggable task
backends (local markdown default; Linear/GitHub adapters), model routing,
a first-run wizard, and a layered learnings system. OSS-ready successor to
`overstory-tui` (`ovs`, `~/git/thegrid/overstory-tui`, frozen).

## Docs — what to read, what to append

- Fresh pickup: [HANDOFF.md](HANDOFF.md). Rules that were learned from
  incidents live in `.claude/skills/` — load the matching skill before
  touching tmux (tmux-discipline), the test gate/e2e/merge (shipping-
  gates), or hook/session/transcript code (claude-code-facts). When a new
  learning generalizes into a rule, update the skill too.
- **When you ship / when surprised — append, never read:**
  `scripts/log-append.py tasks <<'EOF' … EOF` adds your `- [x]` row to
  the top of [TASKS.md](TASKS.md) §Now; `scripts/log-append.py learnings
  --section "<name>" <<'EOF' … EOF` adds a dated entry to
  [LEARNINGS.md](LEARNINGS.md). Both keep the heads under their cap by
  moving the oldest rows into `docs/archive/` (never deleting; the
  `internal/guidance` test enforces the cap). Looking for a past row or
  entry? `grep -r <term> TASKS.md LEARNINGS.md docs/archive/` — pull a
  file in only for a specific lookup, never as a default read.
- **Three memory surfaces, one line between them.** Auto memory
  (`~/.claude/projects/<repo>/memory/`, machine-local, shared by every
  worktree of this repo) holds Claude-written working notes: corrections
  and confirmed approaches, one lesson per file. [LEARNINGS.md](LEARNINGS.md)
  (git, every machine) holds dated, verified surprises about the harness
  and tooling. `.claude/skills/` holds the rules that generalized. A note
  moves up when it proves out; nothing lives in two places. A train's
  memory file is deleted when its PR merges — TASKS.md and git hold the
  rest, and only the index line costs every chat.
- Read only as the task needs: [DESIGN.md](DESIGN.md) (founding what/why),
  `docs/*-design.md` (deep designs), [docs/roadmap.md](docs/roadmap.md)
  (open phases), [docs/seed-manifest.md](docs/seed-manifest.md) (when
  touching a package copied from ovs). Plans go in `docs/plans/`.
- External surfaces (gv-<surface> repos) build against the plugin
  contract, [docs/plugins.md](docs/plugins.md) + the plugin-authoring
  skill. Any `--json` field or events.jsonl record is a contract:
  additive-only; `e2e/plugin.sh` is the tripwire.

## Layout

Config `~/.config/grove/`, state `~/.local/state/grove/` (env override
`GROVE_STATE_DIR`) are the global/defaults layer. A repo or parent dir
with a `.grove/` marker is a WORKSPACE with its own
`.grove/{config.yaml,state,orchestrator}`, found by ambient walk-up. The
orchestrator's brain, `.grove/orchestrator/CLAUDE.md`, is grove's seed and
is replaced on every update; the operator's rules for that workspace live
beside it in `CLAUDE.local.md`, the overlay, which wins where the two
contradict and is never touched by grove (`gv brains`,
docs/orchestrator-overlay.md). A
workspace's cockpit and its workers share one tmux session
`grove-<label>` (window 0 = cockpit, 1+ = workers). The cockpit has two
layouts, `horizontal` (every pane side by side) and `focus`; `L` toggles
them (`cockpit.layout`; the old `vertical`/`tiled` values read as
horizontal). In `focus` one chat is shown beside the dash (the dash's
width is `cockpit.focus_dash_width`, a fraction, default 0.3; unset, 0,
non-numeric, a percent like `30`, or outside 0.15–0.85 read as 0.3;
takes effect when the cockpit session is next built) and the rest are
minimized (hidden, as in §Hidden chats) behind a 4-row rail pane under it
— one chip per chat, the shown one bold, titled by Claude's pane title; a
chat's name (`cockpit·1`, `chat-3`) is stamped on its pane
(`@grove_chat_name`) before any move, so it never flips with focus —
click a chip or `gv chat focus <session>` swaps the shown chat; `L` back
restores exactly the chats focus minimized. In focus every hidden chat's
pane is pinned to the slot's size, so a swap is one same-size `swap-pane`
(no Claude repaint) and a chat keeps its `grove-chat-<label>-<n>` home for
life; a client attached straight to a hidden chat un-pins it to follow
that client, and its detach re-pins it. `gv hooks install`
writes the **shared** `~/.cc-work/settings.json` — it preserves other
tools' entries, but treat it with respect.
Subscription accounts live in `<state>/accounts/` (`<name>.token` 0600 +
`active`); they are injected as `CLAUDE_CODE_OAUTH_TOKEN` at launch and
never apply under a non-default `claude_config_dir` (that profile keeps
its own login). A token-pinned session has no claude.ai connectors and no
Remote Control — keep connector-needing chats on `login` — and `/login`
inside one switches that session, so never run it there.

## Feature trains

A long-lived feature branch is a first-class thing: `gv feature new
<slug> --repo R [--base main]` opens `feature/<slug>` (or `--adopt`
registers one already pushed by convention); `gv grab DEV-X --feature
<slug>` forks the worktree from it and PRs back into it instead of the
repo base — inferred automatically for a ticket carrying the feature's
label, no flag needed. `gv feature ls [--json]` shows every open train's
cars, how far behind base it is, and its own PR into base. `gv feature
land <slug> [--yes]` runs `gv done` for every car whose PR merged into
the train's branch; it never closes an issue itself — that needs an
explicit operator order, taught to the orchestrator seed as the
exception to "never close any issue" (`orchestrator/CLAUDE.md`). `gv
serve <slug>` runs the train's tip from `.grove/run.sh`, committed with
the workspace — trust is a recorded sha256 (`run_script_trusted`); a
changed script always re-prompts, never runs silently.

## Hidden chats

Opt-in, default unchanged: a chat opens as a side-by-side cockpit pane
(`O`, `)`, digits, `gv orchestrator new`) and stays one until the
operator hides it — nothing auto-hides, and hide/show touch no spawn
path. Hiding a LOCAL chat moves its pane (`break-pane`) into an ordinary
detached `grove-chat-<label>-<n>` session: the process keeps running,
the `%pane` id and the pane's stamps are unchanged, and `gv chat
ls|send|close`, the phone and `gv park --chats` see it as any other
detached chat. Showing joins it back into the workspace's cockpit window
(`join-pane`). Three routes: `!gv chat hide` inside the chat (no
argument = `$TMUX_PANE`); the cockpit's CHATS box — between AGENTS and
ACTIVITY, absent with zero chats, reached with `tab` — where `h`
hides/shows (the keyboard stays on the dashboard), `enter` focuses
(showing first), `a` replies inline to a hidden local chat and `x`
closes after y/N; and `gv chat hide [<session-id>|<%pane>]` / `gv chat
show <session>` from anywhere. A REMOTE chat's pane is only an ssh
attachment, so hide closes that LOCAL pane and show (`gv chat show
@<host>/<session>`) re-attaches — nothing is sent to the host; the
hidden chat is remembered in the cockpit session's
`@grove_hidden_remote` option, which dies with the session on park.
Both log `chat_hidden` / `chat_shown` (docs/plugins.md, where the known
limits are written down too).

## Build / test

- `go build ./... && go vet ./... && go test ./...` green; `gofmt -l .`
  empty. Never pipe the gate (a pipe reports the pipe's exit status).
- **Never `go install ./cmd/gv`.** The operator's `~/go/bin/gv` is
  refreshed only by `gv update --yes` (a push to main auto-cuts a release
  within about a minute); `go install` stamps the binary `dev`, which
  `gv update` then refuses. To test an UNMERGED branch, hand over a
  throwaway build: `go build -o /tmp/gv-<ticket> ./cmd/gv`.
- `e2e/dummy.sh` runs the grab/ls/hook/untrack/done loop against scratch
  everything; run it before merging anything that touches the task
  lifecycle. `e2e/all.sh` runs every suite (no CI covers them) — run it
  before merging anything that touches the TUI, tmux, or the lifecycle.
- When compacting, always preserve the list of modified files, the
  task's acceptance criteria, the gate command above, the PR URL, and
  the `STATUS:` line contract.

## Hard rules (provider-neutral)

- **The binary never mutates a task backend's terminal state.** Grove
  reads; agents transition; humans finish — for every provider.
- **The binary never deletes worktrees/branches it didn't create** (audit
  reports orphans; removal is the human's call).
- **`events.jsonl` is append-only** (O_APPEND + flock); `tasks.json` is a
  derived view — never writable state.
- Merge checks go through `gh` (`pr view --json state,mergedAt`), never
  git ancestry — squash-merges break ancestry.
- **Propose, then dispose** — orchestrator/autonomy never takes an
  irreversible or outward-facing action without human confirmation.
- **`ovs` is frozen.** Never edit `~/git/thegrid/overstory-tui`; note
  backports instead of applying them.

## Conventions

- Decision logic lives in tested internal packages; `cmd/gv/main.go` is
  thin glue. TDD for anything with branching logic.
- Packages copied from ovs stay byte-comparable with upstream (only the
  import path differs) until a plan task deliberately generalizes them;
  record every divergence in docs/seed-manifest.md.
- Non-trivial work: brainstorm → `docs/plans/YYYY-MM-DD-<slug>-design.md`
  (design-reviewer) → `docs/plans/YYYY-MM-DD-<slug>.md` (plan-reviewer)
  → execute on a short-lived branch in a worktree
  (`~/git/.worktrees/grove/<slug>`), commit per plan task, then a PR
  (or `git merge --ff-only`) to main. Docs go direct to main; code goes
  through a branch.
- E2E without touching anything live: the dummy-data pattern — scratch
  `HOME` + `GROVE_STATE_DIR` + repo `claude:` set to `echo`
  (docs/seed-manifest.md §Dummy-data E2E).
