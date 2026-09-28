// Boot-record decoding: the pre-first-call lines of a session transcript
// (system prompt, instructions, skills, deltas, the kickoff prompt) plus the
// first billable assistant call they paid for. New file — ovs.go/parse.go/
// session.go stay untouched.
package transcript

import (
	"bufio"
	"encoding/json"
	"io"
)

// BootFile is one file from an "instructions" attachment.
type BootFile struct {
	Path    string `json:"path"`
	Type    string `json:"type"`
	Content string `json:"content"`
}

// BootAttachment is the union of every attachment.type the boot record can
// carry. Only the fields for its own Type are populated; RawLen is set only
// for a type not in the known table (the "other" bucket), and holds the
// byte length of the attachment object's own JSON.
type BootAttachment struct {
	Type string

	SystemPrompt []string   // prompt_snapshot
	Files        []BootFile // instructions
	Content      string     // skill_listing
	SkillCount   int        // skill_listing
	AddedLines   []string   // deferred_tools_delta, agent_listing_delta
	AddedNames   []string   // deferred_tools_delta
	AddedBlocks  []string   // mcp_instructions_delta
	RawLen       int        // anything else
}

// BootLine is a tolerant decode of one line preceding the first billable
// assistant call. Classification into named boot parts happens in package
// cost; this only carries what that classification needs.
type BootLine struct {
	Type       string // the line's own "type" field
	Attachment *BootAttachment
	UserChars  int  // text-block (or plain string) chars, type == "user" only
	HasImage   bool // an image block was present, type == "user" only
}

// bootAssistantLine mirrors the assistant-line shape ParseUsage decodes,
// plus isSidechain — ParseBoot must skip sidechain turns when looking for
// the first billable call, which ParseUsage never needed to.
type bootAssistantLine struct {
	RequestID         string `json:"requestId"`
	Timestamp         string `json:"timestamp"`
	IsSidechain       bool   `json:"isSidechain"`
	IsAPIErrorMessage bool   `json:"isApiErrorMessage"`
	Message           struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
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
}

// ParseBoot reads a session JSONL stream and splits it at the first billable
// assistant call: lines is every line before it, decoded as boot context;
// first is that call's usage (nil when the transcript never has one). It
// stops reading as soon as that line is found — same scanner buffer as
// ParseUsage, since boot context can carry a full skill listing or system
// prompt on one line. Garbage lines never abort the scan.
func ParseBoot(r io.Reader) (first *UsageEntry, lines []BootLine) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		raw := sc.Bytes()
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			continue
		}
		if probe.Type == "assistant" {
			var l bootAssistantLine
			if err := json.Unmarshal(raw, &l); err != nil {
				continue
			}
			if l.Message.Usage == nil || l.IsSidechain || l.IsAPIErrorMessage || l.Message.Model == "<synthetic>" {
				continue
			}
			u := l.Message.Usage
			e := &UsageEntry{
				MessageID: l.Message.ID,
				RequestID: l.RequestID,
				Model:     l.Message.Model,
				Input:     u.InputTokens,
				Output:    u.OutputTokens,
				CacheRead: u.CacheReadInputTokens,
				Timestamp: l.Timestamp,
			}
			if u.CacheCreation != nil {
				e.CacheCreate5m = u.CacheCreation.Ephemeral5m
				e.CacheCreate1h = u.CacheCreation.Ephemeral1h
			} else {
				e.CacheCreate5m = u.CacheCreationInputTokens
			}
			return e, lines
		}
		lines = append(lines, decodeBootLine(probe.Type, raw))
	}
	return nil, lines
}

func decodeBootLine(lineType string, raw []byte) BootLine {
	bl := BootLine{Type: lineType}
	switch lineType {
	case "attachment":
		var env struct {
			Attachment json.RawMessage `json:"attachment"`
		}
		if err := json.Unmarshal(raw, &env); err != nil || len(env.Attachment) == 0 {
			return bl
		}
		bl.Attachment = decodeBootAttachment(env.Attachment)
	case "user":
		var env struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			return bl
		}
		bl.UserChars, bl.HasImage = decodeUserContent(env.Message.Content)
	}
	return bl
}

func decodeBootAttachment(raw json.RawMessage) *BootAttachment {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil
	}
	a := &BootAttachment{Type: head.Type}
	switch head.Type {
	case "prompt_snapshot":
		var v struct {
			SystemPrompt []string `json:"systemPrompt"`
		}
		_ = json.Unmarshal(raw, &v)
		a.SystemPrompt = v.SystemPrompt
	case "instructions":
		var v struct {
			Files []BootFile `json:"files"`
		}
		_ = json.Unmarshal(raw, &v)
		a.Files = v.Files
	case "skill_listing":
		var v struct {
			Content    string `json:"content"`
			SkillCount int    `json:"skillCount"`
		}
		_ = json.Unmarshal(raw, &v)
		a.Content = v.Content
		a.SkillCount = v.SkillCount
	case "deferred_tools_delta":
		var v struct {
			AddedLines []string `json:"addedLines"`
			AddedNames []string `json:"addedNames"`
		}
		_ = json.Unmarshal(raw, &v)
		a.AddedLines = v.AddedLines
		a.AddedNames = v.AddedNames
	case "agent_listing_delta":
		var v struct {
			AddedLines []string `json:"addedLines"`
		}
		_ = json.Unmarshal(raw, &v)
		a.AddedLines = v.AddedLines
	case "mcp_instructions_delta":
		var v struct {
			AddedBlocks []string `json:"addedBlocks"`
		}
		_ = json.Unmarshal(raw, &v)
		a.AddedBlocks = v.AddedBlocks
	default:
		a.RawLen = len(raw)
	}
	return a
}

// decodeUserContent handles message.content being either a plain string or a
// list of typed blocks. Only "text" blocks count toward chars — an "image"
// block is base64 and would blow the estimate up by orders of magnitude.
func decodeUserContent(raw json.RawMessage) (chars int, hasImage bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return len(s), false
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return 0, false
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			chars += len(b.Text)
		case "image":
			hasImage = true
		}
	}
	return chars, hasImage
}
