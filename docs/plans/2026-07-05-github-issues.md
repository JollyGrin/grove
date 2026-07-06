# github-issues provider — Phase 3 pulled forward (unbrewed runs on it)

> Status: revised per plan-reviewer round 1 (REVISE — all findings
> applied) → confirm → execute.
> Driver (the operator): unbrewed's real flow is GitHub-native — public tickets +
> requests on `JollyGrin/unbrewed-p2p` issues, detailed private tickets on
> `JollyGrin/unbrewed-engine` (pro-server) issues; markdown tasks retire.
> Design basis: DESIGN.md §5.3 (native `gh` adapter, the seam stress
> test), §5.1 interface, §5.4 agent-owned transitions; resolves DESIGN
> OQ3 (labels vs Projects v2) → **labels** (simpler, `gh`-native,
> per-repo; Projects v2 stays a later option).

## Scope decisions

- **Native `gh` CLI adapter** (no token plumbing — `gh auth` owns it;
  doctor already checks it). All calls run with `cmd.Dir = repoPath` so
  `gh` infers owner/repo from the git remote — the provider is
  repo-rooted exactly like markdown. **cmdGrab's non-linear branch is
  currently hardcoded to the markdown kind (review C-1) — Task 1
  generalizes it to `FromConfigKind(cfg, kind, repo.Path)` so the
  resolve-repo-first path serves ANY repo-rooted kind.**
- **Task IDs are fleet-unique:** `<repoName>-<n>` (e.g.
  `unbrewed-p2p-7`) — two repos in one workspace share one tasks.json,
  so bare issue numbers would collide. `ParseID` accepts `7`, `#7`, a
  full issue URL, or the canonical form, and canonicalizes. The id is
  slug-safe (branch `unbrewed-p2p-7-<slug>`, valid markdown-shape id, so
  `provider.IDCandidates`/findTask match the canonical form as-is). No
  `#` anywhere near branch/tmux names (tmux formats treat `#` specially).
  **Short-ref resolution (review I-2):** `gv done 7`/`attach 7` resolve
  via a numeric-suffix scan of tracked ids — exactly one tracked id
  ending `-7` → use it; several → error listing them; none → the normal
  miss. Cold `gv adopt` canonicalizes through the resolved repo's
  provider (`prov.ParseID`), fixing branch inference
  (`origin/<repoName>-<n>-*`).
