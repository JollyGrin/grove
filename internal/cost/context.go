// Context decomposition for `gv cost --context` (grove-289): where a
// ticket's per-call context actually came from — not just what it cost.
// Evidence and method: docs/plans/2026-09-06-token-diet-research.md §7.
package cost

import (
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/JollyGrin/grove/internal/sub"
	"github.com/JollyGrin/grove/internal/transcript"
)

// Bucket names one source of context growth.
type GrowthBucket string

const (
	BReadWhole     GrowthBucket = "read_whole"
	BReadTargeted  GrowthBucket = "read_targeted"
	BBashRead      GrowthBucket = "bash_read"
	BBashBuildTest GrowthBucket = "bash_build_test"
	BBashGit       GrowthBucket = "bash_git"
	BBashGh        GrowthBucket = "bash_gh"
	BBashGv        GrowthBucket = "bash_gv_json"
	BMCP           GrowthBucket = "mcp"
	BAgent         GrowthBucket = "agent_report"
	BEdit          GrowthBucket = "edit_write"
	BAsstInput     GrowthBucket = "assistant_tool_input"
	BAsstText      GrowthBucket = "assistant_text"
	BPrompt        GrowthBucket = "prompt_nudge"
	BOther         GrowthBucket = "other"
)

// Bash command regexes, checked in this exact order (docs/plugins.md
// growth-buckets paragraph mirrors this list).
var (
	reBuildTest   = regexp.MustCompile(`\b(go test|go build|go vet|gofmt|pnpm|npm|yarn|bun|vitest|jest|tsc|eslint|playwright)\b|(^|\s)e2e/|\.sh\b`)
	reGitReadonly = regexp.MustCompile(`\bgit (diff|log|show|status)\b`)
	reGh          = regexp.MustCompile(`\bgh\b`)
	reGvJSON      = regexp.MustCompile(`\bgv \S+.*--json`)
	reBashRead    = regexp.MustCompile(`\b(cat|sed -n|grep|rg|head|tail|awk|wc|ls|find)\b`)
)

// Classify buckets one tool call by tool name and input: Read's
// offset/limit split first, then the name-based rules (mcp__ prefix,
// Agent/Task, Edit/Write/MultiEdit), then the Bash command regex chain in
// fixed order (build/test, git read, gh, gv --json, generic read, else
// other).
func Classify(tool string, input map[string]any) GrowthBucket {
	switch tool {
	case "Read":
		if hasOffsetOrLimit(input) {
			return BReadTargeted
		}
		return BReadWhole
	case "Agent", "Task":
		return BAgent
	case "Edit", "Write", "MultiEdit":
		return BEdit
	case "Bash":
		return classifyBash(inputString(input, "command"))
	}
	if strings.HasPrefix(tool, "mcp__") {
		return BMCP
	}
	return BOther
}

func classifyBash(cmd string) GrowthBucket {
	switch {
	case reBuildTest.MatchString(cmd):
		return BBashBuildTest
	case reGitReadonly.MatchString(cmd):
		return BBashGit
	case reGh.MatchString(cmd):
		return BBashGh
	case reGvJSON.MatchString(cmd):
		return BBashGv
	case reBashRead.MatchString(cmd):
		return BBashRead
	default:
		return BOther
	}
}

func hasOffsetOrLimit(input map[string]any) bool {
	if input == nil {
		return false
	}
	_, hasOffset := input["offset"]
	_, hasLimit := input["limit"]
	return hasOffset || hasLimit
}

func inputString(input map[string]any, key string) string {
	if input == nil {
		return ""
	}
	s, _ := input[key].(string)
	return s
}

// headFor is the identifying label a Top row shows for a tool_result's
// bucket: the file for reads/edits, the command for bash buckets, the
// tool name otherwise.
func headFor(bucket GrowthBucket, tool string, input map[string]any) string {
	switch bucket {
	case BReadWhole, BReadTargeted, BEdit:
		if fp := inputString(input, "file_path"); fp != "" {
			return fp
		}
		return tool
	case BBashRead, BBashBuildTest, BBashGit, BBashGh, BBashGv, BOther:
		if cmd := inputString(input, "command"); cmd != "" {
			return cmd
		}
		return tool
	case BAgent:
		if d := inputString(input, "description"); d != "" {
			return d
		}
		return tool
	default:
		return tool
	}
}

