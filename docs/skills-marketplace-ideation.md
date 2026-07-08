# Skills/workflow marketplace — ideation

Status: **raw ideation, not a design doc yet**. Captured 2026-07-08 to be
elaborated later (Dean: "throw fable on it"). Not a ticket — no
implementation implied by this doc's existence. Depends on grove first
going public/open-source (see `docs/HANDOFF.md` / `TASKS.md` for that
timeline) — nothing here is actionable before that gate.

## Original prompt

> Currently, Grove is not a public repository. I'm still doing some work
> to get it ready before I make it public and open. One of the things I
> was thinking about, though, once I do make this open, is a way to,
> I guess, make a marketplace of skills or things specific for ways that
> people have made their Grove better, useful to specific tasks — like
> maybe someone has a project working on TUI designs and they have a
> workflow of how they review a TUI implementation before it's pushed to
> main, or things like that. Things that involve the entire Grove
> ecosystem and how it organizes the orchestrator and organizes the
> agents. There might be skills people make specifically for making all
> of this better for a specific process, and maybe it'd be better to have
> a way for people to export those things or publish them to a common
> site that I could make later. This would involve making a Grove SaaS
> product to complement this free CLI tool, or something like that.
> I don't know.

## 1. What's actually shareable

*(fill in: is the unit of sharing a `learnings/` layer entry, a whole
kickoff-prompt template, a review workflow like TUI-pre-push-review, a
`.grove/config.yaml` fragment, a full agent-role definition, a Workflow
script (see the harness's `Workflow` tool), or some combination? Does
grove's existing layered-learnings system already give a natural export
boundary, or does sharing need a new artifact type?)*

## 2. Publishing / distribution mechanism

*(fill in: pure git-based — point at a repo/gist, like Homebrew taps or
Oh My Zsh plugins — vs. a hosted registry with search/discovery, like
npm, VS Code Marketplace, Raycast Store, Obsidian community plugins,
GitHub Actions Marketplace. Versioning story, install command shape
(`gv skills add <name>`?), trust/review model for third-party content
running inside an autonomous agent loop — this is code/prompts that
influence what an agent does, so supply-chain risk is real, not
hypothetical.)*

## 3. Grove SaaS — relationship to the free CLI

*(fill in: what's the actual paid surface — hosting the registry, a
hosted cockpit/dashboard, team/org features, analytics across a fleet?
Does this look like GitLab's open-core model, Vercel/CLI-plus-cloud,
or something else? Is monetization even the goal here, or is "SaaS"
shorthand for "the thing that needs a server," i.e. just a registry
that happens to need hosting?)*

## 4. Prior art to look into

*(fill in: npm/VS Code Marketplace/Raycast Store/Obsidian community
plugins/GitHub Actions Marketplace/Homebrew taps/Oh My Zsh — what each
got right or wrong on discovery, trust, and monetization; also worth
looking at MCP server registries since that's the closest analog to
"third-party thing an agent loads and trusts")*

## Open questions

- Does this need to exist before or after grove has real external users,
  or is it premature to design a marketplace for a tool with ~1 user?
- Security: a "skill" here can steer an autonomous coding agent — what's
  the review/sandboxing story before letting someone `gv skills add`
  a stranger's workflow?
- Is the right first step much smaller than a marketplace — e.g. just a
  documented convention for exporting/importing a `.grove/` learnings
  layer via plain git, with the registry/SaaS idea deferred until that
  convention proves people actually want to share these?
