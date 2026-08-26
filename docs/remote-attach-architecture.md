# Remote architecture: attach beats sync (t3code study)

Status: reference notes, 2026-08-26. Source study of
[pingdotgg/t3code](https://github.com/pingdotgg/t3code) (T3 Code — Theo
Browne / T3 Tools' open-source "agent harness control surface": Electron
desktop + hosted web + native mobile clients over a Node/Effect server
that wraps Claude Code and other agent CLIs). Its remote story is widely
regarded as first-class, so we read the source to see what grove should
steal. Their internals docs are excellent and worth reading directly:
`docs/internals/remote.md`, `connection-runtime.md`, `overview.md`,
`environment-auth.md` in their repo.

Companion docs here: `docs/remote-host-setup.md` (VPS runbook, #179) is
*how to stand up a host*; this doc is *why the remote train is shaped the
way it is* and where it goes next.

## The headline: t3code has NO cross-machine sync

Despite the "syncs all your machines" reputation, t3code deliberately
synchronizes **nothing** between machines. No CRDT, no cloud session DB,
no event-log replication. The model:

> One machine runs a server that owns everything — agent processes, PTY
> terminals, git, an event-sourced SQLite store. Every other device
> (phone, laptop, web) is a thin client attaching over an authenticated
> WebSocket. Sessions never move; clients come to them. Host off =
> sessions unreachable (clients keep read-only cached snapshots).

This **validates grove's remote-train bet** (#176–#179: nothing syncs,
each host owns its own state, `--host` is ssh passthrough, fleet view is
an on-demand merge). The industry-leading "remote" implementation made
the same architectural choice. The gap between us is not architecture —
it's the polish of the attach surface.

One thing grove has that t3code punts on entirely: **moving** a task
between machines. `gv handoff` (checkpoint → PR-body handoff → adopt on
the other host, with git as the transfer medium) covers a real case they
have no answer for. Keep it.

## Their mechanism, briefly (with source pointers)

- **Event-sourced core**: `orchestration_events` append-only log +
  derived `projection_*` tables (threads/messages/sessions), written in
  **one SQLite transaction** per command — the read model can never
  durably disagree with the log. (`apps/server/src/orchestration/…`,
  `persistence/Migrations/`.) This is our events.jsonl / tasks.json
  split, hardened.
- **Command receipts**: every mutating command carries a
  **client-minted `commandId`**; the server records receipts and dedupes
  retries. All remote mutation is idempotent by construction.
- **Attach protocol**: `terminal.attach` replays a persisted scrollback
  snapshot, then streams live events (`apps/server/src/terminal/
  Manager.ts`). tmux capture-pane + attach, promoted to a protocol — how
  a phone that can't run tmux gets full terminal state.
- **Access vs launch are orthogonal** (their cleanest framing):
  *access* = how a client reaches a server (LAN / Tailscale endpoint
  hints / managed tunnel — all resolving to the same bearer-auth
  WebSocket); *launch* = how a server comes to exist (already running,
  or an ssh **launch helper** that probes, starts-or-reuses, and marks
  servers it didn't start `external` so it never kills them on
  disconnect). SSH is a launch helper, not a special environment type.
- **Identity vs endpoints**: each host persists a random stable
  `environmentId`; LAN/tailnet addresses are advertised *hints*. The
  connection attempt decides reachability, not the advertisement — raw
  URLs rot when networks change.
- **Cached views lose to live data**: clients cache projections for
  offline reading with explicit per-domain status
  (`empty/cached/synchronizing/live`); a cached snapshot is never
  allowed to overwrite newer live data on fast reconnect.
- **Offline outbox**: mobile composes whole new threads offline —
  thread/message/command IDs minted client-side, queued, drained on
  reconnect, deduped by receipts.
- **Auth ladder**: one-time pairing token (QR/URL, TTL) → OAuth token
  exchange (RFC 8693) → scoped 30-day bearer session → **5-minute
  single-purpose WebSocket ticket** (long-lived tokens never appear in
  socket URLs), with per-RPC-method scopes.
- **Relay off the hot path**: their hosted relay only brokers
  credentials and provisions a Cloudflare tunnel hostname — application
  traffic never transits their infra. Same spirit as "use Tailscale,
  keep grove dumb."

## What grove adopts, in order

1. **Idempotency for remote-relayed mutations** (cheap, protects the
   #176–#179 train). A retried `gv answer`/`nudge`/`grab --host` over a
   flaky link can double-send today. Steal the receipts idea: caller
   mints an op ID, the host checks events.jsonl before appending.
2. **Access/launch vocabulary in `hosts:` config.** Keep "how do I
   reach it" (ssh target) separable from "how does gv get there"
   (installed path today; a launch/bootstrap helper later). If a helper
   ever starts remote servers, copy the `external` marker rule: never
   kill what you didn't start.
3. **Cached-is-not-current in the plugin contract.** Any surface
   caching tasks.json or `--remote` merge output should carry an
   explicit staleness marker and never present cached rows as live.
   (The #178 fleet merge already gets this right: tombstones replaced
   by live rows, stale tombstones flagged, host failure → warning +
   local-only.)
4. **`gv serve` blueprint (when mobile cockpit v2, issue #5, unparks).**
   Copy, don't invent: pairing token → scoped session → short-lived
   socket ticket; snapshot-then-stream attach for panes; client-minted
   IDs + receipts for the outbox; Tailscale as the transport, any relay
   limited to credential brokering.
5. **North star if state ever moves to SQLite**: append event + update
   projection in one transaction. Not urgent — `state.Load` refolds and
   events.jsonl stays the source of truth — but it removes the
   log/view divergence window by construction.

## What we deliberately keep different

- **Handoff stays.** t3code's model dies when the host machine is off;
  grove's git-mediated handoff (PR body as the payload) moves work to
  whichever machine is alive. It's the escape hatch their design lacks.
- **tmux stays the local attach surface.** Their PTY-manager +
  WebSocket machinery exists because they have no tmux; we do. The
  snapshot+stream protocol only matters for clients that can't ssh
  (phone/web) — i.e., `gv serve` territory, not before.
- **No daemon.** Their server is always-on per machine; grove remains a
  CLI + hooks. The fleet merge stays on-demand (one fetch per `R`
  press, no polling) per the cockpit RAM rule.
