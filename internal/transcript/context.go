// Context decoding for the `gv cost --context` view (grove-289): every
// line of a transcript, not just the billable assistant turns ParseUsage
// extracts — so the cost package can see what filled a call's context
// (tool_use inputs, tool_result bodies, prompts) and when a compaction
// happened, not only what it cost.
package transcript

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// Block is one content block of a transcript line. Kind is "text",
// "tool_use", "tool_result", or "thinking" — the four block types a
// transcript line's message.content array carries.
type Block struct {
	Kind      string // "text" | "tool_use" | "tool_result" | "thinking"
	Name      string // tool_use: the tool name; tool_result: resolved via ToolUseID
	ToolUseID string
	Input     map[string]any // tool_use only
	Chars     int            // tool_result / text: byte length of concatenated text content
	Lines     int            // tool_result: count of "\n" in that text
}

// Compact is a `system`/`compact_boundary` line's payload.
type Compact struct {
	Trigger    string
	PreTokens  int
	PostTokens int
}

// Line is one decoded transcript JSONL line.
type Line struct {
	Type      string // "assistant" | "user" | "system" | other
	Subtype   string // system: "compact_boundary" etc.
	MessageID string
	RequestID string
	Model     string
	Usage     *UsageEntry // assistant lines with usage
	Blocks    []Block
	Compact   *Compact // set when Subtype == "compact_boundary"
}

// ctxRawLine is the subset of a transcript line ParseLines reads. Usage
// mirrors usageLine's message.usage shape (ovs.go) so a line's billing
// numbers decode identically whether read by ParseUsage or ParseLines.
type ctxRawLine struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	RequestID string `json:"requestId"`
	Message   struct {
		ID      string          `json:"id"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   *struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreation            *struct {
				Ephemeral5m int `json:"ephemeral_5m_input_tokens"`
				Ephemeral1h int `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
	CompactMetadata *struct {
		Trigger    string `json:"trigger"`
		PreTokens  int    `json:"preTokens"`
		PostTokens int    `json:"postTokens"`
	} `json:"compactMetadata"`
}

// ctxRawBlock is one content block, decoded the same way as
// internal/chat/entry.go:71-80's rawBlock (copied rather than shared —
// that type is chat-package-private and shaped for the tail projection,
// not context accounting).
type ctxRawBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Name      string          `json:"name"`
	ID        string          `json:"id"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

// ParseLines decodes every line of a transcript JSONL stream, in order.
// Same scanner limits as ParseUsage (tool results make huge lines); a line
// that fails to parse is skipped, never fatal — a transcript is written by
// another process and half a line is one still being written.
//
// Unlike ParseUsage, synthetic-model lines are kept (cost.Context skips
// them when deduping API calls; ParseLines itself does no filtering).
func ParseLines(r io.Reader) []Line {
	var lines []Line
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	toolNames := map[string]string{} // tool_use id -> name, resolved across the whole file
	for sc.Scan() {
		var raw ctxRawLine
		if err := json.Unmarshal(sc.Bytes(), &raw); err != nil {
			continue
		}
		l := Line{
			Type: raw.Type, Subtype: raw.Subtype,
			MessageID: raw.Message.ID, RequestID: raw.RequestID, Model: raw.Message.Model,
		}
		if u := raw.Message.Usage; u != nil {
			ue := &UsageEntry{
				MessageID: raw.Message.ID, RequestID: raw.RequestID, Model: raw.Message.Model,
				Input: u.InputTokens, Output: u.OutputTokens, CacheRead: u.CacheReadInputTokens,
			}
			if u.CacheCreation != nil {
				ue.CacheCreate5m = u.CacheCreation.Ephemeral5m
				ue.CacheCreate1h = u.CacheCreation.Ephemeral1h
			} else {
				ue.CacheCreate5m = u.CacheCreationInputTokens
			}
			l.Usage = ue
		}
		if cm := raw.CompactMetadata; cm != nil {
			l.Compact = &Compact{Trigger: cm.Trigger, PreTokens: cm.PreTokens, PostTokens: cm.PostTokens}
		}
		l.Blocks = ctxBlocks(raw.Message.Content, toolNames)
		lines = append(lines, l)
	}
	return lines
}

// ctxBlocks projects a message's `content` — a plain string or an array of
// typed blocks — into Blocks. toolNames is shared across the whole file so
// a tool_result can resolve the tool name from a tool_use several lines
// (even a compaction) earlier.
func ctxBlocks(content json.RawMessage, toolNames map[string]string) []Block {
	if len(content) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		if s == "" {
			return nil
		}
		return []Block{{Kind: "text", Chars: len(s)}}
	}
	var blocks []ctxRawBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return nil
	}
	var out []Block
	for _, b := range blocks {
		switch b.Type {
		case "text":
			out = append(out, Block{Kind: "text", Chars: len(b.Text)})
		case "thinking", "redacted_thinking":
			out = append(out, Block{Kind: "thinking", Chars: len(b.Thinking)})
		case "tool_use":
			if b.ID != "" && b.Name != "" {
				toolNames[b.ID] = b.Name
			}
			var input map[string]any
			if len(b.Input) > 0 {
				_ = json.Unmarshal(b.Input, &input)
			}
			out = append(out, Block{Kind: "tool_use", Name: b.Name, ToolUseID: b.ID, Input: input})
		case "tool_result":
			text, nlines := resultTextAndLines(b.Content)
			out = append(out, Block{
				Kind: "tool_result", Name: toolNames[b.ToolUseID], ToolUseID: b.ToolUseID,
				Chars: len(text), Lines: nlines,
			})
		}
	}
	return out
}

// resultTextAndLines flattens a tool_result's content — a string for most
// tools, an array of text/image blocks for the ones returning attachments
// — and counts its newlines.
func resultTextAndLines(content json.RawMessage) (string, int) {
	if len(content) == 0 {
		return "", 0
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s, strings.Count(s, "\n")
	}
	var blocks []ctxRawBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return "", 0
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	joined := strings.Join(parts, "\n")
	return joined, strings.Count(joined, "\n")
}