// Amplified is one context-growth contribution: a block whose content
// stayed in every subsequent call's context.
type Amplified struct {
	Bucket     GrowthBucket `json:"bucket"`
	Tool       string       `json:"tool"`
	Head       string       `json:"head"`
	Chars      int          `json:"chars"`
	CallsAfter int          `json:"calls_after"`
	EstTokens  int          `json:"est_tokens"`
}

// CompactRec is one `compact_boundary` system line.
type CompactRec struct {
	Trigger    string `json:"trigger"`
	PreTokens  int    `json:"pre_tokens"`
	PostTokens int    `json:"post_tokens"`
	AtCall     int    `json:"at_call"`
}

// Delegation summarizes a ticket's `gv sub` runs (grove-288). Zero value
// when sub.jsonl has no records for the ticket — never an error.
type Delegation struct {
	Calls        int   `json:"calls"`
	Raw          int   `json:"raw"`
	Agentic      int   `json:"agentic"`
	Failures     int   `json:"failures"`
	InputChars   int   `json:"input_chars"`
	OutputTokens int   `json:"output_tokens"`
	Millis       int64 `json:"ms"`
}

// ContextReport is the `gv cost --context` decomposition for one ticket.
type ContextReport struct {
	APICalls    int                      `json:"api_calls"`
	AvgCtx      int                      `json:"avg_ctx"`
	P90Ctx      int                      `json:"p90_ctx"`
	MaxCtx      int                      `json:"max_ctx"`
	Floor       int                      `json:"floor"`
	FloorShare  float64                  `json:"floor_share"`
	CtxTokens   int                      `json:"ctx_tokens"`
	Compactions []CompactRec             `json:"compactions"`
	Growth      map[GrowthBucket]float64 `json:"growth"`
	Top         []Amplified              `json:"top"`
	Delegation  Delegation               `json:"delegation"`
}

// growthEvent is one block's contribution before its CallsAfter multiplier
// is known (that needs the FINAL call count, only known once the whole
// transcript has been walked).
type growthEvent struct {
	bucket GrowthBucket
	tool   string
	head   string
	chars  int
	k      int // calls completed at the time this block was emitted
}

