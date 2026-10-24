package orchestrator

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/remote"
)

// seedCoveredRemoteVerbs is the golden list of verbs orchestrator/CLAUDE.md
// is known to teach. When internal/remote.Supported changes, this test
// forces a human to confirm the seed was taught the new verb before the
// golden list is updated to match.
var seedCoveredRemoteVerbs = map[string]bool{
	"grab": true, "ls": true, "adopt": true, "handoff": true,
	"answer": true, "nudge": true, "diff": true, "pause": true,
	"untrack": true, "orchestrator": true, "chat": true,
}

// TestSeedDocumentsRemoteVerbs fails the moment internal/remote.Supported
// drifts from the verbs orchestrator/CLAUDE.md is known to teach.
func TestSeedDocumentsRemoteVerbs(t *testing.T) {
	for verb := range remote.Supported {
		if !seedCoveredRemoteVerbs[verb] {
			t.Errorf("remote verb set changed (%v): a verb that takes --host must be taught in "+
				"orchestrator/CLAUDE.md (duty 3 Dispatch + the tools block) before it ships. "+
				"Update the seed, then update seedCoveredRemoteVerbs here.", verb)
		}
	}
	for verb := range seedCoveredRemoteVerbs {
		if !remote.Supported[verb] {
			t.Errorf("remote verb set changed (%v): a verb that takes --host must be taught in "+
				"orchestrator/CLAUDE.md (duty 3 Dispatch + the tools block) before it ships. "+
				"Update the seed, then update seedCoveredRemoteVerbs here.", verb)
		}
	}
}

// TestSeedTeachesHostFlag guards the --host teaching added for grove-234
// against silent deletion.
func TestSeedTeachesHostFlag(t *testing.T) {
	if n := strings.Count(ClaudeMd, "--host"); n < 4 {
		t.Errorf("orchestrator/CLAUDE.md mentions --host only %d times (want >= 4): "+
			"the seed's --host teaching (duty 3 Dispatch + the tools block) has been trimmed — "+
			"restore it in orchestrator/CLAUDE.md", n)
	}
	if !strings.Contains(ClaudeMd, "gv grab <task> --host") {
		t.Error("orchestrator/CLAUDE.md is missing the `gv grab <task> --host` example — " +
			"restore it in orchestrator/CLAUDE.md's tools block")
	}
	if !strings.Contains(ClaudeMd, "handoff MOVES a task that is already running") {
		t.Error("orchestrator/CLAUDE.md is missing the `handoff MOVES a task that is already " +
			"running` warning that steers workers away from misusing gv handoff for remote " +
			"dispatch — restore it in orchestrator/CLAUDE.md's duty 3 Dispatch section")
	}
	if !strings.Contains(ClaudeMd, "BEFORE the ticket") {
		t.Error("orchestrator/CLAUDE.md is missing the relay-verb `BEFORE the ticket` " +
			"position rule (answer/nudge take --host only before the ticket; after it, " +
			"everything is payload) — restore it in orchestrator/CLAUDE.md's tools block " +
			"--host entry (grove-242)")
	}
}

// TestSeedTeachesSupervise guards the grove-253 `gv supervise` teaching
// against silent deletion: an orchestrator on a headless host (no desk
// cockpit) must know this verb exists instead of reinventing a monitor
// script.
func TestSeedTeachesSupervise(t *testing.T) {
	if !strings.Contains(ClaudeMd, "gv supervise") {
		t.Error("orchestrator/CLAUDE.md is missing `gv supervise` — restore it in the tools block")
	}
	if !strings.Contains(ClaudeMd, "pr_ready") {
		t.Error("orchestrator/CLAUDE.md is missing `pr_ready` — restore the Monitoring section's " +
			"delivery/liveness event-type list")
	}
}

// TestSeedTeachesCostContext (grove-289) guards duty 8's context-rot
// rescue paragraph: it must point the orchestrator at `gv cost --context`
// / the two `--analyze` flags, not the retired hand-derived
// `cache_read_tokens ÷ turns` signal.
func TestSeedTeachesCostContext(t *testing.T) {
	if !strings.Contains(ClaudeMd, "gv cost --context") {
		t.Error("orchestrator/CLAUDE.md is missing `gv cost --context` — restore it in duty 8's " +
			"context-rot rescue section")
	}
	if !strings.Contains(ClaudeMd, "context: avg ≥ 200k") || !strings.Contains(ClaudeMd, "never compacted") {
		t.Error("orchestrator/CLAUDE.md is missing the two `gv cost --analyze` context flags — " +
			"restore them in duty 8's context-rot rescue section")
	}
	if strings.Contains(ClaudeMd, "cache_read_tokens ÷ turns") {
		t.Error("orchestrator/CLAUDE.md still teaches the retired `cache_read_tokens ÷ turns` " +
			"signal — grove-289 replaced it with `gv cost --context` / the --analyze flags")
	}
}

