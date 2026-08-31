package chat

// grove-216: the transcript→message projection behind `gv chat tail`.
//
// The house rule this file exists to honor: READ THE TRANSCRIPT, NEVER THE
// PANE. A pane capture is ANSI soup hard-wrapped at pane width whose chrome
// has changed under us twice; the `.jsonl` is append-only and structured, so
// following it is a file tail — no tmux polling, no chrome parsing, and the
// same bytes a hook would have quoted.
//
// Everything here is pure: bytes in, entries out. The file half lives in
// tail.go, the tmux half nowhere near it.

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
)

// Roles an entry can carry. Claude Code's transcript files also hold
// bookkeeping lines (`mode`, `permission-mode`, `file-history-snapshot`,
// `attachment`, …) that are not conversation; only these two types project.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Kinds an entry can carry — one per content block, not one per line: a
// single assistant line routinely holds a thinking block and two tool_use
// blocks, and a client that wants to collapse tools needs them separable.
const (
	EntryText       = "text"
	EntryToolUse    = "tool_use"
	EntryToolResult = "tool_result"
	EntryThinking   = "thinking"
)

// Entry is one projected transcript entry — the `gv chat tail` line shape.
// Contract (docs/plugins.md): additive only.
//
// Seq is 1-based and assigned in file order over EMITTED entries, so it is
// stable across runs of the same append-only file: `--since N` resumes from
// exactly where a client left off, and two clients tailing the same chat
// agree on what entry 42 is. Ts is a pointer so a line with no timestamp
// emits `null` rather than year 1 dressed up as a real time.
type Entry struct {
	Seq  int        `json:"seq"`
	Role string     `json:"role"`
	Kind string     `json:"kind"`
	Text string     `json:"text"`
	Tool string     `json:"tool"`
	Ts   *time.Time `json:"ts"`
}

// rawLine is the subset of a transcript line this projection reads. Every
// other field is bookkeeping the phone has no use for.
type rawLine struct {
	Type        string `json:"type"`
	Timestamp   string `json:"timestamp"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// rawBlock is one content block. The Anthropic block types share a `type`
// discriminator and disjoint payload fields, so one struct reads all four.
type rawBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Name      string          `json:"name"`
	ID        string          `json:"id"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

// Projector turns transcript lines into entries, in order. Stateful for two
// reasons: seq must count emitted entries across the whole file, and a
// tool_result names only the tool_use_id it answers — pairing it back to a
// tool NAME needs the tool_use that came before it. One Projector per tail.
type Projector struct {
	seq   int
	tools map[string]string // tool_use id → tool name
}

// NewProjector starts a projection at seq 0 (the first emitted entry is 1).
func NewProjector() *Projector {
	return &Projector{tools: map[string]string{}}
}

// Seq is the last sequence number handed out — what a caller resuming a
// stream would pass as --since.
func (p *Projector) Seq() int { return p.seq }

// Line projects one raw transcript line into zero or more entries.
//
// Zero is the common case and never an error: most lines are bookkeeping,
// and a line that does not parse at all is skipped rather than fatal — a
// transcript is written by another process and half a line is a line that
// is still being written, not a corrupt file. Skips are deterministic, which
// is what keeps seq stable between a `tail` and a later `tail --since`.
func (p *Projector) Line(line []byte) []Entry {
	line = trimLine(line)
	if len(line) == 0 || line[0] != '{' {
		return nil
	}
	var raw rawLine
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil
	}
	if raw.Type != RoleUser && raw.Type != RoleAssistant {
		return nil
	}
	// isMeta lines are injected context (the caller's own preamble, resume
	// banners), and isSidechain lines are a SUBAGENT's private conversation
	// — real transcript entries, but not this chat's, and interleaving them
	// would make a phone read a Task agent's tool spam as the orchestrator
	// talking to itself.
	if raw.IsMeta || raw.IsSidechain {
		return nil
	}
	ts := parseTS(raw.Timestamp)
	role := raw.Type
	if raw.Message.Role != "" {
		role = raw.Message.Role
	}
	var out []Entry
	for _, e := range p.blocks(raw.Message.Content) {
		e.Role, e.Ts = role, ts
		if e.Kind != EntryToolUse && strings.TrimSpace(e.Text) == "" {
			// An empty text/thinking block is chrome (a redacted thinking
			// block carries a signature and no text); a tool_use with no
			// input is still a real call and keeps its row.
			continue
		}
		p.seq++
		e.Seq = p.seq
		out = append(out, e)
	}
	return out
}

// blocks projects a message's `content`, which is either a plain string (the
// operator's own prose — what `gv chat send` produces) or an array of typed
// blocks. Seq/role/ts are filled in by the caller.
func (p *Projector) blocks(content json.RawMessage) []Entry {
	if len(content) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return []Entry{{Kind: EntryText, Text: s}}
	}
	var blocks []rawBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return nil
	}
	var out []Entry
	for _, b := range blocks {
		switch b.Type {
		case "text":
			out = append(out, Entry{Kind: EntryText, Text: b.Text})
		case "thinking", "redacted_thinking":
			out = append(out, Entry{Kind: EntryThinking, Text: b.Thinking})
		case "tool_use":
			if b.ID != "" && b.Name != "" {
				p.tools[b.ID] = b.Name
			}
			out = append(out, Entry{Kind: EntryToolUse, Tool: b.Name, Text: compactJSON(b.Input)})
		case "tool_result":
			// The tool NAME, recovered from the tool_use this answers —
			// without it a client shows a wall of anonymous results, since
			// the raw block carries only an opaque toolu_… id.
			out = append(out, Entry{Kind: EntryToolResult, Tool: p.tools[b.ToolUseID], Text: resultText(b.Content)})
		}
	}
	return out
}

// resultText flattens a tool_result's content, which is a string for most
// tools and an array of text/image blocks for the ones that return
// attachments. Images contribute nothing — a phone gets them from the
// transcript directly if it ever wants them.
//
// Deliberately NOT truncated: a client that wants one collapsed line takes
// the first line itself, and a client that wants the whole result (the
// "expandable" half of the design's tool row) has no second place to get it.
func resultText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}
	var blocks []rawBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return string(content)
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// compactJSON renders a tool_use's input as one line. `null` and an absent
// input both render empty rather than the four-letter word "null".
func compactJSON(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return s
	}
	return buf.String()
}

// parseTS reads the transcript's RFC3339 timestamp, or nil — an unparseable
// stamp is reported as absent, never as a zero time a client would render as
// the year 1.
func parseTS(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}

// trimLine strips the line terminator a reader hands over (and the \r a
// transcript written on another platform may carry).
func trimLine(line []byte) []byte {
	return []byte(strings.TrimRight(string(line), "\r\n"))
}
