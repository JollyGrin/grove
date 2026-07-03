// Overstory additions to the copied parkranger transcript wrapper. New
// functionality lives here so parse.go/session.go stay byte-comparable
// with upstream.
package transcript

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// UsageEntry is one billable assistant turn from a session JSONL.
// CostUSD is non-nil only on older Claude Code versions that wrote
// per-entry costs (the "auto" cost mode prefers it when present).
type UsageEntry struct {
	MessageID     string
	RequestID     string
	Model         string
	CostUSD       *float64
	Input         int
	Output        int
	CacheCreate5m int
	CacheCreate1h int
	CacheRead     int
	Timestamp     string
}

// usageLine mirrors the JSONL shapes observed live 2026-07-02 plus the
// older flat-cache/costUSD variants ccusage documents. Decoded tolerantly:
// unknown fields ignored, unparseable lines skipped.
type usageLine struct {
	RequestID         string   `json:"requestId"`
	Timestamp         string   `json:"timestamp"`
	IsAPIErrorMessage bool     `json:"isApiErrorMessage"`
	CostUSD           *float64 `json:"costUSD"`
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

// ParseUsage extracts billable usage entries from a session JSONL stream.
// Lines without message.usage (user turns, tool results), API-error
// entries, and synthetic models carry no cost and are skipped; garbage
// lines never abort the scan (transcripts can have torn final lines).
func ParseUsage(r io.Reader) []UsageEntry {
	var entries []UsageEntry
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024) // tool results make huge lines
	for sc.Scan() {
		var l usageLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			continue
		}
		if l.Message.Usage == nil || l.IsAPIErrorMessage || l.Message.Model == "<synthetic>" {
			continue
		}
		u := l.Message.Usage
		e := UsageEntry{
			MessageID: l.Message.ID,
			RequestID: l.RequestID,
			Model:     l.Message.Model,
			CostUSD:   l.CostUSD,
			Input:     u.InputTokens,
			Output:    u.OutputTokens,
			CacheRead: u.CacheReadInputTokens,
			Timestamp: l.Timestamp,
		}
		if u.CacheCreation != nil {
			e.CacheCreate5m = u.CacheCreation.Ephemeral5m
			e.CacheCreate1h = u.CacheCreation.Ephemeral1h
		} else {
			// Old shape: flat total only — 5m was the only cache tier then.
			e.CacheCreate5m = u.CacheCreationInputTokens
		}
		entries = append(entries, e)
	}
	return entries
}

// SessionFiles lists every transcript file for a worktree: all session
// JSONLs in the encoded project dir PLUS each session's
// <uuid>/subagents/agent-*.jsonl (subagent usage is written separately and
// excluded from parent totals — CC issue #60591; layout verified live
// 2026-07-02). Discovery is cwd-scan by design: the folded task keeps only
// its latest session id, but grab/adopt/resume all share the worktree
// path, so scanning the dir captures the ticket's full history.
func SessionFiles(worktreePath string) ([]string, error) {
	projDir := ProjectDir(worktreePath)
	entries, err := os.ReadDir(projDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() {
			if strings.HasSuffix(e.Name(), ".jsonl") {
				files = append(files, filepath.Join(projDir, e.Name()))
			}
			continue
		}
		sub := filepath.Join(projDir, e.Name(), "subagents")
		agents, err := os.ReadDir(sub)
		if err != nil {
			continue
		}
		for _, a := range agents {
			if !a.IsDir() && strings.HasPrefix(a.Name(), "agent-") && strings.HasSuffix(a.Name(), ".jsonl") {
				files = append(files, filepath.Join(sub, a.Name()))
			}
		}
	}
	return files, nil
}