// TestSeedTeachesLaneBilling guards the #234 lane-billing paragraph
// (zai-plan vs openrouter-) against silent deletion.
func TestSeedTeachesLaneBilling(t *testing.T) {
	if n := strings.Count(ClaudeMd, "zai-plan"); n < 2 {
		t.Errorf("orchestrator/CLAUDE.md mentions zai-plan only %d times (want >= 2): "+
			"the seed's lane-billing paragraph has been trimmed — restore it in orchestrator/CLAUDE.md", n)
	}
	if n := strings.Count(ClaudeMd, "openrouter-"); n < 2 {
		t.Errorf("orchestrator/CLAUDE.md mentions openrouter- only %d times (want >= 2): "+
			"the seed's lane-billing paragraph has been trimmed — restore it in orchestrator/CLAUDE.md", n)
	}
}

// TestSeedLabelsTicketNumbers guards the number-labelling rule: the
// operator cannot keep bare #N references straight, so every first
// mention carries a short parenthetical and multi-number messages end
// with a `Numbers` addendum. Added grove-wide 2026-09-05 after the rule
// lived only in one workspace's memory.
func TestSeedLabelsTicketNumbers(t *testing.T) {
	for _, want := range []string{
		"Label every ticket and PR number",
		"`Numbers` addendum",
	} {
		if !strings.Contains(ClaudeMd, want) {
			t.Errorf("orchestrator/CLAUDE.md is missing %q — the ticket-number labelling "+
				"guardrail has been trimmed; restore it in the Guardrails section", want)
		}
	}
}

// seedSection returns the body of a `## <name>` section of the seed —
// everything up to the next `## `. Asserting INSIDE the section is what
// makes the mandate tripwire real: `gv answer` appears all over
// CLAUDE.md, so a substring search over the whole file would stay green
// after the section was deleted.
func seedSection(t *testing.T, heading string) string {
	t.Helper()
	i := strings.Index(ClaudeMd, "\n## "+heading)
	if i < 0 {
		t.Fatalf("orchestrator/CLAUDE.md has no `## %s` section — restore it", heading)
	}
	body := ClaudeMd[i+1:]
	if j := strings.Index(body[3:], "\n## "); j >= 0 {
		body = body[:j+3]
	}
	return body
}

// TestSeedTeachesSupervisionMandate guards the grove-273 mandate section:
// the standing, scoped pre-authorization that lets an orchestrator woken
// at 03:00 steer a worker instead of stalling until morning. Every
// assertion is scoped to the section itself, and the in/out split is the
// whole point — an orchestrator that reads the section but loses the
// out-of-scope list would `gv done` a task overnight.
func TestSeedTeachesSupervisionMandate(t *testing.T) {
	sec := seedSection(t, "Supervision mandate")

	// How it watches: the workspace-wide stream, as a Monitor.
	for _, want := range []string{"gv watch --json", "Monitor", "run_in_background"} {
		if !strings.Contains(sec, want) {
			t.Errorf("`## Supervision mandate` no longer mentions %q — the section must say to "+
				"watch the scope with one `gv watch --json` Monitor, and why `run_in_background` "+
				"is the wrong tool for a stream that never exits", want)
		}
	}

	// In scope: steering only. Out of scope: anything that ends a task or
	// reaches outside the fleet — each of those becomes an ntfy push.
	in, out, ok := strings.Cut(sec, "**Out of scope")
	if !ok {
		t.Fatal("`## Supervision mandate` has no `**Out of scope` block — the mandate is only " +
			"safe because the set it authorizes is closed; restore the out-of-scope list")
	}
	for _, verb := range []string{"gv answer", "gv nudge"} {
		if !strings.Contains(in, verb) {
			t.Errorf("`## Supervision mandate` no longer lists %q as in-scope — restore it", verb)
		}
	}
	for _, verb := range []string{"untrack", "done", "adopt"} {
		if !strings.Contains(out, "`"+verb+"`") {
			t.Errorf("`## Supervision mandate` no longer lists `%s` as OUT of scope — a mandated "+
				"orchestrator would end a task unattended; restore it", verb)
		}
	}
	if !strings.Contains(out, "ntfy") {
		t.Error("`## Supervision mandate`'s out-of-scope block no longer mentions ntfy — an " +
			"out-of-scope event must become a push to the operator, not a silent stall; " +
			"restore the curl form")
	}

	// Length is cost: this file is resident in every orchestrator turn.
	if n := strings.Count(sec, "\n"); n > 95 {
		t.Errorf("`## Supervision mandate` is %d lines (want <= 95): it is resident in every "+
			"orchestrator turn, so growth is paid on every request — tighten it", n)
	}
}