- **Hard rule intact:** the binary never transitions or closes issues.
  Verbs (rendered into the generic md_* kickoff set, which every
  non-linear kind already uses): Start = agent runs
  `gh issue edit <n> --add-label in-progress` (best-effort — create the
  label once per repo if missing) and comments a one-line start note;
  Review = open the PR with `Closes #<n>` (auto-links + auto-closes on
  merge — the human's merge IS the terminal transition) and swap the
  label to `in-review`. Never close the issue directly.
- **Capabilities{CanList: true}:** `gv grab` with no args lists open
  issues (canonical id · labels · title), excluding in-flight per the
  existing event-state-authoritative filter. **No silent caps (review
  I-3):** the gh fetch limit is 200 and printBacklog prints an explicit
  "capped at 200" line when the fetch fills it.
- **Surface integration:** `provider.kind: github` valid globally and
  per-repo; wizard's provider step offers `github` when the probe sees a
  GitHub remote; connections/doctor provider-readiness row for github =
  gh binary + gh auth (both rows exist — the github row just points at
  them; no new checks).
- **Out:** Projects v2 transitions, issue creation from gv, cross-repo
  issue search, webhooks. The Grid/linear and markdown paths are
  untouched (golden/kickoff tests must stay green).

## Task 1 — `internal/provider/github.go` + grab/resolution wiring (TDD)

**Files:** `internal/provider/{github.go,github_test.go}`,
`internal/provider/provider.go` (FromConfigKind case),
`internal/config/config.go` (accept "github" in validation),
`cmd/gv/main.go` (cmdGrab non-linear branch → `FromConfigKind(cfg, kind,
repo.Path)`; printBacklog cap note; findTask numeric-suffix fallback;
adopt cold-path canonicalization via the resolved provider).

- `NewGitHub(repoPath, repoName string)`; `Kind()="github"`.
- `ParseID`: `7` / `#7` / `https://github.com/<o>/<r>/issues/7` /
  `<repoName>-7` → `<repoName>-7`; anything else errors.
- `Get(id)`: `gh issue view <n> --json number,title,body,url,labels,
  comments,state` → provider.Task (comments mapped author/body; labels
  by name; closed issues still fetch — adopt needs them).
- `List()`: `gh issue list --state open --json number,title,labels
  --limit 200` → Tasks sorted by number; the provider reports when the
  fetch filled the cap (returned alongside the tasks) and printBacklog
  surfaces it.
- `gh` runner injectable (func field) for table tests — no network, no
  real gh in unit tests. Timeout 15s.

**Verify:** table tests: ParseID matrix; Get/List JSON fixtures incl.
comments + labels + the 200-cap flag; error surfaces (gh exit 1 → its
stderr in the error); FromConfigKind("github") wiring; config validation
accepts github per-repo + global; IDCandidates("unbrewed-p2p-7")
resolves to itself (review S-1 — the unanchored linear regex also emits
a spurious P2P-7 candidate; pin that membership arbitration handles it);
numeric-suffix fallback unit-tested (unique/ambiguous/none).

## Task 2 — wizard/doctor/docs surface

**Files:** `internal/wizard/wizard.go` (+test), `internal/connections/
core.go` (+test), `config.example.yaml`, `cmd/gv/main.go` (usage note
only if needed).

- providerOptions: add `github` when `Probe.RemoteHost == "github"`;
  provider step title loses the "Phase 3 roadmap" note.
- connections: **refactor `providerConnections` to iterate repos by
  `ProviderKindFor`** (review I-4 — it currently branches once on the
  global kind, so github repos would get bogus markdown task-dir rows):
  markdown repos keep the task-dir row, linear adds the key row once,
  github repos get a static pointer row (`provider:github:<repo>`,
  gates = the existing gh binary+auth rows). Mixed-fleet test.
- config.example.yaml documents `provider: github` per-repo.

**Verify:** wizard test (github offered iff remote), connections row
test, existing golden/kickoff/doctor tests green.

## Task 3 — e2e + gate [no-TDD: e2e harness]

**Files:** `e2e/github.sh` (new).

Dummy-data pattern with a **stub `gh`** first on PATH (records argv,
serves canned JSON for `issue list`/`issue view`): scratch workspace
with two child repos both `provider: github`, worker `echo`. Asserts:
no-arg grab lists issues with canonical ids; `gv grab 7 --repo <r>` →
branch/worktree `<r>-7-<slug>`, kickoff prompt contains the label verbs
+ `Closes #7` + STATUS sentinel and no Linear/markdown-isms; dedup on
re-grab; `gv done 7` resolves via the numeric-suffix fallback when unique and
errors when #7 is tracked in both repos; `gv done` refuses unmerged
(stub `gh pr view` says OPEN) and `--force` cleans; backlog cap note
appears when the stub serves a full page. **Gate:** bare
`go build/vet/test`, gofmt clean, all five e2e suites exit 0.

## Task 4 — unbrewed cutover (the operator's machine, after merge) [no-TDD: runbook]

`gv init` at `~/git/unbrewed` (parent, label `unbrewed`, provider
github); set both children `claude: claude --dangerously-skip-
permissions`; **delete the children's `.grove/` dirs** (sample tasks
only — the operator sanctioned; also removes their implicit-workspace markers so
the parent is the unambiguous nearest root); remove the two unbrewed
entries from the global config (backup first — grid entries stay);
verify: `gv` inside either repo shows `GROVE · unbrewed`; `gv grab
--repo unbrewed-p2p` / `--repo unbrewed-pro-server` list the right
repo's real issues (review I-5: no-arg grab in a two-repo workspace is
deliberately ambiguous — cwd→repo narrowing is a noted future nicety);
doctor green.

## Risks / FMA

| Risk | Mitigation |
|---|---|
| grab never reaches the provider (the C-1 class) | cmdGrab generalization is Task 1 scope with e2e gating on real github-kind grabs |
| Short refs unusable after grab (done/adopt asymmetry) | Numeric-suffix fallback + provider-canonicalized cold adopt, unit + e2e tested |
| >200 open issues silently truncated | Cap surfaced by provider + printBacklog note |
| Issue-number id collisions across repos in one fleet | `<repoName>-<n>` canonical ids; e2e pins #7-in-both-repos |
| Agent closes/mutates issues (terminal-state rule) | Verbs never close; PR `Closes #N` + human merge is the transition; kickoff text explicit |
| `gh` missing labels breaks transitions | Verbs are best-effort prose with a create-once hint; label failure never blocks the work |
| Kickoff regressions on linear/markdown | Byte-golden + template tests stay in the gate |
| Unit tests hitting the network | Injectable gh runner; e2e uses a stub binary |
| Deleting children's `.grove/` loses data | Pre-checked: only sample task-001.md files; explicit user sanction; backup listing printed before rm |
