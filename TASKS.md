# Grove — status board

> Working docs: [DESIGN.md](DESIGN.md) is the what/why · **TASKS.md** is
> the status board · [LEARNINGS.md](LEARNINGS.md) is the surprises.
> Fresh pickup? Read [HANDOFF.md](HANDOFF.md) first.
>
> Phases mirror DESIGN.md §13 (redrawn 2026-07-03 per design review).
> Each phase gets a `docs/plans/` plan (plan-reviewer gated) before code.

## Now (2026-07-12)

- [x] Seed brain learned `--host` and billing lanes (grove-234,
      2026-09-01): the embedded orchestrator seed had **zero** occurrences
      of `--host` and said nothing about model-profile lanes, though
      grove-176 shipped `gv grab --host`, grove-191 extended it to nine
      verbs (+ `orchestrator new`), and grove-36 shipped `--profile`. A
      freshly-`gv init`'d workspace whose brain stamp matched the seed
      EXACTLY still dispatched by raw `ssh` (§9 presented `gv handoff` as
      the only route to another host, and handoff correctly refuses a task
      with no PR body) and grabbed on the pay-per-token
      `openrouter-glm-flash` lane while the flat-rate `zai-plan-glm-flash`
      lane was configured with its key present. Content-only fix to
      `orchestrator/CLAUDE.md`: the tools block gains `gv grab --host`
      (with the note that `--host` is intercepted before each verb's
      flagset, so it never appears in `-h` output) and `gv grab
      --profile`; duty 3 gains a **Remote dispatch** paragraph (grab
      `--host` starts fresh work; handoff MOVES running work; the remote
      resolves `--repo` against its OWN config) and a **Lanes cost
      different money** paragraph (`zai-plan-*` flat-rate vs
      `openrouter-*` per-token, same model under two prefixes, name the
      lane when proposing a grab); duty 9 is rescoped to moving running
      work. The stamp moves by design. Tripwire is #235; propagation to
      already-seeded workspaces is #236.
- [x] `gv update`'s dev-build refusal stops telling the operator to
      `go install ./cmd/gv` (grove-233, 2026-09-01): `ErrDevBuild` now
      names the escape hatch that actually works — `gv update --yes
      --force` — instead of the one thing (`go install`) that re-stamps
      the binary `dev` and guarantees the next refusal too. Doc comment
      rewritten to state the rule rather than the old inverted premise.
- [x] model-lanes skill: executable snippets now actually execute
      (grove-202, 2026-08-29): the skill tells the orchestrator to run its
      snippets verbatim, and four defects made that produce wrong routing
      *silently*. (1) The Step 2 calibration read `total_tokens / turns`;
      `gv cost --analyze --json` emits five separate counters
      (`input_tokens`, `output_tokens`, `cache_create_5m_tokens`,
      `cache_create_1h_tokens`, `cache_read_tokens`) and no total at all,
      so the expression yielded `null` and every credit/ceiling number
      downstream was null or skewed — replaced with a jq that sums the five
      and prints tickets/turns/resident_k/cache-share (live: 16 tickets,
      1,066 turns, 108k resident, 95% cache read). (2) Gate 0 built the
      transcript dir with `sed 's#/#-#g'`, missing `EncodePath`'s second
      `.` → `-` rule — `…/grove/.grove/orchestrator` encodes to
      `-home-dean-git-grove--grove-orchestrator`, so the lane-killer gate
      read a nonexistent directory and any lane would look dead. (3) It
      picked the transcript with `sorted(glob(...))[-1]` over UUID
      filenames; now `max(..., key=os.path.getmtime)` (on a live 7-file
      project dir the two disagree). (4) The z.ai windowed ceiling
      double-applied the peak multiplier — 2,000-credit bucket ÷ 18.6
      credits/turn is ~107 turns, ~80 under the ≤75% in-flight rule (~160
      off-peak), not 40/80; off-peak fleet share is ~37%, not ~19%, so
      width is 2, not 3. Also: `repos:`/`provider:` documented as
      replaced wholesale (not deep-merged) with `orchestrator:` dropped
      from the global layer; `ZAI_API_KEY`'s origin documented (profile
      `auth_token_env` + `~/.config/grove/.env`, per-machine — a 401 means
      no key on this box, not no credits); the
      `CLAUDE_CODE_AUTO_COMPACT_WINDOW` ban scoped to the credit meter with
      the shipped `kimi` profile named as the required exception; the skill
      relocated in its own text to repo-tracked `.claude/skills/`; the
      60–90 routing band reconciled against `ticket-writing`'s split-at-60
      (split wins whenever splitting is still available). The path-encoding
      and mtime rules were promoted into `claude-code-facts`, and the
      never-logged 2026-08-27 probe results into LEARNINGS.md.