// TestSeedTeachesChatHide guards the grove-405 chat-hide teaching: the two
// verbs in the tools block, the one chat move an orchestrator makes without a
// second confirmation (hiding ITSELF when the operator asks), and the opt-in
// invariant — the seed must never tell a chat to hide by default.
func TestSeedTeachesChatHide(t *testing.T) {
	for _, want := range []string{"gv chat hide [<s>]", "gv chat show <s>"} {
		if !strings.Contains(ClaudeMd, want) {
			t.Errorf("orchestrator/CLAUDE.md is missing `%s` — restore it in the tools block", want)
		}
	}
	if !strings.Contains(ClaudeMd, "Never hide yourself\n   unasked") {
		t.Error("orchestrator/CLAUDE.md is missing the `Never hide yourself unasked` rule — " +
			"chats are on screen by default (the chat-hide invariant); restore duty 3's " +
			"\"Getting out of the way\" paragraph")
	}
	if !strings.Contains(ClaudeMd, "`chat hide`/`chat show` on another chat") {
		t.Error("orchestrator/CLAUDE.md's Guardrails no longer list `chat hide`/`chat show` on " +
			"another chat as propose-then-act — restore it")
	}
}

// TestSeedTeachesEffort guards the grove-435 `--effort` teaching: the
// effort dial is a one-off pin exactly like --model, and the orchestrator
// must know it exists before it reaches for a cheaper model.
func TestSeedTeachesEffort(t *testing.T) {
	if !strings.Contains(ClaudeMd, "gv grab <task> --effort E") {
		t.Error("orchestrator/CLAUDE.md is missing `gv grab <task> --effort E` — restore it in the tools block")
	}
	if !strings.Contains(ClaudeMd, "low|medium|high|xhigh|max") {
		t.Error("orchestrator/CLAUDE.md no longer names the documented effort levels (low|medium|high|xhigh|max)")
	}
}

// TestSeedTeachesEffortFirst (grove-436) guards the routing doctrine in
// duty 3 Dispatch: effort on the default model is the first lever, a
// model/lane change the second, and whichever is proposed is named with
// its reason in the grab line. Scoped to the Duties section so deleting
// the paragraph cannot hide behind a mention elsewhere.
func TestSeedTeachesEffortFirst(t *testing.T) {
	duties := seedSection(t, "Duties")
	for _, want := range []string{
		"Effort first, lane second",
		"--effort",
		"say which setting and why in the same grab line",
		"never route on your own",
	} {
		if !strings.Contains(duties, want) {
			t.Errorf("orchestrator/CLAUDE.md duty 3 Dispatch is missing %q — the effort-first "+
				"routing doctrine (grove-436) has been trimmed; restore it", want)
		}
	}
	if strings.Contains(ClaudeMd, "a cheap task on Sonnet") {
		t.Error("orchestrator/CLAUDE.md still teaches model-first routing (`a cheap task on Sonnet`) — " +
			"grove-436 replaced it with effort-first on the default model")
	}
}

// TestSeedTeachesLearningsReview (grove-439) guards duty 11: the
// orchestrator knows `gv learnings --json` exists, proposes one
// promotion or retirement per note with the target file and text, and
// touches no memory file or skill without the operator's yes.
func TestSeedTeachesLearningsReview(t *testing.T) {
	if !strings.Contains(ClaudeMd, "gv learnings --json") {
		t.Error("orchestrator/CLAUDE.md is missing `gv learnings --json` — restore it in the tools block")
	}
	duties := seedSection(t, "Duties")
	for _, want := range []string{
		"**Learnings review**",
		"gv learnings --json",
		"one promotion or retirement per note",
		"target file and the exact text",
		"Apply only on the operator's yes",
		"Never edit a memory file or a skill unasked",
	} {
		if !strings.Contains(duties, want) {
			t.Errorf("orchestrator/CLAUDE.md Duties is missing %q — the learnings-review duty "+
				"(grove-439) has been trimmed; restore it", want)
		}
	}
}

// flat collapses the seed's hard line wraps so a sentence can be asserted
// whole, wherever the 72-column wrap happens to fall inside it.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

