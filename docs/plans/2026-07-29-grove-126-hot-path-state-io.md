# grove-126: cockpit hot path — state I/O (design + plan)

**Ticket:** #126 (farewell audit 2026-07-18). The tmux half was fixed by
grove-149 (#152): one `SessionSnapshot` per session per tick. This plan
covers the remaining state-I/O half.

## Problem

The cockpit's 1s beat (`refreshCmd`) currently does, every tick:

1. `state.Load` — full JSON decode of events.jsonl **and** an
   unconditional rewrite of tasks.json to disk.
2. `state.ReadEvents(…, 200)` — a **second** full scan of the same log
   for the feed tail.

events.jsonl is append-only with no compaction, so both costs grow
forever. Two side problems ride along: every stop hook stores the
**untruncated** `LastAssistantMessage` (multiple KB per turn, compounding
the parse cost), and `gv ls` builds a cold `cost.Cache` per invocation
(the cost.go comment defers an on-disk cache to IDEAS.md; the ticket asks
for the decision to be folded in here).

## Design

### 1+2. Incremental folder (`internal/state/folder.go`, new file)

`state.go` stays byte-comparable with ovs (seed-manifest rule); new code
goes in a new file in the same package so it can reuse the unexported
`fold`, `tasksSlice`, `eventsPath`.

```go
type Folder struct {
    mu       sync.Mutex
    stateDir string
    tailCap  int              // feed tail bound (cockpit passes 200)
    offset   int64            // bytes of events.jsonl already consumed
    tasks    map[string]*Task // running fold state
    tail     []Event          // last tailCap events, oldest-first
    viewHash uint64           // fnv-64a of the last tasks.json written
    wrote    bool             // view written at least once this process
}

func NewFolder(stateDir string, tailCap int) *Folder
func (f *Folder) Refresh() (map[string]*Task, []Event, error)
```

`Refresh` semantics — one pass, O(appended bytes):

- **Stat first.** Missing file → forget everything, return empty (same
  shape as `Load`). `size < offset` → the append-only log was replaced
  (scratch e2e reuse, manual surgery): reset and refold from 0.
- **Consume only complete lines.** Read from `offset` with
  `bufio.Reader.ReadBytes('\n')` — no `bufio.Scanner` line-length cap,
  so this path never inherits #123's truncation bug. A trailing chunk
  without `\n` is a torn append still in flight: leave it unconsumed
  (offset does not advance); it folds on a later tick once completed.
- **Complete-but-malformed lines are skipped** (offset advances — they
  will never become valid), matching `ReadEvents`. Note: `Load` instead
  *stops* folding at the first malformed line, which after a crash-torn
  append followed by more appends silently drops everything after the
  scar; the folder's skip-and-continue is strictly more resilient. The
  divergence is visible only on a corrupt log.
- **Snapshot copies out.** Fold mutates `*Task` in place across ticks,
  and the bubbletea render loop holds the previous result — so `Refresh`
  returns per-call shallow copies of the task structs and the tail
  slice. A mutex serializes concurrent refreshes (the 1s beat plus
  ad-hoc refreshes run as separate cmd goroutines).
- **View write is dirty-flagged.** Marshal + write tasks.json only when
  at least one event folded this call (or on the first call of the
  process); skip the write when the marshaled bytes hash equal to the
  last written view (e.g. ticket-less feed events like
  `orchestrator_closed` fold to a no-op). This also shrinks the #121
  non-atomic-write race window; the atomic-rename fix itself stays #121.

RAM stays bounded (cockpit rule): the task map is the same map `Load`
already returned each tick, the tail is capped, the view is remembered as
a hash, not bytes. Net allocation per tick *drops* from
O(whole log twice) to O(new events + fleet-size copies).

### Wiring (`internal/tui/tui.go`)

- `Model` gains `folder *state.Folder` (pointer — Model is copied by
  value); `New` creates it with tail cap 200.
- `refreshCmd(folder, stateDir, session)` calls `folder.Refresh()` once;
  the separate `state.ReadEvents` call is deleted. `stateDir` remains a
  parameter for the resource-gauge log.
- `prsCmd`'s nil-tasks fallback switches from `state.Load` (full parse +
  rewrite, every 30s beat) to `state.Active(state.ReadTasks(stateDir))`
  — read-only, no fold, no rewrite. tasks.json is at most one changed
  tick stale, which is fine for a 30s PR poll; hooks already rely on it
  the same way.

CLI paths (`cmd/gv` × 7, almanac mode entry, audit) keep cold
`state.Load`/`ReadEvents` — one-shot invocations, not the hot path.

### 3. Cap what stop hooks store (`internal/hooks/hooks.go`)

`Receive("stop")` caps `data["message"]` at 2000 runes (head-truncated,
`…` marker, rune-safe — grove-131 class). Head, not tail: the parsed
`status`/`sentinel`/`question` fields already carry the actionable end of
the message, and `firstLine` (done-notification body) wants the head.
Classification still runs on the full text. This bounds per-turn log
growth; the *reader*-side truncation bugs stay #123 (this cap guarantees
new records can never outgrow reader buffers).

### 4. `gv ls` cold cost cache — decision: defer (stays IDEAS.md)

Keep the in-process `cost.Cache` only. An on-disk cache adds an
invalidation protocol (transcript mtime/size per file), a new state file
in every workspace, and a staleness surface — for a cold path that runs
at human frequency and whose latency has not measurably hurt (`gv ls`
with cost on is interactive-fast today; the audit already watches
`events_size_bytes`). Revisit only if `gv ls` latency is actually felt;
that would be a new ticket with a measurement attached, not part of this
hot-path fix.

## Tasks

1. `internal/state/folder.go` + `folder_test.go` (TDD): parity with
   `Load`+`ReadEvents` on a seeded log; incremental append pickup; tail
   cap; torn-write hold-back; malformed-line skip; shrink→refold;
   dirty-flagged view write (unchanged fold must not rewrite an
   externally-touched tasks.json); returned-copies isolation.
2. Wire `Folder` into `Model`/`refreshCmd`; switch `prsCmd` fallback to
   `ReadTasks`; update `refresh_test.go` producer test.
3. Hooks message cap + test.
4. Gate: `go build ./... && go vet ./... && go test ./...`, `gofmt -l .`
   empty, `e2e/all.sh` green.

## Acceptance mapping

- per-tick work O(new events), one list-windows per session per tick —
  (1)+(2); tmux half already grove-149.
- no visible behavior change in `e2e/cockpit.sh` / `e2e/all.sh` — gate.
- cockpit RAM constraint — bounded tail, hash-not-bytes view memory, no
  new goroutines/polls.
