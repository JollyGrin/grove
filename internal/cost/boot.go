package cost

import (
	"sort"
	"strings"

	"github.com/JollyGrin/grove/internal/transcript"
)

// BootPart is one named slice of a session's boot context — a system
// prompt, one instructions file, a skill listing, the kickoff prompt, or the
// unaccounted residual (tool schemas, never recorded in the transcript).
// EstTokens is always Chars*10/36 — an estimate, never a billed figure.
type BootPart struct {
	Name      string `json:"name"`
	Path      string `json:"path,omitempty"`
	Chars     int    `json:"chars"`
	EstTokens int    `json:"est_tokens"`
	Count     int    `json:"count,omitempty"`
}

// Boot is the decoded boot record of one session: what its first billable
// request cost, and the parts of the pre-call context that paid for it.
type Boot struct {
	Tokens     int        `json:"tokens"`
	Input      int        `json:"input_tokens"`
	CacheWrite int        `json:"cache_write_tokens"`
	CacheRead  int        `json:"cache_read_tokens"`
	Model      string     `json:"model"`
	Timestamp  string     `json:"timestamp"`
	HasImage   bool       `json:"has_image"`
	Parts      []BootPart `json:"parts"`
}

func estTokens(chars int) int {
	return chars * 10 / 36
}

// classifyInstructionsFile names an instructions-attachment file's part:
// MEMORY.md (by base name, any directory) is the cross-repo memory index; a
// CLAUDE.md anywhere under a .grove/orchestrator/ tree (including a profile
// subdir) is the orchestrator brain; everything else is repo instructions.
func classifyInstructionsFile(path string) string {
	norm := strings.ReplaceAll(path, "\\", "/")
	if base := norm[strings.LastIndexByte(norm, '/')+1:]; base == "MEMORY.md" {
		return "memory_index"
	}
	if strings.Contains(norm, ".grove/orchestrator/") && strings.HasSuffix(norm, "CLAUDE.md") {
		return "orchestrator_brain"
	}
	return "repo_instructions"
}

type bootPartKey struct{ name, path string }

// BootOf decodes ParseBoot's output into a Boot: ok is false when the
// transcript never reached a billable assistant call. tool_schemas_est is
// the residual left after every other part is accounted for, clamped at 0 —
// tool schemas are never recorded in the transcript, so this is the only way
// to estimate their share.
func BootOf(first *transcript.UsageEntry, lines []transcript.BootLine) (Boot, bool) {
	if first == nil {
		return Boot{}, false
	}

	b := Boot{
		Input:      first.Input,
		CacheWrite: first.CacheCreate5m + first.CacheCreate1h,
		CacheRead:  first.CacheRead,
		Model:      first.Model,
		Timestamp:  first.Timestamp,
	}
	b.Tokens = b.Input + b.CacheWrite + b.CacheRead

	acc := map[bootPartKey]*BootPart{}
	var order []bootPartKey
	add := func(name, path string, chars, count int) {
		k := bootPartKey{name, path}
		p, ok := acc[k]
		if !ok {
			p = &BootPart{Name: name, Path: path}
			acc[k] = p
			order = append(order, k)
		}
		p.Chars += chars
		p.Count += count
	}

	for _, l := range lines {
		switch l.Type {
		case "attachment":
			addAttachment(add, l.Attachment)
		case "user":
			add("first_prompt", "", l.UserChars, 0)
			if l.HasImage {
				b.HasImage = true
			}
		}
	}

	var parts []BootPart
	sumEst := 0
	for _, k := range order {
		p := *acc[k]
		p.EstTokens = estTokens(p.Chars)
		sumEst += p.EstTokens
		parts = append(parts, p)
	}

	residual := max(b.Tokens-sumEst, 0)
	parts = append(parts, BootPart{Name: "tool_schemas_est", EstTokens: residual})

	sort.SliceStable(parts, func(i, j int) bool {
		if parts[i].EstTokens != parts[j].EstTokens {
			return parts[i].EstTokens > parts[j].EstTokens
		}
		return parts[i].Name < parts[j].Name
	})
	b.Parts = parts

	return b, true
}

func addAttachment(add func(name, path string, chars, count int), a *transcript.BootAttachment) {
	if a == nil {
		return
	}
	switch a.Type {
	case "prompt_snapshot":
		add("system_prompt", "", len(strings.Join(a.SystemPrompt, "")), 0)
	case "instructions":
		for _, f := range a.Files {
			add(classifyInstructionsFile(f.Path), f.Path, len(f.Content), 0)
		}
	case "skill_listing":
		add("skill_listing", "", len(a.Content), a.SkillCount)
	case "deferred_tools_delta":
		add("deferred_tools", "", len(strings.Join(a.AddedLines, "\n")), len(a.AddedNames))
	case "agent_listing_delta":
		add("agent_listing", "", len(strings.Join(a.AddedLines, "\n")), 0)
	case "mcp_instructions_delta":
		add("mcp_instructions", "", len(strings.Join(a.AddedBlocks, "\n")), 0)
	default:
		add("other", "", a.RawLen, 0)
	}
}
