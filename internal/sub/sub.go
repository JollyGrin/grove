// Package sub reads sub.jsonl, the delegation ledger `gv sub` appends one
// record to per micro-task run (grove-288). This package carries only the
// reader: `gv cost --context`'s delegation section is independent of the
// `gv sub` train and must work — zero rows, no error — whether or not
// sub.jsonl, or the writer that produces it, exists yet.
package sub

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
)

// Record is one `gv sub` run: a read-only micro-task dispatched to a
// model_profiles lane outside the worker's own context.
type Record struct {
	Time         string `json:"time"`
	V            int    `json:"v"`
	Ticket       string `json:"ticket"`
	Lane         string `json:"lane"`
	Model        string `json:"model"`
	Mode         string `json:"mode"` // "raw" | "agentic"
	InputChars   int    `json:"input_chars"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	CachedTokens int    `json:"cached_tokens"`
	Turns        int    `json:"turns"`
	Ms           int64  `json:"ms"`
	Exit         int    `json:"exit"`
	PromptHead   string `json:"prompt_head"`
}

func path(stateDir string) string { return filepath.Join(stateDir, "sub.jsonl") }

// Read returns every record in stateDir's sub.jsonl. A missing file reads
// as zero records, not an error — the ledger only exists once `gv sub` has
// run at least once. A line that fails to parse is skipped, never fatal.
func Read(stateDir string) ([]Record, error) {
	f, err := os.Open(path(stateDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		var r Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// ForTicket filters records down to one ticket's runs.
func ForTicket(records []Record, ticket string) []Record {
	var out []Record
	for _, r := range records {
		if r.Ticket == ticket {
			out = append(out, r)
		}
	}
	return out
}