// Context decomposes a ticket's transcript lines (every session +
// subagent file, concatenated in file order) into the per-call context
// report. subs is this ticket's `gv sub` records (already filtered by the
// caller) — pass nil/empty when sub.jsonl has none.
//
// Dedup mirrors Total (cost.go:215): assistant usage lines are counted
// once per (message.id, requestId), and <synthetic> models never count as
// a call. Call index k is 1-based; a block emitted between call k and
// k+1 (including blocks of call k's own message) has CallsAfter =
// totalCalls-k and is weighted Chars*CallsAfter. Blocks emitted before
// the FIRST call (k still 0 — the floor: system prompt, tool schemas,
// CLAUDE.md) are not weighted at all — they are fixed overhead every call
// pays, not growth.
func Context(lines []transcript.Line, subs []sub.Record, top int) ContextReport {
	rep := ContextReport{
		Compactions: []CompactRec{},
		Growth:      map[GrowthBucket]float64{},
		Top:         []Amplified{},
	}

	seen := map[[2]string]bool{}
	var ctxSizes []int
	calls := 0
	toolInputs := map[string]map[string]any{}
	var events []growthEvent

	for _, l := range lines {
		if l.Subtype == "compact_boundary" && l.Compact != nil {
			rep.Compactions = append(rep.Compactions, CompactRec{
				Trigger: l.Compact.Trigger, PreTokens: l.Compact.PreTokens, PostTokens: l.Compact.PostTokens,
				AtCall: calls,
			})
		}
		if l.Type == "assistant" && l.Usage != nil && l.Model != "<synthetic>" {
			key := [2]string{l.MessageID, l.RequestID}
			if key == [2]string{} || !seen[key] {
				seen[key] = true
				calls++
				ctxTok := l.Usage.Input + l.Usage.CacheRead + l.Usage.CacheCreate5m + l.Usage.CacheCreate1h
				ctxSizes = append(ctxSizes, ctxTok)
				rep.CtxTokens += ctxTok
			}
		}
		if calls == 0 {
			continue // floor content: not weighted (no call has happened yet)
		}
		for _, b := range l.Blocks {
			switch b.Kind {
			case "tool_use":
				if b.ToolUseID != "" {
					toolInputs[b.ToolUseID] = b.Input
				}
				events = append(events, growthEvent{
					bucket: BAsstInput, tool: b.Name, head: b.Name, chars: marshalLen(b.Input), k: calls,
				})
			case "tool_result":
				input := toolInputs[b.ToolUseID]
				bucket := Classify(b.Name, input)
				events = append(events, growthEvent{
					bucket: bucket, tool: b.Name, head: headFor(bucket, b.Name, input), chars: b.Chars, k: calls,
				})
			case "text":
				switch l.Type {
				case "user":
					events = append(events, growthEvent{bucket: BPrompt, chars: b.Chars, k: calls})
				case "assistant":
					events = append(events, growthEvent{bucket: BAsstText, chars: b.Chars, k: calls})
				}
			}
		}
	}

	rep.APICalls = calls
	if calls > 0 {
		rep.AvgCtx = rep.CtxTokens / calls
		rep.MaxCtx = maxInt(ctxSizes)
		rep.P90Ctx = percentile(ctxSizes, 0.9)
		rep.Floor = ctxSizes[0]
		if rep.CtxTokens > 0 {
			rep.FloorShare = float64(rep.Floor*calls) / float64(rep.CtxTokens)
		}
	}

	amplified := make([]Amplified, 0, len(events))
	weightByBucket := map[GrowthBucket]float64{}
	for _, e := range events {
		callsAfter := calls - e.k
		weight := float64(e.chars) * float64(callsAfter)
		weightByBucket[e.bucket] += weight
		amplified = append(amplified, Amplified{
			Bucket: e.bucket, Tool: e.tool, Head: e.head, Chars: e.chars,
			CallsAfter: callsAfter, EstTokens: int(weight / 1.9),
		})
	}
	sort.Slice(amplified, func(i, j int) bool {
		wi := float64(amplified[i].Chars) * float64(amplified[i].CallsAfter)
		wj := float64(amplified[j].Chars) * float64(amplified[j].CallsAfter)
		if wi != wj {
			return wi > wj
		}
		return amplified[i].Chars > amplified[j].Chars
	})
	if top > 0 && len(amplified) > top {
		amplified = amplified[:top]
	}
	rep.Top = amplified

	if rep.CtxTokens > 0 {
		for b, w := range weightByBucket {
			rep.Growth[b] = (w / 1.9) / float64(rep.CtxTokens)
		}
	}

	for _, r := range subs {
		rep.Delegation.Calls++
		if r.Mode == "raw" {
			rep.Delegation.Raw++
		} else {
			rep.Delegation.Agentic++
		}
		if r.Exit != 0 {
			rep.Delegation.Failures++
		}
		rep.Delegation.InputChars += r.InputChars
		rep.Delegation.OutputTokens += r.OutputTokens
		rep.Delegation.Millis += r.Ms
	}

	return rep
}

// ContextHeavy: the fleet-mean context per call is deep enough that a
// context cap or `gv sub` delegation would materially help.
func ContextHeavy(avgCtx int) bool { return avgCtx >= 200_000 }

// NeverCompacted: a long-running ticket (150+ calls) that has never once
// crossed a compaction boundary — either genuinely tight, or the auto-
// compact threshold never fired because of the `[1m]` cache-tier pin
// (LEARNINGS.md).
func NeverCompacted(calls, compactions int) bool {
	return calls >= 150 && compactions == 0
}

func marshalLen(input map[string]any) int {
	if len(input) == 0 {
		return 0
	}
	b, err := json.Marshal(input)
	if err != nil {
		return 0
	}
	return len(b)
}

func maxInt(vs []int) int {
	m := 0
	for _, v := range vs {
		if v > m {
			m = v
		}
	}
	return m
}

// percentile is the nearest-rank p-th percentile (p in [0,1]) of vs.
func percentile(vs []int, p float64) int {
	if len(vs) == 0 {
		return 0
	}
	sorted := append([]int(nil), vs...)
	sort.Ints(sorted)
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
