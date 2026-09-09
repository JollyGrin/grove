package sub

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/JollyGrin/grove/internal/schema"
)

// Record is one gv sub call, appended to <stateDir>/sub.jsonl. The answer
// text and the credential are never recorded.
type Record struct {
	Time         time.Time `json:"time"`
	V            int       `json:"v"`
	Workspace    string    `json:"workspace"`
	Ticket       string    `json:"ticket"`
	Lane         string    `json:"lane"`
	Model        string    `json:"model"`
	Mode         string    `json:"mode"` // "raw" | "agentic"
	InputChars   int       `json:"input_chars"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	CachedTokens int       `json:"cached_tokens"`
	Turns        int       `json:"turns"`
	Millis       int64     `json:"ms"`
	Exit         int       `json:"exit"`
	PromptHead   string    `json:"prompt_head"`
}

// Path returns the ledger file location inside a state dir.
func Path(stateDir string) string { return filepath.Join(stateDir, "sub.jsonl") }

// Append writes one Record under an exclusive flock, following the
// events.jsonl discipline (O_APPEND + flock) so concurrent gv sub calls
// never interleave.
func Append(stateDir string, r Record) error {
	if r.V == 0 {
		r.V = schema.Version
	}
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	f, err := os.OpenFile(Path(stateDir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("flock: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_, err = f.Write(line)
	return err
}

// Read parses sub.jsonl tolerantly: unparseable lines (a torn concurrent
// write, or a schema this reader predates) are skipped, never fatal.
func Read(stateDir string) ([]Record, error) {
	f, err := os.Open(Path(stateDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var records []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		records = append(records, r)
	}
	return records, nil
}
