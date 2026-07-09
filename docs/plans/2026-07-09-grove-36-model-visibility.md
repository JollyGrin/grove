# grove-36 finish line: per-pane model/provider visibility (PR #37 follow-through)

**Worker target:** existing worktree `~/git/.worktrees/grove/grove-36-openrouter-model-profiles-run`,
branch `grove-36-openrouter-model-profiles-run`, PR #37 (open, unmerged). All work
lands on that branch; PR #37 merges once, complete.

**Dean's core complaint:** after opening an orchestrator (or worker) he cannot
tell whether the pane is running his Claude sub or an OpenRouter profile — and
if OpenRouter, which model. Claude Code's own UI is useless here (it shows the
model *class* name, "Sonnet 5", even when `ANTHROPIC_MODEL=z-ai/glm-5.2`).
Grove must own the indicator at the tmux + `gv ls` layer.

**Invariant (non-negotiable):** the no-profile path stays byte-identical to
today. Default Claude panes/windows/ls rows show *nothing new*.

## Verified issues (each reproduced against the branch as of 2026-07-09)

### 1. The orchestrator pane tag gets clobbered — visibility is transient
`spawnOrchestratorProfile` tags the pane via `tmux.SetPaneTitle(paneID, name)`
(cmd/gv/main.go) and turns on `pane-border-format "#{pane_index}: #{pane_title}"`
(internal/tmux/grove.go:213). But `pane_title` is writable by the foreground
program via OSC escape — and Claude Code *does* set the terminal title
(grove.go's own `DisableAutoRename` comment documents it setting "2.1.204").
So the profile tag survives only until claude boots, which is exactly why Dean
sees nothing. Fix:

- Store the tag in a **pane user option**, which programs cannot touch:
  `tmux set-option -p -t <paneID> @grove_profile <name>` (new
  `tmux.SetPaneProfile(pane, profile)` replacing `SetPaneTitle` at the call
  site — keep `SetPaneTitle` only if something else needs it, else delete).
- Change the border format to render the option and fall back to title:
  `#{?#{@grove_profile},⚡ #{@grove_profile},#{pane_title}}` prefixed with
  `#{pane_index}: `. Unprofiled panes keep whatever title claude sets; the
  profiled pane shows `⚡ openrouter-glm` permanently.
- Do NOT use `allow-set-title off` — it would kill Claude Code's own useful
  titles on every pane in the window.
- Test in grove_test.go the same way ShowPaneBorders/SetPaneTitle are tested.
- Manual acceptance: spawn a profiled orchestrator, wait for claude to fully
  boot, confirm the border still reads the profile name.

### 2. The profile is not persisted — `gv ls` cannot answer "who's on GLM?"
`state.Task` (internal/state/state.go:56) has no profile field; the only
carrier is the worker window name suffix. Fix:

- Add `ModelProfile string \`json:"model_profile,omitempty"\`` to `state.Task`
  and to the grab-time tracking event payload (find where grab appends the
  tracked event and threads fields into the derived view — follow the pattern
  of `Branch`/`Worktree`).
- `gv ls`: human table gets a profile marker only for rows where it's set
  (suffix the agent/model cell or add a narrow column that collapses when the
  whole fleet is unprofiled); `--json` always emits the field.
- events.jsonl is append-only: new field is additive, old events simply lack
  it — no migration. Verify `state.Load` on a pre-existing events file still
  round-trips (test with an event missing the field).

### 3. `gv adopt` silently drops the profile — worst bug of the set
`cmdAdopt` (cmd/gv/main.go:1853) has `--model` but no `--profile`, and its
relaunch never calls `WrapProfile`. Adopting a disconnected GLM worker
resurrects it on Dean's own Anthropic sub — invisible provider switch,
exactly the failure mode this ticket exists to kill. Fix:

- `--profile` flag on adopt, defaulting to the stored `t.ModelProfile` from
  state (issue 2); `--profile anthropic` explicitly strips it.
- Wrap the relaunch command with `config.WrapProfile(...)` the same way grab
  does (wrap the composed claude+prompt command only — same hooks caveat as
  the grab call site comment).
- Window name via `tmux.WorkerWindowProfile(...)` with the effective profile.
- Persist the effective profile in the adopt event so state stays true.

### 4. Profiled orchestrator resumes a Claude conversation on GLM
`orchestratorCmdProfile` keeps the `--continue` limb, so a fresh GLM
orchestrator resumes the *latest Claude-created* conversation in
`.grove/orchestrator/` at ~100% context. Confirmed in live testing 2026-07-09.
Fix — preferred option first:

- **(a) Per-profile project dir:** run the profiled pane with cwd
  `.grove/orchestrator/<profile>/` (MkdirAll). Claude Code keys `--continue`
  chains by cwd, so each profile gets its own continuity, and CLAUDE.md still
  applies because Claude Code loads CLAUDE.md from ancestor directories.
  **Verify the ancestor-CLAUDE.md load in a throwaway session first**; if it
  doesn't hold, fall back to (b).
- **(b) Fresh-only:** profiled panes drop the `--continue` limb entirely
  (`orchestratorCmdProfile` returns just the wrapped fresh launch). Loses
  continuity but is trivially correct.
- Either way the unprofiled `orchestratorCmd` path stays untouched.

### 5. Cost pricing key mismatch — dated slug vs configured key
Transcripts record `z-ai/glm-5.2-20260616`; pricing is keyed `z-ai/glm-5.2`
→ `CostKnown:false`. Fix in internal/cost: exact-match first, then
longest-prefix match where the boundary character after the prefix is `-`
(so `z-ai/glm-5.2` matches `z-ai/glm-5.2-20260616` but never
`z-ai/glm-5.20`). Table-driven test with both cases.

### 6. Out of scope (documented so nobody chases it)
Claude Code's in-app model display ("Sonnet 5") can't be fixed from grove —
it's the model-class display name. A personal statusline script echoing
`$ANTHROPIC_MODEL` is a Dean-config option, not grove code. The grove
surfaces (window suffix, pane border, `gv ls`) are the answer.

## Task order & gates

T1 (pane user-option tag) → T2 (state/ls persistence) → T3 (adopt) →
T4 (orchestrator continue) → T5 (cost prefix match). T2 before T3 because
adopt's default reads the stored field.

Gates before pushing: `go build ./... && go vet ./... && go test ./...`,
`gofmt -l .` empty, `e2e/dummy.sh` green (grab lifecycle is touched by T2/T3).
Manual smoke on the real cockpit: one profiled orchestrator (border tag
persists post-boot), one profiled worker (window suffix + `gv ls` field),
adopt round-trip of the profiled worker, unprofiled everything unchanged.
