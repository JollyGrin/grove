# Grove — learnings archive · 2026-09

> Rotated out of [LEARNINGS.md](../../LEARNINGS.md) by
> `scripts/log-append.py` (grove-275). Same entry format and sections;
> newest first within each section. The rules these entries taught live
> in `.claude/skills/`; this is the dated record. Grep `LEARNINGS.md
> docs/archive/LEARNINGS-*.md` for the full log.

## Go / CLI

- **2026-09-01 · A workspace brain can be perfectly in sync with the seed
  and still be wrong** (grove-234). The field incident looked like brain
  drift and was not: the workspace's stamp (`6794db4eb15a`) matched the
  embedded seed byte for byte — the doc was current, the SEED was stale.
  `orchestrator/CLAUDE.md` is hand-maintained and nothing ties it to the
  verbs it documents, so `--host` (grove-176, nine verbs by grove-191) and
  `--profile` (grove-36) shipped without ever reaching it; grove-189 DID
  update the seed, which is the proof this is a remembered step, not an
  enforced one. Two failures fell out of one dispatch: the chat concluded
  "`gv grab` has no host flag" (it does — `internal/remote/remote.go`
  intercepts `--host` BEFORE the verb's flagset, so it is absent from
  every `-h` output, and a doc that isn't written is the only place to
  learn it) and dispatched by raw `ssh`; and it grabbed on a per-token
  `openrouter-*` lane while the flat-rate `zai-plan-*` twin was
  configured, because the lane distinction lived only in config comments
  and a repo-tracked skill marked "do NOT load for ordinary dispatch" —
  neither of which exists in another workspace. Rule: a flag that changes
  where work runs or who pays for it is not shipped until the seed says
  so; grove-190's stamp answers "is this brain behind the seed?", never
  "is the seed behind the binary?" (tripwire: #235).
- **2026-09-01 · The release path filter is part of the "shipped"
  definition** (grove-241). `release.yml` triggers on `cmd/**`,
  `internal/**`, `go.mod`, `go.sum` — and not on `orchestrator/**`, even
  though `orchestrator/CLAUDE.md` is embedded in the binary
  (`//go:embed`), so a seed-only merge changes what every `gv init`
  ships and what `gv brains` compares against yet cuts no release: the
  fixed seed exists on main and reaches no machine until an unrelated Go
  change happens to land. Field-proven 2026-09-01 on the #234 train —
  PR #238 (seed content only) merged 12:10:51Z with no Release run, and
  had it merged alone, `gv brains` everywhere would have reported every
  workspace current against a seed the binary never got. An embedded
  asset's directory missing from `on.push.paths` means
  merged-but-never-shipped, invisible until something else releases; the
  guard is a test (`orchestrator/release_test.go`) that reads the
  workflow file and fails without the line.
- **2026-09-01 · A parse-rule split between verbs is indistinguishable
  from breakage until it is taught** (grove-242). `gv nudge grove-236
  --host groveremote "rebase please..."` ran LOCALLY and failed
  `no active task grove-236 — see gv ls`: the flag was never parsed and
  nothing hinted why. The rule is deliberate, not a bug — relay verbs
  parse `--host` prefix-only by design (payload may legitimately contain
  `--host`, so `ExtractHostPrefix` stops at the first non-flag arg, the
  ticket) while `grab` scans the whole argv (`ExtractHost`, any position
  works). But the orchestrator had learned flag-order from the seed's own
  tools block — `gv grab grove-N --host H`, flag-after-ticket — and
  generalized it to a verb where the flag is silently swallowed into
  message text. Fix is teaching, not parsing: the local "no active task"
  error appends the position rule when the payload still carries a
  literal `--host` (pure argv inspection at the call site; the parse is
  untouched), and the seed's `--host` entry now says for answer/nudge the
  flag must come BEFORE the ticket. A verb-surface split like this must
  be taught in the seed or it reads as breakage.

## Claude Code behavior (verified in ovs)

- **2026-09-04 · hook attribution is cwd + session id; a Bash `cd` moves
  the hook cwd** (grove-250; incident on unbrewed 2026-09-02). Hooks live
  in the profile's global `settings.json`, so EVERY session fires them and
  the receiver decides ownership — and it decided by cwd alone. Claude
  Code's hook `cwd` is the Bash tool's persistent shell cwd, not the
  directory the session launched in, so an orchestrator that ran one
  `cd <worktree> && …` had its next Stop attributed to that worker:
  `gv watch` emitted `idle` while the worker was 30 minutes into a busy
  turn, `gv ls` showed the orchestrator's own chat reply as the worker's
  `last_message`, and a stall monitor fired a false idle. The payload
  already carried `session_id` and the task already recorded its worker's
  (`claude_session_id`, folded from `session_started`); nothing compared
  them. Now `stop`/`notification`/`session-end` at a tracked cwd are
  dropped when the ids differ (silent, zero writes); `session-start` is
  exempt so an adopt's fresh pickup session can still register; a task
  with no recorded id keeps cwd-only attribution so an unknown id never
  makes a task unreachable. The brain rule "never `cd` into a tracked
  worktree from the orchestrator" stays as belt; this is the braces.