- [x] `null` / bare `---` config.yaml no longer slips the empty-doc guard
      (grove-201, 2026-08-29): grove-129 fixed the *empty* config panic by
      testing `len(doc.Content) == 0`, but yaml.v3 parses a file holding
      `null`, `~` or a bare `---` into a document with ONE `!!null`
      ScalarNode — length 1, so the guard was skipped and `appendKey` /
      `Doc.Set` wrote into a scalar's `Content`, which the emitter
      discards. Both write paths reported success while losing everything:
      `gv init` emitted `null` with `WroteConfig` true, and a wizard
      `Save()` dropped every confirmed setting, no error. One
      `ensureRootMapping` helper now normalizes the root for both sites —
      content-free (no Content, or a lone null scalar) is replaced by the
      seed mapping, carrying the discarded node's comments; a scalar or
      sequence root holds real data, so it is an ERROR, never an
      overwrite. `Save()` keeps a backstop check so a write that could not
      land can never report success. `config.SaveHotkey` had the same
      guard and the same blind spot (symptom: a spurious "top level is not
      a mapping" on a `null` config) — its root is now coerced in place,
      exactly as `ensureMap` already did for null sections.

- [x] Refresh path for already-seeded orchestrator brains (grove-190,
      2026-08-29): grove-189 improved the embedded seed, but
      `buildCockpit` writes it only when
      `<workspace>/.grove/orchestrator/CLAUDE.md` is ABSENT — correct (a
      customized brain must never be clobbered), so the fix reached zero
      of the nine already-seeded Mac brains. Every seed write now ends
      with a stamp line `<!-- grove-seed: <short sha256 of the seed
      body> -->`, and `gv init --only orchestrator-md` is the delivery
      path: absent → seed it; stamp matches → "up to date"; stamp stale →
      `CLAUDE.md.new` beside it plus a `diff` hint, **never** an
      overwrite. Drift is decided from the stamp ALONE, so an operator
      who edited the prose around it still gets told when the seed moved;
      a brain with no stamp at all predates stamping, so it is reported
      as hand-managed and left alone (`--force-orchestrator-md` writes
      the `.new` when the human asks for it). `gv doctor` gains the
      "orchestrator brain up to date" warn row with `gv init --only
      orchestrator-md` as the remedy — green on a fresh seed, on an
      unseeded workspace (the cockpit seeds it on first run) and on a
      hand-managed brain; it flags a moved stamp and says when a `.new`
      is already waiting to be diffed in. Decision logic is pure and
      tested in `internal/bootstrap` (`PlanBrain`/`RefreshBrain`/
      `SeedBrain`); `cmd/gv` is glue; `e2e/wizard.sh` covers the four
      cases end to end. Operator follow-up: one
      `gv init --only orchestrator-md` pass per Mac workspace.

- [x] Orchestrator seed brain refreshed (grove-189, 2026-08-29): the
      embedded seed `orchestrator/CLAUDE.md` — written into
      `<workspace>/.grove/orchestrator/CLAUDE.md` on first cockpit run —
      had not moved since `gv pause` (grove-90), the context-rot rescue,
      and the remote train (#176–#179) shipped, so every workspace seeded
      from it started out not knowing those verbs exist (`grep -c handoff`
      → 0). Now the tools block carries `gv pause <ticket> [--force]` and
      `gv handoff <ticket> --to/--from <host>`, `gv audit`'s class list
      gains `paused`/`idle` and orphan claude/mcp processes, `gv adopt`
      says it revives paused tasks too, and `gv sweep` lists its real
      offers (idle → pause, orphan process → kill). Duty 6 gains the rule
      that **paused rows are not cleanup** — a ⏸ is the operator's
      bookmark, never swept, never untracked on the orchestrator's own
      judgment. Two new duties: **context-rot rescue** (detect from
      `gv cost --json` — turns past ~80 with no PR, or cache_read÷turns
      past ~150k — checkpoint nudge template, then pause → adopt on
      confirm, with the caution that `adopt` resumes the stored session
      FIRST and would resurrect the rotted context) and **remote
      overflow** (when to propose a handoff, the `--to`/`--from` flow,
      host names come from `hosts:` in config). Provider-neutral
      throughout: `DEV-X` and "the operator", no workspace-specific
      dispatch conventions, and the existing dispatch-and-dismiss section
      kept. Already-seeded brains are unchanged — `buildCockpit` writes
      the seed only when the file is absent; that delivery gap is #190.

- [x] Chat sessions are reapable (grove-203, 2026-08-29): grove-198 gave a
      workspace's detached orchestrator chats their own
      `grove-chat-<label>-<n>` tmux sessions — deliberately outside
      `grove-<label>`, so an attaching ssh client can't resize the
      cockpit's shared windows and the chat outlives the ssh drop — and
      nothing reaped or reported them. `gv park` killed only the cockpit
      session, leaving one claude process per chat alive and invisible;
      `gv audit` knew nothing about them, so on a remote host reached only
      over ssh there was no surface at all to notice them from. Now:
      `tmux.ChatSessions` enumerates them (pid, pane command, attached,
      uptime), resolving the `grove-chat-app` name collision through the
      workspace registry the way `closablePane` does — cockpit wins, and a
      nil check yields NOTHING, so an uninjected caller under-reports
      rather than over-kills a dashboard. `gv audit` gains a report-only
      CHAT SESSIONS block and an additive `chat_sessions` JSON field.
      `gv park` keeps them running by design ("propose, then dispose" — a
      chat is the operator's own conversation) but NAMES each survivor with
      its pid and attach line; `gv park --chats` is the explicit reap. The
      cockpit's X modal says the same thing before the keypress, since the
      kill takes the dashboard that would print it. Either way the parked
      event records `chats` (and `chats_killed`), so a park that leaves
      processes running is durable rather than silent. `e2e/chat.sh` covers
      audit → park → `park --chats`, and asserts the colliding `chat-app`
      cockpit survives the reap.

- [x] Chat rows age on ACTIVITY, not pane birth (grove-228, 2026-08-31):
      every chat in the phone list read as days old, the busy ones worst of
      all — the groveremote cockpit said "4d ago" while its agent worked,
      because `created` meant two different things by kind (tmux pane birth
      on a live row, transcript ModTime on an archived one) and nothing was
      ordered by recency at all. `chat.Row` gains **`last_active`**: the
      transcript's mtime on EVERY kind, zero on a live pane whose
      `session_id` is still null (never its own birth dressed up as
      activity), with `Row.Activity()` the one fallback both the ordering
      and every display read. **Additive only** — `created` keeps its exact
      meaning, which is what `e2e/plugin.sh` guards. `chat.Less` sorts on
      activity ahead of the `created` tiebreak (workspace → kind → n
      unchanged above it), `chatAge` renders a relative age off it, and the
      phone falls back to `created` on the zero (year 0001 is truthy in JS
      — `c.last_active || c.created` would have aged it as 739000d) while
      the project card gains the workspace's freshest activity, so the
      projects screen finally answers "where was I?". The label is
      deliberately untouched: showing the newest turn instead of the first
      prompt changes what a row IS and is a separate call. Live on the Mac:
      the attached, busy `grove-grove-repo` cockpit reports
      `created 10:07` and `last_active 13:43` on the same row — 4h of pane
      age and 11 minutes of silence, told apart at last. **`e2e/chat.sh`
      only ever ran on GNU userland** — `touch -d @<epoch>` and a
      `/tmp`-vs-`/private/tmp` scratch path both fail on macOS, so the
      whole `chat ls` half of the suite was dark on the operator's own
      machine; both are fixed here (`set_mtime`, `pwd -P`) and it is green
      on the Mac for the first time.

- [x] `gv chat` resolves the Claude config dir PER WORKSPACE (grove-227,
      2026-08-31): `internal/transcript` resolved it once per process
      (`GV_CLAUDE_CONFIG_DIR`, else `~/.claude`), so the reader — and
      therefore `gv chat serve`, ONE process serving every workspace — was
      blind to thegrid, whose orchestrator runs on the work subscription
      (`ccwork`, i.e. `CLAUDE_CONFIG_DIR=~/.cc-work`). thegrid showed one
      unidentified read-only cockpit card and no history; the env var could
      only move the blind spot, never close it. `claude_config_dir` was
      already written in `~/git/thegrid/.grove/config.yaml` saying exactly
      this — and was inert, on no struct and read by nothing. Now it is a
      real top-level `config.Config` field (`~/` expanded), **workspace
      layer only**: `merge.go` drops it from the global layer beside
      `orchestrator`, so no global key can ever redirect every workspace's
      reader at one subscription's transcripts. `transcript` grows
      `ProjectDirIn`/`ListSessionsIn`; the old names are wrappers passing
      `""`, which still means GV_CLAUDE_CONFIG_DIR-else-`~/.claude`, so
      every non-chat caller (adopt, cost, hooks, worker spawn) computes a
      byte-identical path. Only the four chat call sites change; a
      `chatRecord` now CARRIES its `ConfigDir` rather than re-deriving it.
      Precedence at the resolver is workspace key → env → `~/.claude`: the
      key wins deliberately, so thegrid reads the same in any environment
      while the env keeps serving the e2e harness. Writability is untouched
      — a cockpit row stays read-only everywhere. Live on the Mac, no env
      var, one command: thegrid archived **0 → 107** and its cockpit gained
      a label, while grove-repo 37, unbrewed 116, waterhouse 19, warcraft 4,
      xteink 2 all held exactly still (182 → 289 rows, +107 exactly).

- [x] Profile picker for phone-spawned chats (grove-225, 2026-08-31): the
      phone could only spawn on the host's default lane — `+ new chat`
      POSTed an empty body and `chatSpawnReq.Profile` stayed "" — while the
      desk has had profiled spawns since grove-36/41/105. One additive
      route on the closed table, `GET /api/profiles`, lists the host's
      configured profiles on `ResolveOrchestratorProfile`'s own semantics
      (sorted, **names only**: a profile's `base_url` and auth env var stay
      on the host, since the page leaves the house), and
      `POST /api/workspaces/<l>/new` takes an **optional** `{"profile"}` —
      an absent body, `{}` and `{"profile":""}` are all the byte-compatible
      grove-218 spawn, so an older client keeps working. The name travels
      into the same `chatSpawnReq` the CLI fills, which means the refusals
      come for free and unchanged: an unknown profile is `cfg.ResolveProfile`'s
      own words with a 409, spawning nothing, and `e2e/chat.sh` proves that
      text is byte-identical to `gv orchestrator new --profile <bad>`
      rather than asserting it by eye. Never a quiet fallback to the
      default lane — an operator would find out which backend billed them
      a day later. `DisallowUnknownFields` on the body for the same reason:
      a typo'd key is a 400, not a silent spawn on the wrong backend. The
      UI is a bottom sheet (the reachable third of a phone screen) with
      "Claude (host default)" first, and it exists **only** when the list
      is non-empty — zero profiles renders no sheet and `+ new chat`
      behaves exactly as before, which is also where a failed
      `/api/profiles` fetch degrades to: the lane list is garnish, the
      spawn button is not. Table tests for the route parse and the body
      decode (present/absent/empty/unknown-field), e2e for the wiring —
      a profiled phone spawn lands in the profile's own cwd inside its
      backend wrapper.

- [x] `gv chat serve` — the phone UI (grove-218, 2026-08-31): train 4/4
      of `gv chat` (design §6–8), and grove's **first HTTP listener** (it
      used net/http as a client only). One `net/http` server plus one
      embedded page — `index.html` + `app.js` + a vendored, version-pinned
      `marked.min.js`, hand-written in the spirit of `site/` and served
      from `embed.FS`, so the repo still has **zero JS toolchain**: no npm,
      no lockfile, no node on the host, no bundler. Three screens
      (projects → a project's chats → the chat) over seven routes that are
      exactly the `gv chat` verbs: `/api/chats` is grove-215's payload
      verbatim, `/api/chats/<s>/events` is grove-216's `tail --follow`
      forwarded as SSE **byte for byte** (so a browser parses what a pipe
      would), `send`/`keys` are the relay path unchanged, and
      `workspaces/<l>/new` + `chats/<s>/resume` are grove-198/217. Chats
      are addressed by tmux SESSION NAME wherever they have one — that
      comes straight from tmux, while an id can be absent: grove-222 leaves
      `session_id` null rather than guessing which conversation a pane it
      did not spawn is running, so such a row still LISTS and still takes
      input (its pane is real) and only its history is unavailable, with
      the UI naming `gv chat restamp` as the fix. Bind safety is the ticket: **loopback by default**
      (`tailscale serve` in front of it is the whole auth story; any other
      bind prints a paragraph naming what someone on that network could
      do), off unless invoked, and a **closed route table** — no `done`,
      no `untrack --rm`, no backend mutation, asserted by a test and by
      e2e 404s rather than described in a comment. Mutating requests must
      carry `Content-Type: application/json`, which is what stops a page
      in another tab driving the server (no preflight is answered, no CORS
      header is sent); the shell ships a strict CSP, which — not marked —
      is what makes rendering agent markdown safe. The one pane scrape in
      the whole subsystem is modal detection for the raw-key row, and it
      is falsified against the shapes that must NOT fire (a markdown list
      above the box, digits typed mid-sentence, an idle box). Handlers via
      `httptest` with a fake backend; `e2e/chat.sh` covers the real wiring
      end to end — a browser POST spawns a chat, reaches a live agent, and
      its reply comes back over SSE. Zero new Go dependencies.

- [x] `gv chat tail` + `gv chat send` (grove-216, 2026-08-31): train 2/4
      of `gv chat` (design: `docs/plans/2026-08-31-gv-chat-design.md`
      §3–4). `gv chat tail <session> [--follow] [--since N]` emits the
      chat's transcript as JSONL — `{seq, role, kind, text, tool, ts}`, one
      entry per CONTENT BLOCK so a client can collapse tools without
      losing the prose beside them. Read from the transcript, never the
      pane: the `.jsonl` is append-only, so following it is a byte offset
      and a 250ms poll, with no tmux, no ANSI and none of the chrome that
      has changed under us twice. `seq` counts emitted entries in file
      order, which is what makes it stable enough for `--since N` to
      resume; bookkeeping lines, `isMeta` injections and `isSidechain`
      subagent traffic project to nothing, and only COMPLETE lines are
      consumed, so a 200KB `tool_result` landing in two writes is never
      half-parsed (bufio.Reader, never Scanner — a Scanner silently drops
      the long line). A `tool_result` is paired back to the NAME of the
      `tool_use` it answers, since the raw block carries only an opaque
      id. `gv chat send <session> "<text>"` reuses `tmux.PasteText`
      wholesale — the grove-144 sequence (server-global `gv-relay` buffer,
      BRACKETED paste, settle, a SEPARATE Enter, then a scrape that proves
      the submit landed, one retry, then a loud non-zero) — aimed at the
      immutable `%N` pane id, never a name that could prefix-match a
      sibling (grove-116/78). `gv chat keys <session> <chars>` is that
      rule's own exception: a picker keystroke, raw and un-Enter-wrapped,
      because a permission prompt acts on the keypress itself. Both write
      verbs gate on the row's `writable` FIELD (grove-215) rather than a
      second reading of `kind`, so the CLI and a phone can never disagree;
      a cockpit pane's refusal points at `tmux attach`, an archived one's
      at `gv orchestrator new --resume`. `e2e/chat.sh` now covers the
      whole loop against a stand-in claude that appends what it is given:
      spawn → ls → send → **tail sees the message**, plus `--since`,
      `--follow` inside 1s, both refusals, and a raw key that leaves the
      transcript untouched.

- [x] Chat identity + `gv chat ls --json` (grove-215, 2026-08-31): train
      1/4 of `gv chat` (design: `docs/plans/2026-08-31-gv-chat-design.md`
      §1–2). `tmux.ChatSessions` knew a workspace's live chat sessions and
      `transcript.ListSessions` knew the session ids in its orchestrator
      project dir, and **nothing joined them** — "newest .jsonl" is
      ambiguous the moment a workspace has two chats, because they share
      one project dir. Now a chat's Claude session id is resolved lazily
      (the id is minted by claude on boot, not known at spawn) as the
      newest UNCLAIMED transcript whose cwd matches the pane's, then
      stamped once on the pane as the user option `@grove_chat_session` and
      never re-derived — a pane user option is durable where a pane title
      is not (Claude Code overwrites titles on boot). `gv chat ls
      [--workspace L] [--json]` reports all three states a chat can be in:
      `chat` (live detached, the only `writable: true` kind), `cockpit`
      (the cockpit's own orchestrator pane — identified by cwd, since the
      dashboard and a grove-199 remote pane both sit at the workspace root)
      and `archived` (a transcript with no live pane). No `--workspace` =
      every registered workspace, each row carrying its own (grove-191's
      workspace-transparent shape); an unresolved chat reports `session_id:
      null` and resolves on the next call. The `kind: chat` filter still
      routes through the registry's cockpit answer, so a nil `CockpitCheck`
      keeps under-reporting — `gv park --chats` kills what chat rows
      describe. `e2e/chat.sh` covers spawn → ls (distinct + stable ids for
      two chats in one project dir, null for a booting one, the cockpit
      pane read-only, an archived transcript, cross-workspace rows) and now
      runs in the hostile-tmux-conf pass too.

- [x] `gv orchestrator new --resume <session-id>` revives an archived chat
      (grove-217, 2026-08-31): train 3/4 of `gv chat` (design §5). Most
      orchestrator chats on a host are transcripts with no live pane —
      readable history and nothing else. `--resume` is `gv adopt`'s pattern
      applied to chats: it allocates the next `grove-chat-<label>-<n>` and
      launches the orchestrator command with `--resume <id>` in it, same
      detached shape as grove-198. Claude Code re-fires SessionStart with
      the **same** session id (verified ≥6 days after the pane died), so
      identity survives — and because the id is known at spawn time (the
      one case where it is), the pane is stamped `@grove_chat_session`
      immediately rather than left to grove-215's lazy resolver, which
      would otherwise hand a revived chat the newest transcript in the dir.
      `gv chat ls` then shows the same id as `kind: chat, writable: true`.
      The conversation's own cwd decides its backend (Claude Code keys
      resume by project dir, and a profiled chat has its own — grove-36
      T4), so the profile is INFERRED from where the transcript lives and
      `--resume` with `--profile` is a hard error rather than a precedence
      rule; the flag lands inside `WrapProfile`'s `exec`, never after it.
      Everything is refused before anything is created: an unknown id (a
      revival from the wrong dir is looking in a project dir that does not
      hold it, in a DETACHED pane nobody is reading), a malformed one (the
      id reaches a shell command line), and one a live pane already holds
      (two claude processes on one append-only transcript). Composes with `--host`: the
      id is one more NAME that travels, resolved against the HOST's
      transcripts, and the op-id receipt still makes an ssh-255 retry spawn
      exactly once. Unlabelled, `--resume` takes the ambient workspace.
      `e2e/chat.sh` covers spawn → park → revive → identity continuity,
      both plain and relayed, in both tmux-conf modes.

- [x] Chat identity is known by CONSTRUCTION, not inferred (grove-222,
      2026-08-31): grove-215's resolver sorted live panes newest-created
      first and handed each the newest unclaimed transcript. Wrong, and
      found only by live verification on groveremote: transcript recency is
      LAST WRITE, so an older pane still working outranks a younger one gone
      idle — two live chats came back stamped with each other's ids, stably
      (all #215's acceptance demanded) and durably (the stamp is never
      re-derived), which pointed `gv chat tail` at the wrong conversation
      and `gv chat send` at the wrong agent. Now: grove MINTS the session id
      (`chat.NewSessionID`), passes it to `claude --session-id <uuid>` and
      stamps the pane at creation — every chat grove spawns, detached
      (`spawnWorkspaceChat`) or cockpit (`spawnOrchestrator`,
      `spawnOrchestratorProfile`), wears its identity from second zero, and
      the flag goes on BEFORE `WrapProfile`'s `exec` like `--resume` does.
      For a pane grove did not spawn, GROUND TRUTH replaces recency: the id
      the pane's agent was launched on, read out of its argv through the
      process tree (`chat.PaneSessionID` over `ps -Ao pid,ppid,args`; the
      pane pid is a shell, so the walk is over descendants). That pass also
      SELF-HEALS a stamp that is already wrong. What is left of inference
      answers only when it cannot guess: `chat.Resolve` pairs a pane with a
      transcript only where exactly ONE unstamped pane competes for a
      project dir (the cockpit's `--continue` pane, whose id grove cannot
      mint) — rivals stay `session_id: null`, because a missing id costs a
      client a button and a wrong one pastes into the wrong agent. `gv chat
      restamp <session> [<id>]` clears or re-points a stamp by hand for the
      cases neither source can reach. Unit tests pin the exact live
      inversion (younger pane idle, older pane active) and fail under mtime
      pairing; `e2e/chat.sh` proves the minted id reaches the program, that
      the decoy transcripts recency would have paired stay archived, that an
      unstamped pane re-derives from its RUNNING process, and that two
      unidentifiable rivals both report null.

- [x] Paste-able attach hints survive zsh (grove-207, 2026-08-29): a word
      that STARTS with `=` is equals-expanded by zsh (macOS's default
      shell), so the printed `tmux attach -t =grove-chat-<label>-<n>` died
      with `zsh: grove-chat-… not found` before `ssh` ever ran — the
      copy-paste path was broken on exactly the machine `@` was built for.
      The `=` stays (tmux's exact-match anchor, the grove-99 rule); the
      target is quoted instead. `remote.Quote` no longer treats a leading
      `=` or `~` as safe (both are word-initial expansions in zsh), which
      also fixed the cockpit's own ssh-attach pane command; both printed
      hints — the host's `attach:` line and the local `from here:` line,
      now rendered by the one `remoteChatAttachCmd` — carry the quotes.
      The `attach:` line is also the machine-readable carrier, so the
      parser went permissive FIRST: `ParseChatSession` accepts the target
      bare, single- or double-quoted (a half or mismatched quote is a
      truncation and yields no hint), keeping new-local/old-host skew
      working. `e2e/chat.sh` asserts the quoted print; `e2e/cockpit.sh`'s
      fake host now emits the quoted carrier while the ssh.log assertion
      stays bare — the pane shell strips the quotes, which is the proof
      they are transparent to ssh and tmux.
- [x] Kickoff step-2 subagent-fanout guidance in `md_default.tmpl` (grove-115,
      2026-08-27): replaced the short step-2 in the markdown-default kickoff
      template with the longer fan-out variant — instructs workers to use an
      Explore subagent when reconnaissance spans ~3+ files or unfamiliar
      territory, read directly when file/line is known, and not to ask for
      confirmation on implementation ambiguities. No other templates touched
      (manual/pickup have different structures; linear templates untouched).
      All `internal/kickoff` tests pass.

Grove is the operator's live daily driver and dogfoods itself: the real
backlog is **GitHub issues on this repo** (`grove-N` = issue #N), worked
by grove workers. Live tests + hooks install happened long ago; day-to-day
flow is issue → `gv grab grove-N --repo grove` → PR → merge → `gv done`.

- [ ] Phase 1 remainder: 1b pack loading, 1c drift detection (below)
- [ ] Phase 4 remainder: hooks/inbox generalization, generic orchestrator
      CLAUDE.md + pack overlay (below)
- [ ] Phase 5: learnings system first cut (below)
- [ ] Phase 6: OSS polish → Grid pack → parity gate → ovs retirement
- [ ] Parked-but-tracked side quests: mobile cockpit v2 (issue #5, planned),
      Obsidian live board (issue #9, design paused at REVISE), remote
      overflow host (docs/remote-host-setup.md; train #176–#178)
- [x] Cockpit `@`-armed remote spawn (grove-199, 2026-08-29, part 2 of the
      remote-orchestrator pair; #198 is the verb it calls): from the local
      cockpit, `@` arms a remote spawn and the NEXT key opens an
      orchestrator chat on the host — `0`/`O` the host's own Claude, `1-8`
      the same digit→profile map the local keys and the `)` picker bind
      (only the profile NAME travels), `)` the picker with an `@<host>`
      banner. Transient state, not a mode: it clears on the spawn, on esc,
      and on any other key (which cancels with a flash rather than falling
      through to its local meaning), so no local spawn key changed
      meaning. One host arms directly, a repeated `@` cycles hosts (sorted,
      the R-merge order), zero hosts flashes `no hosts configured`. The
      spawn relays #198's verb with a fresh op id, and ONLY on success
      opens a local pane running `ssh -t <ssh> tmux attach -t =<session>`,
      re-tiled through the existing `SpawnPane`/`SelectLayout` path — a
      failed relay surfaces the remote's own error line as the flash and
      spawns nothing (never a dead pane). The relay runs
      `remote.RunDetached` (stdin closed: ssh must not race the TUI's key
      reader) with both streams captured (a stray write corrupts the
      alt-screen), via `runRemoteIdempotentWith` — the grove-186 hop with
      its ssh call and notice stream injected. Pane identity: remote panes
      carry `@grove_remote` (the same OSC-proof user-option carrier as
      `@grove_profile`) and read `@<host> · <profile>` in the border
      status, in their own border color; local panes are unchanged.
      Keypress-driven throughout — no poll, no goroutine (the cockpit RAM
      rule). Also fixes the #200-review follow-up: a `grove-chat-*` session
      can now self-close via `gv orchestrator close` (its single pane is
      the orchestrator, not a dashboard), so a fire-and-forget remote chat
      no longer strands its claude process alive on the host. The
      exemption is decided by the workspace REGISTRY, not the name shape
      (#204 review): a workspace labelled `chat-app` owns cockpit session
      `grove-chat-app`, the same string a chat session produces, so
      `closablePane` takes an injected `tmux.CockpitCheck`
      (`cockpitSessionCheck`, built from the registered labels) and an
      ambiguous name collapses to COCKPIT — a nil check treats every
      session as one, so forgetting to inject is only ever
      over-protective. `e2e/cockpit.sh` gained a workspace
      cockpit driving `@`+digit against a fake ssh (relay argv, the local
      ssh-attach pane command, the pane's identity tags, and an error path
      that spawns no pane); `e2e/chat.sh` covers the self-close.
      Nested-tmux prefix capture on the attach pane is the accepted
      tradeoff (documented in the `?` overlay, as with worker attach).
- [x] `gv watch` — a transition stream monitors can trust (grove-205,
      2026-08-29): new read-only `internal/watch` + `gv watch [--json]
      [--ticket X]... [--type t,…] [--sentinel s,…] [--since <RFC3339> |
      --replay] [--until <sentinel>]`, tailing the ambient workspace's
      events.jsonl one flushed line per event. Baseline is FROM NOW by
      construction (the offset is taken at process start), so the
      "before-snapshot sampled after the fact" failure is gone as a
      category; `--until done` exits 0 exactly when that transition lands
      (the one-notification shape). Default type set covers every
      terminal/actionable state — agent_status incl. the idle stop with no
      STATUS line, notification, session_ended, task_done, task_untracked,
      task_paused — so a crashed worker is never silent. Additive
      `sentinel_at` on the task view for poll-based consumers. Filed by two
      false DONEs in one minute on 2026-08-29, both from a pane grep for
      `STATUS: DONE` — a string the kickoff prompt plants in every worker's
      pane from second zero. Templates deliberately unchanged; the fix is
      making the pane unnecessary. Docs: orchestrator/CLAUDE.md Monitoring
      section, docs/plugins.md, tmux-discipline skill, LEARNINGS.md. New
      `e2e/watch.sh` (in `e2e/all.sh`) greps the live pane to prove the
      trap, then proves watch is immune.
- [x] Remote orchestrator chat: `gv orchestrator new --host` (grove-198,
      2026-08-28, part 1 of the remote-orchestrator pair; the cockpit `@`
      prefix + attach pane is #199): starts an orchestrator chat ON a host,
      inside that host's TWIN of the calling workspace. Local half mints an
      op id and relays `orchestrator new --op-id <id> --as <host>
      --workspace <label> [--profile p]` through the hop grove-186 built
      (`runRemoteIdempotent`, now shared with answer/nudge), auto-filling
      the label from the ambient workspace; `orchestrator` joined
      `remote.Supported` (other subcommands get the friendly error).
      Receiving half resolves the label against ITS registry
      (`workspace.ResolveTwin`) and the profile name against the TWIN's
      config, then spawns a detached `grove-chat-<label>-<n>` session
      (`tmux.NextChatSession`/`CreateChatSession`) in the twin's
      orchestrator dir, seeding CLAUDE.md exactly as `buildCockpit` does.
      Own session, not a cockpit window: an ssh client attaching must not
      resize the cockpit's shared windows, and the chat outlives the ssh
      drop. Only NAMES travel — no twin, a dead marker, or an unknown
      profile is a hard non-zero error (`no workspace '<label>' on
      @<host> — register a twin there or spawn locally`), NEVER a
      fall-back to the host's global layer (the 2026-07-05
      ccwork-inheritance hazard). Idempotency: the receipt is checked
      against the TWIN's state before anything is created and the spawn
      appends `orchestrator_spawned` (additive, workspace-scoped, data
      `{workspace, session, profile?, op_id?}`) carrying the session name,
      so a 255 retry reprints the first spawn's attach line instead of
      making a second chat. Output is paste-able from either end: the host
      prints `attach: tmux attach -t =<session>`, the local half parses it
      back and adds `ssh -t <ssh> tmux attach -t =<session>`. New
      `e2e/chat.sh` (fake ssh, second tmux server, real-server canary) in
      `e2e/all.sh`; docs/remote-host-setup.md gained a "Workspace twins"
      section replacing the workspace-free rule.
- [x] Idempotent relayed mutations + delivery confirmation (grove-186,
      2026-08-27, closes the #176–#186 remote-overflow train): **(A)** a
      retried `gv answer/nudge --host` can no longer double-steer a worker.
      The sender mints a client op id (`remote.NewOpID`, 16 crypto/rand
      bytes as hex — no uuid dependency) and prepends `--op-id <id>` to the
      hop; ssh exit 255 is ambiguous (the remote may or may not have acted)
      so the SAME argv re-runs once after ~2s, and a second 255 surfaces the
      op id plus the exact manual retry command. The receiver matches
      `--op-id` in leading-flag position only (#184's rule for the relay
      verbs, since free text may mention it) and checks `state.SeenOpID`
      BEFORE anything a retry would repeat — hit ⇒ `✓ already applied`,
      no tmux send, no event. Additive by construction: with no op id
      `Data` is nil and `omitempty` drops the key, so the record is
      byte-for-byte today's (`TestAnsweredEventByteShape`; `e2e/relay.sh`
      and `e2e/plugin.sh` stay green). **(B)** ✓ now means "the worker
      heard you", not "keys were sent" — the three swallowed nudges of
      2026-08-26 were pasted into panes that were booting or mid-`/compact`,
      where an EMPTY input box reads as landed to `pasteLanded`. `PasteText`
      became `(warn string, err error)`: it refuses to send while the pane
      shows `Compacting conversation` (error ⇒ caller records nothing) and,
      after a verified submit, scrapes up to 15s for POSITIVE uptake — the
      probe echoed outside the input box, or `esc to interrupt`. No
      evidence still records the event (submit was verified; grove-144's
      stance holds) but returns a warning: stderr for the CLI, the flash
      for the cockpit. The single-char `SendRawKey` picker path skips both.
      Two traps found while proving it: the uptake scrape must read bounded
      SCROLLBACK (a long prompt pushes its own head off-screen; unbounded
      would match an older identical relay), and e2e pane assertions must
      squeeze ALL whitespace — `capture-pane` strips trailing spaces, so a
      wrap on a space welds the words either side. Also fixed a
      pre-existing red `internal/github` test on main (wedged-`gh` stub was
      `sh -c "sleep 5"`, whose orphaned child held the stdout pipe open past
      the deadline and made a working timeout look dead).
- [x] Cockpit acts on `@host` rows (grove-185, 2026-08-27, cockpit half of
      the remote-overflow train): #178's blanket read-only gate now lifts
      for LIVE remote rows — `a`/`n` open the existing detail input bound
      to the row's host and submit relays `gv answer|nudge` over ssh
      (`remote.Argv`, one-shot `tea.Cmd`, 10s ctx, first output line to the
      flash); `d` and `enter` suspend via `tea.ExecProcess` (`gv diff |
      less -R`, and the field-proven #177 `ssh <ssh> -t <gv> attach`).
      Deliberate divergence from local rows: `d` is NOT done and `enter` is
      NOT the detail view, so a cockpit `done` can never fire at a remote
      worker. `v` stays blocked (the reviewing flag is local backend
      state) and handed-off tombstones stay read-only bookmarks. Nothing
      local is appended — the remote host records its own `answered` event
      — and a remote-bound detail skips `paneTailCmd` entirely (scraping
      here could hit a local pane that merely shares the name, grove-116
      class), rendering a fixed attach hint instead. No new goroutine,
      poll, or cache: every path is one keypress → one process, the `R`
      merge fetch's class. `e2e/cockpit.sh` drives the live TUI with
      send-keys against a fake ssh and asserts each argv.
- [x] Remote `--host` passthrough for five more verbs (grove-184,
      2026-08-27, tail of the #176–#178 remote-overflow train): `answer`,
      `nudge`, `diff`, `pause`, `untrack` join `grab/ls/adopt/handoff` in
      `remote.Supported` — the remote gv does its own guarding, interactive
      confirms already stream over ssh (#177). The one verb-specific rule
      (the filing's "pure passthrough" was wrong): relay free text may
      legitimately contain `--host` (`gv nudge grove-7 try gv ls --host
      pc`), so `answer`/`nudge` match `--host` only in leading-flag
      position via new `remote.ExtractHostPrefix` (scan stops at the first
      non-flag arg; `--host=X` and trailing bare `--host` behave as in
      `ExtractHost`); the no-free-text verbs keep whole-argv
      `ExtractHost`, so trailing flags relay exactly like `grab`/`ls`.
      `e2e/handoff.sh` asserts the fake-ssh hop for nudge (leading
      `--host`), diff/pause/untrack (trailing `--host`), and the
      free-text-mentions-`--host` case stays green.
- [x] Global layer is workspace-transparent (grove-191, 2026-08-27, remote
      train #184–#186): `gv ls` run at the global layer (no ambient
      workspace) aggregates the global state + every alive registered
      workspace (new `internal/workspace.FindTicket`/`stateDir` Peek
      helpers) into one fleet view; `--json` rows gain the additive
      `workspace` field (label; omitted for global-layer rows), the
      human table gains a conditional `WORKSPACE` column tagging rows
      `@<label>` — inside a workspace and on a no-registry machine the
      output is byte-identical to before. The ticket verbs (`answer,
      nudge, diff, pause, untrack, adopt, attach, done`) and `grab
      --repo <workspace repo>`, run at the global layer against a ticket
      the global state doesn't know, re-exec the same binary
      (`os.Executable()`, identical argv, `cmd.Dir` = workspace root,
      streamed stdio, exit code propagated) after one `→ workspace
      <label>` line; two workspaces owning the ticket error naming both;
      no recursion (the child resolves ambient and never scans the
      registry). Ambient label derivation now runs the registry's own
      `ValidateLabel` (a cloned grove repo with derived label `grove`
      failed loudly with a `workspace.label` pointer instead of building
      a `grove-grove` session; hook receiver and fix-path verbs exempt).
      Contract: `workspace` additive row field; docs/plugins.md;
      `e2e/workspace.sh` covers all of it.
- [x] VPS grove-host runbook (grove-179, 2026-08-22): `docs/pc-remote-host-setup.md`
      (WSL2 PC, blocked on BIOS) rewritten as `docs/remote-host-setup.md` —
      Ubuntu VPS over Tailscale SSH as overflow for the Mac-is-home topology
      (#176 hosts/--host, #177 handoff, #178 fleet view): sizing, stack,
      Mac-side `hosts:` block, headless gotchas, phone access; WSL2 kept as
      an appendix, old path reduced to a redirect line.
- [x] `gh()` timeout (grove-164, 2026-08-20): `internal/github.gh()` ran
      every `PRForBranch`/`PreviewURL`/`Merged` call via plain
      `exec.Command(...).Run()` with no deadline — a wedged `gh` (offline,
      stalled network) hung the caller forever, and the cockpit's 30s PR
      poll beat stacked another batch of hung children each cycle after
      its own 6s `FetchAll` timeout walked away. Mirrored the existing
      `runGH` pattern (`internal/provider/github.go`): `exec.CommandContext`
      with a 15s deadline (package var `ghTimeout`, test-overridable). One
      function change bounds every caller at once. Accepted trade: a call
      that would've succeeded past 15s now fails fast instead — error
      display honesty is #124, not this ticket.
- [x] `state.Load` skips a malformed events.jsonl line and keeps folding
      instead of stopping at the first decode error (grove-166,
      2026-08-20): one crash-torn line buried mid-file was silently
      dropping every later event for every CLI command, and the
      `tasks.json` rewrite that follows `Load` persisted the truncated
      fold — `gv ls`/`audit`/`sweep` lost tasks created after the torn
      line until the log was hand-repaired. Now matches `Folder.consume`'s
      skip-and-continue behavior exactly (deliberate divergence from the
      ovs-byte-comparable copy, recorded in docs/seed-manifest.md).
- [x] cost.Cache evicts dead transcript paths on the fleet sweep
      (grove-165, 2026-08-20): `(*Cache).Retain(keep)` drops every
      `byFile`/`latest` entry whose path isn't in keep; the TUI costs page
      sweep (`costsCmd`, the one full-fleet enumeration) collects the
      union of session files via `UsageForTaskCollect` and Retains once
      per sweep. Per-task callers (audit goroutines, `gv cost`) still use
      `UsageForTask` and can never evict — no sibling-eviction re-parse
      churn. Closes the cockpit RAM ratchet from done/untracked tickets
      and Claude's ~30-day transcript pruning.
- [x] Cockpit computes the feed tail once per frame (grove-167,
      2026-08-20): `View()` now builds `feedItems` and the
      `latestAnswered` map (fxFull only — lower fx never built it) once
      and passes them down to `rowBudgets`/`viewActivity`/`viewScene`→
      `applyCast` as arguments, instead of each helper rebuilding the
      200-event tail (~3x/frame) and the answered map (~3x/frame). Pure
      render-path refactor: no new model fields or caches, byte-identical
      output (existing scene/view/footer tests as oracle).
- [x] `gv handoff grove-N --to/--from <host>` (grove-177, 2026-08-22, part
      2 of the remote-overflow train on `feature/remote`): moves a running
      task between hosts by composing existing verbs — guard (mid-turn /
      untracked / no branch) → checkpoint nudge with the context-rot
      handoff template + idle wait (hooks are truth; default 10 min,
      `--timeout`) → verify via git/gh (branch on origin, local == remote
      head, clean worktree, open PR whose body has the five headings or
      >200 chars) → dry-run plan + confirm (`--yes`) → `gv untrack` (+
      window close; `--rm` for the full teardown) → `ssh <host> gv adopt
      --repo --branch` → tombstone. `internal/handoff` is the sequencer
      over a Runner seam (fake-runner tests: every guard aborts before
      any mutation, verify failure before untrack, remote-adopt failure
      leaves NO tombstone and prints the retry). Tombstone = additive
      `task_handed_off` event → `Task.HandedOffTo`; `gv ls --json` rows
      carry `handed_off_to` (+ `live: handed-off`), the table prints a
      `→ host` pointer line; it also drops the stored session id so a
      pull-back is a cold pickup-prompt adopt (the transcript never
      travels — the PR body is the carrier; `gv cost` rows split across
      hosts, documented not solved). `--from` = remote `ls --json` +
      remote `handoff --release --release-to <self>` over ssh + local
      cold adopt. `remote.Supported` gains adopt/handoff. `e2e/handoff.sh`
      (fake `ssh`/`gh` on PATH, second state dir + second isolated tmux
      server) proves both directions; `e2e/plugin.sh` green.
- [x] Remote hosts: `hosts:` config + `--host` passthrough for grab/ls
      (grove-176, 2026-08-22, part 1 of the remote-overflow train on
      `feature/remote`): `config.Host{ssh, gv}` (ssh required, gv defaults
      to `gv`, unknown name errors listing configured hosts); new
      `internal/remote` builds `ssh -o BatchMode=yes <target> -- <gv>
      <verb> <args…>` with every passthrough arg single-quoted (`--brief
      "with spaces"` survives) and propagates the remote exit code;
      `main()` intercepts `--host` before dispatch, other verbs get "not
      supported yet". Output is the remote's own envelope — no contract
      change. `gv doctor` gains a warn-severity `host:<name>` row (ssh
      reachability + remote `gv --version`, 8s bound). No state sync.
- [x] One-fleet view: handed-off tombstones + `--remote` merge (grove-178,
      2026-08-22, part 3 of the remote-overflow train on `feature/remote`,
      read-only): a `task_handed_off` event (written by #177's `gv
      handoff`; data `host`, `branch`) folds to `Task.HandedOffTo` — the
      task leaves Active() but `state.HandedOff` lists it, label
      "elsewhere". `gv ls` prints tombstones dim after the live rows
      (`grove-142 ⇢ grove-host  <branch>  handed off 2h ago`); `gv ls
      --remote` runs `gv ls --json --no-pr` on every configured host in
      parallel (new `internal/fleet`: 5s per host, a failure is one
      warning line and never a non-zero exit), rows carry `host`
      ("local" / the host name), a tombstone the host reports live is
      replaced by the live row, one the named host answered without
      renders `⇢ host?`. Cockpit: `R` toggles the merge — one fetch per
      press, no poll/goroutine, board rebuilt in Update (never per
      frame), remote rows tagged `@host`, remote/tombstone rows are
      read-only (enter/n/a/v/d flash where the task lives). `gv audit`
      classes tombstones `handed_off` (report-only, suggests `gv ls
      --remote`; never abandoned, never a sweep offer). Contract: `host`
      and `handed_off_to` are additive row fields; docs/plugins.md.
- [x] Pane targeting survives `pane-base-index ≠ 0` (grove-168,
      2026-08-20): fresh installs with the common `base-index 1` +
      `pane-base-index 1` dotfiles died at the cockpit build and the grab
      placeholder hint, and grab's `.1` claude launch landed in the
      worktree shell. Panes are now targeted like windows (grove-116):
      immutable `%N` ids — `SplitVerticalWindow` returns the split's id,
      new `tmux.FirstPaneID` resolves "the window's first pane" for
      hint/dash/nvim/mobile, `closablePane` protects the lowest-index
      pane rather than `index == 0`. Guardrail: `GROVE_E2E_TMUX_CONF=hostile`
      boots the isolated e2e server with the hostile conf; `e2e/all.sh`
      runs cockpit.sh + workspace.sh in both modes. Grove never
      normalizes the user's numbering.
- [x] `gv update` — self-update from the latest GitHub release (grove-160,
      2026-08-19): fetches `releases/latest` unauthenticated, compares the
      tag to the stamped version, prints `gv vX → vY` and confirms y/N
      (`--yes` skips; `dev` builds refused, `--force` overrides). Replace
      is atomic: temp file in the target's own directory → chmod 0755 →
      rename over `os.Executable()` (symlinks resolved); any failure
      leaves the old binary byte-identical; verified by exec'ing
      `<path> version`. Decision logic in `internal/update` (injectable
      API base, httptest integration incl. the test binary updating
      itself). Later: checksums.txt verification, doctor "update
      available" hint.
- [x] `gv version` / `gv --version` stamped at release build (grove-159,
      2026-08-19): `main.version` (ldflags `-X main.version=<tag>`, default
      `"dev"`) printed as `gv <version> (<GOOS>/<GOARCH>)`; release.yml's
      build job now takes the version job's computed tag as `VERSION` env
      and stamps it in. Prerequisite for `gv update`.
- [x] Worker reap kills the process tree by worktree path (grove-156,
      2026-08-17): `tmux kill-window` only takes the pane's foreground
      group — build/test children (jest-worker) daemonized, reparented to
      launchd, and spun at 100% CPU for days after the task shipped. Both
      teardown paths (`gv done` / `untrack --rm`) now SIGTERM every
      process whose argv references the task's worktree path immediately
      before worktree removal (2s wait, survivors reported, never
      SIGKILL), and audit/sweep gained a `worktree_processes` class
      (additive `--json` field, per-item confirmed SIGTERM in sweep) for
      processes referencing a tracked worktree whose task is done or dir
      is gone. Hard scoping: only tasks.json `worktree` paths — grove
      created them, so ownership is by construction; never a generic
      `.worktrees/` pattern. `orphan_processes` rows gained `rss_kb`.
      Stale done-task claude processes (no path in argv) split to #157.
- [x] Bootstrap panic on empty config.yaml (grove-129, 2026-08-27):
      `internal/bootstrap/writer.go:53` — `Doc.root()` indexed
      `d.node.Content[0]`, which was empty when the config file existed
      but was zero-byte or comment-only. `LoadDoc` + `Get` then panicked
      with `index out of range`. Fixed by treating empty/comment-only
      files as an empty mapping node in `LoadDoc` (and similarly in
      `ensureConfig`); `Get` returns not-found, `Set` creates the mapping.
      Added unit tests for zero-byte, comment-only, and whitespace-only
      files.
- [x] `gv grab --brief "<text>"` (grove-146, 2026-07-29): ad-hoc operator
      instructions at dispatch, so process context (release-scope
      constraints, test-env guidance) no longer had to be written into
      ticket descriptions before each grab. `kickoff.Render` gained a
      `brief` param, appended verbatim as a final `## Operator brief`
      section after all ticket-derived content when non-empty (works with
      `--manual` too — the prompt file persists either way); empty/absent
      stays byte-identical to today's renders. No new state/events — the
      prompt file on disk is the record.
- [x] Cockpit state I/O incremental (grove-126, 2026-07-29): the other
      half of the grove-149 hot path — the 1s beat parsed the append-only
      events.jsonl twice per tick (`state.Load` + `state.ReadEvents`) and
      rewrote tasks.json every second, costs that grow with log age
      forever. New `state.Folder` remembers byte offset + fold state:
      each refresh parses only appended bytes and derives the task map
      AND the 200-event feed tail from one pass (torn appends held back,
      malformed lines skipped, shrunk log refolds, results returned as
      copies); tasks.json rewritten only when the folded view's hash
      changes; the 30s PR-poll fallback went read-only (`ReadTasks`).
      Stop hooks cap stored messages at 2000 runes (classification still
      on full text; reader-side truncation stays #123). `gv ls` on-disk
      cost cache decided as defer — reasoning in the plan doc
      (docs/plans/2026-07-29-grove-126-hot-path-state-io.md).
- [x] Dash refresh batched to O(1) tmux spawns (grove-149, 2026-07-29):
      field report — `gv dash` pegged an external user's CPU at 5-6
      workers; the 1s tick spawned ~6 tmux processes per task per second
      (3× list-windows + list-panes + display-message + capture), and
      per-spawn cost varies ~50× by environment (EDR scanning each exec,
      WSL1). New `tmux.SessionSnapshot`: ONE list-windows + ONE
      `list-panes -s` per tick answer every window/pane question for the
      whole board (glyph tolerance + grove-116 sibling rules preserved);
      `detect.DetectLiveFrom` reads it and only capture-pane stays
      per-task — 6N+2 → ≈N+3 spawns/sec. Stateless `DetectLive` remains
      for one-shot callers (`gv ls`).
- [x] Wizard pre-selects autonomous worker command (grove-147, 2026-07-29):
      the worker-command select listed plain `claude` first while
      `config.Load`'s default (when the key is omitted) was already
      `claude --dangerously-skip-permissions` — manual-mode workers stall
      on every permission prompt, which cascades into `gv nudge`/`answer`
      landing on the prompt and false dead/stalled reads. Reordered
      Options so autonomous is first, reworded the step copy to say
      autonomous is grove's expected mode and warn what manual breaks.
- [x] Relay submit verification (grove-144, 2026-07-29): `gv nudge` /
      `gv answer` / the TUI inline reply pasted text and pressed Enter
      back-to-back with zero settle, so a TUI still ingesting the paste
      swallowed the Enter — the text sat unsent as `[Pasted text]` while gv
      printed ✓ and appended `EvAnswered` (`gv ls` showed a stalled worker
      as `working`; biggest time-sink of Oleg's fresh-install session).
      `tmux.PasteText` now pastes bracketed (`-p`, which the doc comment
      had claimed for a year), settles 250ms, presses Enter, then verifies
      by scraping the pane's input box: one retry Enter, then a non-zero
      exit with a recovery command and NO recorded answer. Verify/retry
      ladder extracted as `verifySubmit`/`pasteLanded` (unit-tested);
      `e2e/relay.sh` proves both legs against paste-consuming stubs; the
      TUI reply moved off the render loop into `relayCmd`.
- [x] PR-poll timer multiplication fix (grove-118, 2026-07-18): the
      `prsMsg` handler unconditionally re-armed its own 30s tick, so every
      ad-hoc `prsCmd` delivery — including manual `r` refresh — added
      another self-perpetuating poll loop (grove-24's `refreshMsg` bug,
      reintroduced for PRs); split into a `prTickMsg` beat that alone
      re-arms, `prsMsg` now pure data application; vestigial one-shot
      `prTickEvery()` (callback returned nil, dropped by bubbletea) removed
- [x] Cost cache eviction (grove-119, 2026-07-18): `cost.Cache.entriesFor`
      kept every `(path, mtime, size)` generation forever — with the
      costs page open, a live worker's continuously-mutating transcript
      inserted a new full-parse entry every 1s refresh and never freed the
      old ones, growing cockpit RAM (reserved for workers) unbounded. Added
      a `path -> newest fileKey` index; on insert, the prior generation for
      that path is evicted, so the cache holds exactly one entry per path.
- [x] Kimi Code plan fuel gauges (grove-133, 2026-07-18): ACCOUNT tab
      rows whose profile `base_url` targets `https://api.kimi.com/` show
      per-window quota gauges under the key line
      (`5h  ▓▓▓░░  62% left · resets in 2h 20m`) — new read-only
      `internal/kimi` client (`GET /v1/usages`, schema per kimi-cli's
      `_parse_usage_payload`, tolerant parsing: garbage → empty, non-200 →
      dash + hint), fetched only inside the one-shot `accountCmd`; unset
      key or failed fetch renders a dash fuel line, never an error state
- [x] Window-side tmux target hardening (grove-116, 2026-07-18): worker
      windows resolve by immutable `@N` id via `tmux.WindowID` — the old
      `session:name` targets prefix-matched, so `repo · grove-1` could
      silently hit sibling `repo · grove-10` (pause/untrack/done killed a
      live worker mid-turn, `gv answer` steered the wrong agent, late
      hooks re-badged the sibling's window); KillWindow/RenameWorker/
      ClaudePane refuse missing windows, relay goes through
      `ClaudePaneTarget` (`%N` pane id), grove-1/grove-10 scratch-server
      regression fixtures in `internal/tmux/window_id_test.go`
- [x] Orchestrator hotkeys (grove-105, 2026-07-18): `)` always opens the
      profile picker (default_profile dropped, lingering yaml key ignored);
      digits 1–8 spawn their bound profile directly, bound/unbound from the
      picker and persisted to `orchestrator.hotkeys` in the workspace (or
      global) config.yaml, comments preserved
- [x] ACCOUNT tab → per-profile key manager (grove-104, 2026-07-18):
      one selectable KEYS row per distinct `auth_token_env` across
      configured model_profiles (shared vars merge, profile names on the
      row) — masked value when the key resolves, an explicit "not set —
      enter to paste" state when it doesn't; enter (or p) opens the paste
      flow for the selected row's var; `openrouter.Key`/`SaveKey` are
      var-agnostic (same 0600 replace-in-place contract, other lines
      byte-for-byte); OpenRouter row keeps balance/runway/top-up extras,
      other rows are stars-only; zero profiles → grove-87's standalone
      OpenRouter view unchanged
- [x] Model profile per-profile env map (grove-103, 2026-07-18): `env:`
      map on `ModelProfile` for backend-specific vars beyond the six
      built-ins (Kimi Code's K3 endpoint needs
      `CLAUDE_CODE_AUTO_COMPACT_WINDOW`/`ENABLE_TOOL_SEARCH`/etc.) —
      exported sorted, before the built-in six, which win on collision so
      `env:` can't redirect `base_url`/`auth_token_env`; keys validated at
      config load (`^[A-Za-z_][A-Za-z0-9_]*$`) since they're interpolated
      unquoted into the wrap's shell line. No `env:` → byte-identical
      `WrapProfile` output.
- [x] Workspace marker narrowed (grove-100, 2026-07-17): a `.grove/` is a
      workspace marker only when it holds substance — `config.yaml`,
      `state/`, or `orchestrator/`; a `.grove/` with only the markdown
      backend's `tasks/` is NOT a workspace, so grove-78's fail-closed
      grab guard no longer traps `gv init`-scaffolded repos on the
      legacy global-config path. workspace.sh (red since the bare-dir
      marker landed, unmasked by grove-99) back to green unmodified;
      e2e/all.sh fully green.
- [x] Cockpit build restored on tmux 3.6a (grove-99, 2026-07-17):
      grove-78's blanket `=`-anchor broke every pane/window-target command
      (`set-option`/`show-options`/`select-layout`/`split-window` reject
      bare `=name` → `gv` died at SetCockpitLayout, CockpitReady always
      false); those helpers now anchor via `tmux.ExactActive` (`=name:`),
      Exact stays for true session-target commands; regression test vs
      scratch server + e2e/cockpit.sh back to green. Leftover: workspace.sh
      still red — legacy-path grab vs `.grove/tasks` workspace marker
      (issue #100)
- [x] Sweep offers: orphan kill + idle pause (grove-92, 2026-07-17):
      `gv sweep` gains two per-row-confirmed offer types — orphan process
      → plain-SIGTERM `kill <pid>` (a survivor is reported, never
      SIGKILLed) and idle worker → `gv pause`; offer-building extracted to
      pure `audit.SweepOffers`, which drops paused rows on the paused FACT
      (not class — Merged outranks Paused in Classify), so a paused task
      yields ZERO offers of any kind; `sweep --json` adds additive
      `orphan_processes`; e2e stubs `ps` via PATH so a piped `y` can never
      reach a real process
- [x] Audit idle class (grove-91, 2026-07-17): flag finished-but-burning
      workers — window alive + agent done (idle with STATUS done sentinel)
      or waiting + quiet past `audit.idle_after` (default 30m,
      zero/invalid tolerant) classify `idle`, suggestion `gv pause`; ranks
      below merged/drifted/paused/abandoned/disconnected, working agents
      never idle (stuck stays the cost flag's job); additive `--json`
      class + `facts.sentinel`
- [x] Audit orphan-process report (grove-89, 2026-07-17): `gv audit`
      flags claude/mcp descendants reparented to launchd (ppid==1,
      not in any live tracked pane's ancestry) — pure detection fn
      over injected ps/tmux text, additive `orphan_processes` in
      `--json`, human output prints a suggested `kill <pid>`;
      report-only, audit stays pure read
- [x] WindowExists glyph fix (grove-94, 2026-07-17): live window names
      carry grove-47 state glyphs (`… ⏸`), audit's exact-equality
      check classified every live worker `disconnected`; now matches
      stored name exactly or as `stored + " "` prefix; kill/target
      paths untouched
- [x] `gv pause` (grove-90, 2026-07-17): park one worker — kill its window
      (worktree/branch/transcript untouched), `task_paused` event +
      `Task.Paused` fold, audit class `paused` (never falls through to
      disconnected/abandoned; suggestion `gv adopt`), ⏸ in ls + cockpit,
      paused detail skips the pane scrape and hints `gv adopt`; mid-turn
      guard behind `--force`; dummy.sh pause→adopt loop asserts
      `--resume <sessionID>`
- [x] Cockpit first-frame panic fix (grove-79, 2026-07-13): viewActivity's
      `items[:avail]` clamped (grove-56 regression — empty feed + small
      leftover panicked the render); narrow/short render-sweep test;
      e2e/all.sh runs all six suites; cockpit.sh/workspace.sh re-greened
      and capture with `-S -300`
- [x] Surface-plugin contract v1 (grove-75, 2026-07-13): docs/plugins.md +
      copyable plugin-authoring skill; `schema_version`/`v` stamps;
      e2e/plugin.sh tripwire. First consumer: gv-remarkable (issue #76,
      separate repo)
- [x] grab fail-closed + exact tmux session targets (grove-78,
      2026-07-13): grab errors when the repo's workspace isn't the ambient
      one (no legacy-session escape) and rolls back worktree/local
      branch/prompt/window on failure (remote branch kept); every
      session-scoped `-t` in internal/tmux `=`-anchored via `tmux.Exact`
      (session `grove` vs `grove · <ticket>` window collision, live)

## Phase 0 — extraction proven (skeleton + local-md) ✅ 2026-07-04

Plan: [docs/plans/2026-07-04-phase-0.md](docs/plans/2026-07-04-phase-0.md)
(plan-reviewer approved). Divergences logged in docs/seed-manifest.md.

- [x] Seed: copy ovs tree byte-identical, module path rewrite, build/vet/
      test green (2026-07-03, see docs/seed-manifest.md)
- [x] **P0.0 namespace rename** (2026-07-04): config `~/.config/grove/`,
      state `~/.local/state/grove/` + `GROVE_STATE_DIR`, `gv hook` +
      basename-matched installer predicate (never claims ovs entries),
      `grove`/`grove-mobile` cockpit sessions, notifier group/titles
- [x] `markdown` TaskProvider (frontmatter schema; backlog = todo/backlog;
      event-state-authoritative in-flight exclusion; no-remote degraded
      grab/done paths per DESIGN §5.2)
- [x] `TaskProvider` interface extraction (P0 read subset of DESIGN §5.1);
      `linear` behind it; kickoff render byte-identical (golden-tested)
- [x] `gv init` P0 scaffold (register repo + `.grove/tasks/` + sample —
      probe/wizard stays Phase 1)
- [x] `gv grab/ls/done` E2E on a dummy repo (`e2e/dummy.sh`, remote-less,
      worker = `echo`) — also covers hooks, untrack, re-grab, audit
- [x] Dual-hook coexistence smoke test (scratch env over a copy of the
      real settings.json: ovs entries byte-identical, gv added once,
      `gv hook` no-ops on a live ovs worktree cwd). Live install = the operator's
      morning step.

## Phase 1 — bootstrap (drop-in-to-any-repo)

Plan: [docs/plans/2026-07-04-phase-1a-wizard.md](docs/plans/2026-07-04-phase-1a-wizard.md)
(plan-reviewer approved; re-scoped 1a to absorb most of 1b — the summary
board IS the manifest rendered).

- [x] 1a+ (2026-07-04): probe (stack/shape/context, `internal/probe`) +
      connections manifest (`internal/connections`, core kinds +
      grid-interim tagged for pack lift) + doctor = manifest renderer
      (✓/!/✗, fixes, --json, errors-only exit) + `gv init` wizard
      (detect-then-confirm huh forms, flag twins, `--yes` fills-empty-only,
      `--only <step>`, re-run = reconfigure, comment-preserving field-merge
      writer) + AGENTS.md bootstrap agent (templated one-shot, never
      overwrites, off under --yes) — e2e/wizard.sh covers it all
- [ ] 1b (remainder): pack loading (local path, slot merge)
- [ ] 1c: drift detection — TTL-cached lazy checks, failure-signal
      degradation via hook classifier, seeded-file hash drift +
      `gv sync --diff`; verb-boundary connection gating
- [x] Workspace registry + `gv switch` + ambient walk-up (2026-07-05,
      plan docs/plans/2026-07-05-workspaces.md, two review rounds):
      per-root `.grove/` config+state+orchestrator, yaml-merge over the
      global config, `grove-<label>` cockpits with the label in the TUI
      header (the visible-focus driver), read-only multi-fleet hook
      ownership, `gv switch`/`gv workspaces`, parent-scope init,
      e2e/workspace.sh. Legacy no-marker path preserved.
- [ ] Measure: is the Aider-style repo map needed at target repo sizes?
      (deferred decision, DESIGN.md OQ4)

## Phase 2 — routing (the smarter swarm) — deferred 2026-07-08

Unbuilt and moved to Parked. Routing/tiering likely isn't worth it for a solo
operator; the cost ledger these tasks would measure against already shipped in
grove-8, so this can be revisited when fleet size makes tier routing pay off.
See Parked / someday.

## Phase 3 — second provider (seam stress test)

- [x] `github-issues` adapter via `gh` (2026-07-05, pulled forward for
      unbrewed — plan docs/plans/2026-07-05-github-issues.md, two review
      rounds). OQ3 resolved → labels; ids `<repo>-<n>` fleet-unique;
      short refs (`gv done 7`) via numeric-suffix arbitration; list cap
      surfaced; e2e/github.sh with a stub gh. The seam held: zero
      changes to the linear/markdown providers.

## Phase 4 — relay + brain + cockpit (generic ovs parity)

- [ ] Hooks/inbox generalization (mail model lives in `state`)
- [ ] Generic orchestrator CLAUDE.md (de-Gridded duties text — does not
      exist yet, flagged by design review) + pack overlay rendering +
      seed-hash tracking
- [x] **Pulled forward 2026-07-04 (the operator's first-test feedback):** cockpit
      main-vertical (bare `gv` opens it; TUI-only = `gv dash`), MAIL/
      REVIEW panels → ACTIVITY feed, `gv orchestrator new` / `O` keybind,
      orchestrator default `claude --dangerously-skip-permissions`
      (e2e/cockpit.sh smoke-tests the layout)
- [x] **grove-8 (2026-07-07):** cockpit costs page (`$`/`c`, esc back) +
      persistent local spend ledger (`<state>/ledger.csv`, O_APPEND+flock;
      toggle in `<state>/cost-recording`, config `cost.record` seeds the
      default; `gv done` writes the final row so history survives
      transcript pruning) + ledger-only history section + hourly/daily/
      weekly spend bars (`internal/ledger`, `cost.Points/Buckets/Bar`,
      `gv cost --ledger|--record on|off`; e2e/dummy.sh proves durability)
- [ ] Cockpit remainder: workspace-labelled sessions (`grove-<label>`)
      + workspace-aware `orchestrator new` (§4.6 — needs Phase 1 ambient
      walk-up), expandable feed entries, lossless-clear drafts (§4.5)

## Phase 5 — learnings, first cut (lean)

- [ ] L0–L2 scopes + `gv learn` + `LEARNING:` sentinel harvest + curation
      inbox with human gate (docs/grove-learnings-design.md)
- [ ] Deferred until corpus size hurts: activation filtering, promotion
      automation, lint, counters (designed, not built)

## Phase 6 — OSS polish + the Grid pack + retirement

- [ ] goreleaser + tagged releases (decision: from day one — set up when
      first useful, no later than first share)
- [ ] Wizard hardening, config.example.yaml refresh, docs for strangers
- [ ] Architect/editor split (config flag, off by default)
- [ ] **Capability-surface audit of the live ccwork machine** → author
      the Grid pack in the workspace marketplace repo
- [ ] **Parity acceptance test** (docs/grove-connections-design.md §8) →
      ovs retirement + team onboarding

## Parked / someday

- Revisit-before-public: trust gate (accepted Critical, connections §9
  row 1) + worker-autonomy core safety guard (connections §6.4)
- Shared fleet visibility across teammates (explicitly out of v1)
- Router/tiers + escalate-on-failed-gate cascade (was Phase 2, deferred
  2026-07-08 — unbuilt; the cost ledger it would measure against shipped in
  grove-8, but there's no case for tier routing at solo scale yet)
- Learned router classifier (needs ledger history; likely never for solo)
- Public learnings commons (scope creep — parked)
