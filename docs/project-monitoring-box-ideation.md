# Custom per-project monitoring / status box — ideation

Status: **raw ideation, not a design doc yet**. Captured 2026-07-08 from a
spoken stream-of-consciousness, to be elaborated later with a bigger model.
Not a ticket — no implementation implied by this doc's existence.

## Original prompt (reformatted from a spoken stream-of-consciousness)

> Each of my projects sometimes has bespoke APIs or stats that would be
> very useful to have at a glance — not necessarily tied to the direct
> workflow of making tickets, but each project has its own unique needs.
>
> For example, in **Unbrewed** it would be great to have a status box I
> could customize per project — a lobby of all the players in it, stats,
> how many people are playing right now — basically polling an endpoint I
> already have on the monitoring station. We can discuss thresholds, how
> often it polls, etc.
>
> On **the grid** it would be nice to poll the two GraphQL endpoints to
> see whether they're up, just at a glance — "oh, staging GraphQL is
> down" is nice to know.
>
> So it's *not* about implementing any one of these specifically. It's
> about making a **container / box** that, when prompted from inside a
> Grove workspace (e.g. Grove managing my Unbrewed project), I could ask
> to add monitoring into the status box. We'd have a location where custom
> integrations live, and a **skill** that tells Grove exactly how to add
> that monitoring and configure it. It should be **hidden unless used**,
> and monitoring can be **turned off** to save resources when I don't want
> it running.

## 1. Core shape — a generic, customizable status box

- The deliverable is a **container/slot**, not any specific integration.
  Unbrewed's player-lobby and the grid's GraphQL-uptime are just the first
  two consumers of the same mechanism.
- It's **per-project (per-workspace)**: config and content live with the
  workspace (`.grove/`), so each project defines its own box contents.
- It's **at-a-glance / ambient**: distinct from the ticket/fleet workflow.
  This is "monitoring information I want visible while I work," not
  something that drives dispatch decisions.
- **Hidden unless used**: no box, no cost, no visual footprint until a
  project opts in. Off by default.

*(fill in: where does the box physically render — a panel in the cockpit
TUI, a new tab, a region of an existing view? How much space does it get,
and does it compete with the fleet view or sit alongside it?)*

## 2. Authoring flow — "ask Grove to add monitoring"

- The intended UX is conversational: from inside a workspace session, Dean
  says something like "add the Unbrewed lobby to the status box," and a
  **skill** walks through wiring it up — picking the endpoint, poll
  interval, thresholds, and how to render the result.
- Implies two artifacts:
  1. A **skill** (repo-agnostic? or shipped with grove) encoding *how* to
     add + configure a monitor.
  2. A **config location + schema** the skill writes into (per workspace).

*(fill in: does the skill generate a static config entry that a built-in
poller consumes, or does it generate actual code/a script per integration?
The former is safer and more uniform; the latter is more flexible but
means arbitrary per-project code running in the cockpit's process — see
the RAM/guardrail note below.)*

## 3. What a "monitor" is — the config unit

Candidate fields for a single monitor entry (to pressure-test later):

- **source**: an HTTP(S) endpoint to poll (REST GET, GraphQL query,
  health check).
- **poll interval**: how often (per-monitor; must respect the cockpit's
  resource budget).
- **extraction**: how to turn the response into the displayed value
  (a JSON path, a count, a GraphQL field, up/down from status code).
- **render**: label + how to show it (number, up/down glyph, small list
  like a player lobby, threshold-colored value).
- **thresholds**: when to color/alert (e.g. players < N is quiet, GraphQL
  non-200 is red).
- **enabled**: on/off toggle per monitor (and a master off switch).

*(fill in: two rough archetypes to support — (a) a scalar/health check
("staging GraphQL: up/down", "players online: 42") and (b) a small list
("lobby: these players"). Do both fit one schema, or is the list case a
separate widget type?)*

## 4. Resource discipline — polling in the cockpit

This is the sharpest constraint. The cockpit TUI has a standing rule that
it **must not grow its memory/CPU footprint** — RAM is reserved for
workers, and that rule forbids new goroutines/polls/caches, per-frame
allocation, etc. (see the `cockpit-ram-reserved-for-workers` memory). A
feature whose entire premise is "poll external endpoints on a timer from
the cockpit" runs straight into that rule.

*(fill in: how to reconcile. Options to weigh — a single shared poller
goroutine for all monitors rather than one per monitor; polling out of
process (a sidecar/hook) that writes results to a file the TUI just reads;
generous default intervals; the whole box being off unless explicitly
enabled so the default cost is genuinely zero. This tension is the thing
the design pass has to resolve before anything else.)*

## 5. Second-order use — the orchestrator reads the box too

Once these monitors exist in a project, they're not just for Dean's
at-a-glance view — the **orchestrator can consume the same signals as
diagnostic context** for its own reasoning and advice. Two concrete
examples:

- **Unbrewed**: be cautious about a big push/deploy when lots of people
  are currently playing. (There should be a graceful reset regardless, but
  the orchestrator knowing "42 in-game right now" changes the advice it
  gives about timing a merge.)
- **The grid**: if a worker is failing to preview an environment and the
  box shows **staging GraphQL is down**, that's very likely *why* — the
  orchestrator can connect those two facts instead of sending the worker
  chasing a phantom code bug.

So the monitor data wants to be readable by the orchestrator (not just
rendered in the TUI) — the same out-of-process results file that feeds the
box could feed the orchestrator's context.

*(fill in: is this pull, i.e. the orchestrator reads the results file when
relevant, or push, i.e. a red monitor proactively surfaces "staging is
down, this may explain DEV-X's failure"? Push is more useful but noisier.)*

## 6. Prior art / analogs to look at

*(fill in: tmux status-line plugins (tmux-plugins), Waybar/Polybar custom
modules, i3blocks/i3status, Übersicht widgets, k9s plugins, lazygit custom
commands, VS Code status-bar contributions — each is "user-defined widget
that runs a command/endpoint on an interval and shows a small result,"
which is exactly this. Steal their config shape and their resource
defaults; note how they sandbox/limit third-party widgets.)*

## Open questions

- **Rendering surface**: where does the box live in the cockpit, and how
  does it coexist with the fleet view without stealing focus or space?
- **Config vs. code**: is a monitor a declarative config entry consumed by
  a built-in poller (safer, uniform, sandboxable) or a per-project
  script/command (flexible, but arbitrary code near the cockpit)? This
  choice drives everything else, including the RAM story.
- **Where does the polling actually run** — in the TUI process (fights the
  RAM rule) or out of process with the TUI as a passive reader? Leaning
  toward the latter given the standing constraint.
- **Repo-agnostic skill vs. grove-specific feature**: is "add a monitor"
  a portable Claude Code skill, or a first-class `gv` capability with a
  config schema grove owns?
- **Secrets/auth**: some endpoints need tokens (the grid's GraphQLs,
  private monitoring stations). Where do credentials live so they're not
  committed into a workspace's `.grove/` config?
- **Failure behavior**: when a polled endpoint is itself down or slow,
  the box must degrade quietly (show "unknown"/stale) and never block or
  slow the cockpit render.
- **Scope of v1**: is the first cut just a single scalar health check
  ("up/down" for one URL) to prove the container + skill + off-by-default
  mechanics, with lists/lobbies/thresholds deferred?