// TestSeedTeachesOverlay (grove-515, brain-overlay car 05) guards the
// `## This seed and the workspace overlay` section: the overlay wins where
// it contradicts the seed, the floor no overlay lowers (the closed list
// of irreversible or outward-facing actions), the named-not-numbered rule
// for workspace duties, and the LEARNINGS.md read at session start. Every
// assertion is scoped to the section, so deleting it cannot hide behind a
// mention elsewhere.
func TestSeedTeachesOverlay(t *testing.T) {
	sec := flat(seedSection(t, "This seed and the workspace overlay"))
	for _, want := range []string{
		"It is replaced wholesale on every grove update, so never edit it.",
		"`CLAUDE.local.md` beside it, if present, is this workspace's overlay",
		"Where the overlay contradicts this file, the overlay wins",
		"A seed rule the overlay does not name still applies in full.",
		"**One floor no overlay lowers:** an irreversible or outward-facing action " +
			"still needs the operator's explicit yes in this chat, or a mandate or land order.",
		"Those actions are merging a PR or a branch, closing an issue, `done`, `untrack`, " +
			"`adopt`, `sweep`, `handoff`, and any `grab` the operator did not ask for.",
		"only by naming it explicitly as one",
		"Workspace duties in the overlay are named, not numbered",
		`cites a seed duty as "duty N (Name)"`,
		"If `LEARNINGS.md` exists beside this file, read it at the start of every session, " +
			"before triage or dispatch.",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("`## This seed and the workspace overlay` no longer says %q — the overlay "+
				"contract (docs/orchestrator-overlay.md) has been trimmed; restore it", want)
		}
	}
	// The section must sit right after the intro, before the tools block,
	// so a brain reads the contract before it reads a single verb.
	if i, j := strings.Index(ClaudeMd, "\n## This seed and the workspace overlay"), strings.Index(ClaudeMd, "\n## Your tools"); i < 0 || j < 0 || i > j {
		t.Error("`## This seed and the workspace overlay` must come before `## Your tools`")
	}
}

// TestSeedTeachesBrainsVerbs (grove-515) guards the three `gv brains`
// forms in the tools block — the verb-surface rule for cars 01-04: a brain
// must know the read, the refresh and the migration exist.
func TestSeedTeachesBrainsVerbs(t *testing.T) {
	tools := seedSection(t, "Your tools")
	for _, want := range []string{
		"gv brains [--json]",
		"gv brains --refresh",
		"gv brains --migrate [--apply]",
	} {
		if !strings.Contains(tools, want) {
			t.Errorf("orchestrator/CLAUDE.md's tools block is missing `%s` — restore it", want)
		}
	}
}

// seedDutyNames is the numbered duty list as an overlay cites it:
// "duty N (Name)". Renaming or renumbering a duty is a deliberate edit
// here too, because every workspace overlay that cites the old name or
// number silently stops matching the seed otherwise.
var seedDutyNames = []string{
	"Fleet summary",
	"Backlog triage",
	"Dispatch",
	"Unstick",
	"Ticket sharpening",
	"Cleanup & recovery",
	"Cost analysis",
	"Context-rot rescue",
	"Remote overflow",
	"Land a feature",
	"Learnings review",
}

var dutyLine = regexp.MustCompile(`(?m)^(\d+)\. \*\*([^*]+)\*\*`)

// TestSeedDutyNamesAreAContract (grove-515) asserts the Duties section's
// numbered list — number and bold name, in order — against seedDutyNames.
func TestSeedDutyNamesAreAContract(t *testing.T) {
	duties := seedSection(t, "Duties")
	var got []string
	for i, m := range dutyLine.FindAllStringSubmatch(duties, -1) {
		if m[1] != strconv.Itoa(i+1) {
			t.Errorf("duty list is not numbered 1..N in order: item %d is numbered %s (%s)", i+1, m[1], m[2])
		}
		got = append(got, m[2])
	}
	if strings.Join(got, "|") != strings.Join(seedDutyNames, "|") {
		t.Errorf("orchestrator/CLAUDE.md's numbered duties changed:\n got: %q\nwant: %q\n"+
			"Overlays cite duties as \"duty N (Name)\" — a rename or renumber is a deliberate "+
			"edit to seedDutyNames here AND to every workspace overlay that cites it", got, seedDutyNames)
	}
}

// TestSeedIsProviderNeutral (grove-515) keeps the thegrid-isms out: the
// seed is born into every workspace, so a task is `<task>`, never a DEV-
// ticket.
func TestSeedIsProviderNeutral(t *testing.T) {
	if strings.Contains(ClaudeMd, "DEV-") {
		t.Error("orchestrator/CLAUDE.md mentions `DEV-` — the seed is provider-neutral; write `<task>`")
	}
	if !strings.Contains(ClaudeMd, "gv grab <task> --repo Y") {
		t.Error("orchestrator/CLAUDE.md is missing the `gv grab <task> --repo Y` example")
	}
}
