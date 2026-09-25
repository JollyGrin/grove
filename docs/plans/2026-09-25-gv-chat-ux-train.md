# gv chat UX train — integration-branch plan (2026-09-25)

**Driver:** the grove-repo orchestrator. **Judge:** Dean.
**Branch:** `chat-ux` (cut from `main`); every PR in this train targets it.
`main` gets one merge when Dean is confident in the whole.

## Why

A real field test (Dean, travelling in China, phone only) worked but kept
blocking on four things:

1. **Questions could not be answered.** AskUserQuestion menus showed no
   buttons, or buttons that only moved a highlight.
2. **Titles were noise.** Rows read `<local-command-caveat>Caveat: The
   messages below…` or carried `<pasted_content id=…>` wrappers.
3. **Order was unintuitive.** You couldn't tell which chat needed you, and
   the one you just used wasn't on top.
4. **Lifecycle was opaque.** "Archived" meant nothing to the operator, and
   there was no way to close a chat so it stopped using a Claude process.

## Root causes (verified 2026-09-25 against code + `gv chat ls`)

| Pain | Cause |
|---|---|
| Questions | Claude Code v2.1.282 draws modals **unboxed**; `DetectPicker` required `│`, so it missed every menu. Fixed in PR #314 (AskUserQuestion phone fix). The same `│` assumption makes `internal/tmux`'s verified-submit (`inputBoxContent`/`pasteLanded`) a **no-op**, so every relay counts as landed. That covers phone send, `gv answer` and `gv nudge`, fleet-wide. |
| Titles | `Row.Label` = the transcript's first user message; `transcript.isBoilerplate` only skips `[request interrupted` / `resume`. About half of the local rows are titled by the `/model` caveat wrapper. |
| Order | `chat.Less` sorts live chats by chat **number** before recency; screen 1 lists workspaces alphabetically; nothing ranks "needs you". |
| Lifecycle | `kind: archived` is jargon for "no running process, transcript kept"; there is no close verb except self-close (`gv orchestrator close`); archived rows (40+) sit beside live ones. |

## Decisions (Dean, 2026-09-25)

- **History hidden by default.** Rename "archived" to **history** in the UI.
  The home screen shows only live chats. Past chats sit behind a
  `history (N)` disclosure. The contract's `kind: "archived"` is unchanged.
- **Closing is a first-class, explained action.** "End chat" stops the
  Claude process (frees RAM/tokens) and keeps the transcript, so it moves to
  history and can be revived. The UI says exactly that at the moment of
  choosing.
- **#293 (pick model for chat), direction (a):** a built-in `opus / sonnet /
  haiku` tier list, optionally overridden by config, in **one sheet** with the
  profiles. The real complaint: **every row must say which model it will
  actually run.** That includes "Claude (host default)", which today names
  no model.
- **Notifications:** page-side only (app open or backgrounded); no server
  push this train.

## Train mechanics

- Kickoff templates hardcode "PR against main", so every grab carries a
  `--brief` override: *base is `chat-ux` — `git fetch && git rebase
  origin/chat-ux` before starting; open the PR with `--base chat-ux`; never
  target main.*
- PRs squash-merge into `chat-ux` after orchestrator review (diff read,
  green gate). `Closes #N` only fires on the default branch, so issues
  stay open until the final merge. That is intended.
- `TASKS.md` is the only file every PR touches. Its conflicts are row
  additions, and the driver resolves them by union.
- Parallelism: at most two workers touching `ui/app.js` at once. The
  server/CLI-only tickets run alongside.
- **Exception:** the verified-submit fix (T1) is fleet-wide and independent
  of the phone UI. It targets **main** directly, and `chat-ux` then merges
  main.
- The driver never edits Go/JS. Fixes go back to the worker (`gv nudge`)
  or out as a follow-up ticket in the train.

## Waves

### Wave 0: land what's in flight (driver)
- Retarget PRs #309–#314 to `chat-ux`. Merge #314 (AskUserQuestion phone
  fix) first, then #309 (Enter = newline), #310 (stop button), #311 (error
  toast), #312 (jump-to-latest), #313 (PWA manifest).
- Close #287 as a duplicate of #286.

### Wave 1: never blocked
- **T1 (new):** verified-submit understands unboxed chrome, with fixtures
  from PR #314's captures. Targets main.
- **#300:** honest "working…" indicator (investigation first).
- **T2 (new):** optimistic "sending…" bubble, plus per-chat drafts in
  `localStorage` keyed by address. The draft follows its chat, so the
  wrong-agent guard (grove-116/78) still holds.
- **#305:** notifications + badge on turn end / question (needs #313).

### Wave 2: find the right chat, and stop it when done
- **T3 (new):** clean titles. Strip harness wrappers
  (`<local-command-caveat>`, `<command-name|message|args>`,
  `<local-command-stdout>`, `<pasted_content …>`, `<system-reminder>`) when
  deriving a chat label, and skip a message that is only wrappers. Do it in
  `internal/chat`, **not** `internal/transcript`, which stays byte-comparable
  with ovs. In the transcript view, render those user entries as a
  collapsed chip (`/model`, `pasted text`), not raw tags.
- **#294 (amended):** `gv chat close <session>` + an "End chat" action on
  the phone (chat header + live row), with a confirm sheet that explains:
  *stops the Claude process, keeps the conversation in history, revivable.*
- **#302 (amended):** live-chats home. All live chats across workspaces,
  ordered **needs you (picker) → working → most recently active**. Change
  `chat.Less` so recency beats chat number within a kind. `history (N)`
  disclosure per workspace. Idle-for-hours live chats get a gentle "idle
  6h · end?" hint.
- **#297:** cache re-opened chats (`?since=`).

### Wave 3: polish
- #303 timestamps · #306 sans prose · #307 SSE lists · #286 build version
  · **#293** model picker per the decision above.

## Test gate before `chat-ux` → main

1. `go build ./... && go vet ./... && go test ./...`, `gofmt -l .` empty,
   `e2e/all.sh` green on the tip of `chat-ux`.
2. Throwaway build `go build -o /tmp/gv-chat-ux ./cmd/gv`, served with
   `gv chat serve` on a spare port for Dean's Android phone. The live
   `gv-chat.service` stays untouched.
3. Phone script (Dean): answer a single-select and a multi-select
   AskUserQuestion; send a multi-line message; stop a turn; end a chat and
   revive it from history; confirm titles and order on the home screen;
   get a notification with the app backgrounded.
4. Then merge to main, `gv update --yes` on both hosts, and restart
   `gv-chat.service` on groveremote.

## Numbers
- #286 — show build version
- #287 — duplicate of #286
- #293 — pick model for chat
- #294 — close a live chat
- #297 — cache reopened chats
- #300 — honest working indicator
- #302 — live-chats home screen
- #303 — timestamps in chat
- #305 — Android notifications and badge
- #306 — sans font for prose
- #307 — SSE list updates
- PR #309 — Enter = newline on phone
- PR #310 — stop button
- PR #311 — error toast
- PR #312 — jump-to-latest pill
- PR #313 — PWA manifest
- PR #314 — AskUserQuestion phone fix
