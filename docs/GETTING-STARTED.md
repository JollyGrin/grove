# Getting started with grove (`gv`)

Grove turns tasks into autonomous Claude Code sessions: **one task → its own
git worktree + tmux window + a kickoff prompt → a PR**. You drive a fleet of
these workers from a single **orchestrator chat**, and glance at a dashboard to
see what needs you.

This guide gets you from zero to running your first ticket with **Linear** as
the task backend.

---

## 1. Install

Grove is a Go CLI. Clone the repo and install the binary:

```sh
git clone https://github.com/JollyGrin/grove.git
cd grove
go install ./cmd/gv      # builds ~/go/bin/gv
```

Make sure `~/go/bin` is on your `PATH` (add `export PATH="$HOME/go/bin:$PATH"`
to your `~/.zshrc` if `gv help` doesn't resolve).

You'll also want:

- **tmux** (`brew install tmux`) — the cockpit and every worker run in tmux.
- **`gh`** (`brew install gh`, then `gh auth login`) — grove checks PR/CI state
  and merges through GitHub, never git ancestry.
- **`claude`** — the Claude Code CLI, logged in.

Sanity check:

```sh
gv doctor      # preflight: tmux, gh, claude, hooks, connections
```

---

## 2. Set grove up for your project (let your LLM do it)

Run this **inside your project's git repo**, then hand the wiring to Claude:

```sh
cd ~/git/your-project
gv init          # workspace-aware wizard: probes the repo, wires connections
```

`gv init` detects your stack, registers the repo, and shows a **connections
board** of what's wired and what's missing. If you'd rather have your LLM do the
whole thing, open Claude Code in the repo and tell it:

> Set up grove for this repo. Run `gv init`, walk through the wizard, and get
> `gv doctor` to green. Explain any connection it can't wire on its own.

Grove works out of a `.grove/` directory it creates in the repo (config, task
state, orchestrator brain). The repo becomes a grove **workspace**.

---

## 3. Pick a task backend (let your LLM do it)

By default grove uses local markdown files (`.grove/tasks/*.md`) as the task
backend — zero setup, fine for trying the loop. Two remote backends exist:

- **GitHub issues** (lightest remote option — grove develops itself on it):
  no API key beyond `gh auth login`. Set `provider.kind: github` and task ids
  become `<repo>-<n>` for issue #n. Agents transition issues via labels; your
  merge closes them.
- **Linear** — the rest of this section.

To switch to **Linear**, two things need to be true — get your API key, then
point grove's config at Linear. Ask your LLM:

> Switch grove's task backend to Linear. My Linear team key is `DEV`. Walk me
> through creating a personal API key, then update grove's config to use the
> linear provider, and confirm `gv doctor` sees the key.

What that involves, concretely:

1. **Create a Linear API key** at
   [linear.app/settings/api](https://linear.app/settings/api), then export it in
   your shell:

   ```sh
   # in ~/.zshrc
   export LINEAR_API_KEY="lin_api_..."
   ```

2. **Point the config at Linear** — in `~/.config/grove/config.yaml` (or the
   repo's `.grove/config.yaml`):

   ```yaml
   provider:
     kind: linear

   linear:
     api_key_env: LINEAR_API_KEY
     team: DEV                 # your Linear team key

   repos:
     your-project:
       path: ~/git/your-project
       base: main
       setup: pnpm install     # runs in each fresh worktree (optional)
       # If you have multiple repos, map Linear labels → repo:
       # linear_labels: [frontend, web]
   ```

3. **For backlog triage**, the orchestrator explores Linear through the
   **Linear MCP server**. Add it to Claude Code so the orchestrator can browse
   and score tickets:

   ```sh
   claude mcp add ...        # your Linear MCP server of choice
   ```

   Ask your LLM to help wire this up if you don't already have a Linear MCP
   configured.

Run `gv doctor` again — the Linear key row should now be green.

---

## 4. The UX — driving the fleet

### Open the cockpit

```sh
gv       # opens the cockpit: dashboard on the left, orchestrator chat on the right
```

The cockpit is a tmux session (`grove-<workspace>`): window 0 holds the
**dashboard** pane and one or more **orchestrator chats** side by side —
this is where you spend ~90% of your time. Each worker gets its own window
(1+) in the same session, named after its task with a live state glyph, so
**`ctrl-b w`** shows the whole fleet as a tree.

Standard tmux keys work: **`ctrl-b z`** zooms the focused pane fullscreen,
**`ctrl-b ↑/↓`** moves between panes. The default split is horizontal
(equal columns); **`L`** cycles horizontal → vertical → tiled.

### Open a new orchestrator chat

From the dashboard pane, press **`O`** (or **`0`** — same key, the glyph reads
as a zero in some fonts). This splits a fresh orchestrator chat into the right
column and focuses it, ready to type. **`)`** does the same on a configured
**model profile** (e.g. an OpenRouter backend) — it picks the repo's
`default_profile`, or offers a picker when several profiles exist. Use a
separate chat per parallel thread of work; for sequential unrelated topics,
just `/clear` in one chat instead — a fresh spawn re-pays the ~50k-token
session floor for nothing.

### Close a chat

Type **`/exit`** then **`exit`** (i.e. exit Claude, then exit the shell) in that
pane. The pane closes and the layout re-tiles. Closing a chat loses nothing —
the fleet's state lives in `.grove/`, not in chat history, so a fresh
orchestrator re-derives everything from `gv ls`.

### Dashboard keys (left pane)

Press **`?`** for the full help overlay. The main keys:

| Key | Action |
|-----|--------|
| `O` / `0` | new orchestrator chat |
| `)` | new orchestrator chat on a model profile |
| `j` / `k` | move selection |
| `a` | **attach** — jump into the selected worker's tmux window |
| `enter` | task detail / reply to a waiting question |
| `n` | nudge the selected worker (send a follow-up) |
| `v` | mark reviewing |
| `o` | open the worker's preview |
| `p` | open the worker's PR in the browser |
| `t` | open the ticket in the browser |
| `d` | mark done (verify merged → clean up) |
| `$` | costs page (spend ledger, per-task/model breakdown; `esc` back) |
| `L` | cycle pane layout (horizontal / vertical / tiled) |
| `*` | cycle joy effects (full / calm / off) |
| `X` | park the workspace (kill session; revive via `gv adopt`) |
| `?` | help overlay |
| `q` | quit |

---

## 5. What to tell the orchestrator

The orchestrator is your **chief of staff** — it triages, dispatches, monitors,
and summarizes. It **never writes code** (workers do that). It follows a
**propose-then-confirm** rule: it drafts an action and waits for your "yes"
before grabbing, answering, or nudging anything.

Things to say:

- **"Anything need me?"** → it runs `gv ls` and leads with questions, blockers,
  and review-ready PRs — one line each, with a drafted answer for every open
  question.
- **"Find me 3 easy tickets."** → it explores your Linear backlog, scores each
  for agent-suitability (clear acceptance criteria, small surface, right repo,
  not blocked), and returns a ranked table with a `gv grab` command per row.
- **"Grab DEV-123."** → after you confirm, it dispatches the ticket.
- **"What's DEV-45 stuck on?"** → it reads the worker's question, investigates,
  and proposes an unblock message.
- **"This ticket is too vague — sharpen it."** → it tells you exactly what's
  missing and drafts the fix.

---

## 6. How to pick up tickets

Dispatching a ticket is one command — the orchestrator runs it after you
confirm, or you can run it yourself from any shell in the repo:

```sh
gv grab DEV-123 --repo your-project     # ticket → worktree → autonomous worker → PR
```

- **No arg** (`gv grab`) lists the grabbable backlog.
- **`--repo`** picks which repo the worktree is cut from (always pass it;
  label inference is unreliable).
- **`--model <id>`** pins that one worker to a specific model (e.g. a cheap
  task on Sonnet, a hard one on Opus) without editing config.
- **`--manual`** sets the worktree up for you to drive by hand instead of
  autonomously.

What happens: grove cuts a fresh worktree, opens a tmux window, and launches a
worker with a **kickoff prompt** built from the ticket. The worker investigates,
implements, commits with the ticket prefix, pushes, opens a PR that says
`Closes DEV-123`, runs review, and ends with a `STATUS:` line
(`DONE` / `QUESTION` / `BLOCKED`) that shows up on your dashboard.

You watch from the dashboard; when the PR is merged, press `d` (or tell the
orchestrator `done DEV-123`) to clean up the worktree, window, and branch.

> Grove never moves a ticket to Done and never mutates a tracker's terminal
> state — agents transition tickets to "In Review"; **you** finish them.

---

## 7. Using your existing repo skills

Workers run `claude` **inside a git worktree of your repo**, so anything in your
repo's `.claude/` — **skills, agents, and `CLAUDE.md`** — is automatically
available to every worker. You don't wire this up; it comes for free with the
worktree.

The default kickoff prompt already leans on this. For example, step 6 tells the
worker to *"wrap up with the wrapping-up-task skill: run the pr-reviewer agent,
address findings, verify CI is green."* If your repo defines a `wrapping-up-task`
skill and a `pr-reviewer` agent, workers use them as-is.

To hook your own skills into the flow:

- **Reference them in your repo's `CLAUDE.md`** — every worker reads it, so
  "always run the `lint-and-typecheck` skill before opening a PR" gets honored
  fleet-wide.
- **Override the kickoff template** per repo if you want workers to invoke a
  specific skill by name. In config:

  ```yaml
  repos:
    your-project:
      path: ~/git/your-project
      prompt: ~/git/your-project/.grove/kickoff.tmpl   # your custom kickoff
  ```

  The template gets the ticket's `Identifier`, `Title`, `Description`,
  `Comments`, and `URL` — write your steps around them and name the skills you
  want run.

The orchestrator can also use repo/plugin skills for fleet management (e.g. a
`cleanup-local-state` skill for orphaned worktrees) — just tell it which skill
to run when.

---

## Quick reference

```sh
gv init                        # wire grove into the current repo (workspace)
gv doctor                      # preflight checks
gv                             # open the cockpit (dashboard + orchestrator)
gv grab DEV-123 --repo name    # dispatch a ticket → worker → PR
gv ls                          # fleet table (add --json for the orchestrator)
gv attach DEV-123              # jump into a worker's tmux window
gv answer DEV-123 "text"       # reply to a waiting worker
gv diff DEV-123                # review the branch diff without attaching
gv done DEV-123                # verify merged → clean up
gv help                        # full command list
```

In the cockpit: **`O`/`0`** new chat · **`a`** attach · **`/exit` then `exit`**
close a chat · **`ctrl-b z`** zoom · **`q`** quit dashboard.
