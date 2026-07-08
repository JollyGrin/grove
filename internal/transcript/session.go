package transcript

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Session represents a Claude Code session associated with a worktree.
type Session struct {
	ID          string
	CWD         string
	FirstPrompt string // 80-char truncated for list labels
	FullPrompt  string // 4000-char version for preview
	ModTime     time.Time
	GitBranch   string
}

// EncodePath converts an absolute path to Claude's project directory encoding.
// Rules: / → -, . → -
func EncodePath(absPath string) string {
	s := strings.ReplaceAll(absPath, "/", "-")
	s = strings.ReplaceAll(s, ".", "-")
	return s
}

// ProjectDir returns the <config-dir>/projects/<encoded> directory for a
// worktree path. Grove workers run under the default Claude Code config dir
// (~/.claude); GV_CLAUDE_CONFIG_DIR overrides it for ovs-style repos whose
// workers run under a different profile and for the e2e harness.
func ProjectDir(worktreePath string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	configDir := os.Getenv("GV_CLAUDE_CONFIG_DIR")
	if configDir == "" {
		configDir = filepath.Join(home, ".claude")
	}
	encoded := EncodePath(worktreePath)
	return filepath.Join(configDir, "projects", encoded)
}

// ListSessions finds Claude Code sessions that were actually run in the given worktree path.
// It scans JSONL files in the project directory, filtering by cwd match.
// Returns sessions sorted by ModTime descending (most recent first).
func ListSessions(worktreePath string) ([]Session, error) {
	dir := ProjectDir(worktreePath)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var sessions []Session
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}

		filePath := filepath.Join(dir, e.Name())
		meta, err := parseJSONLMeta(filePath, worktreePath)
		if err != nil || meta == nil {
			continue
		}

		info, err := e.Info()
		if err != nil {
			continue
		}

		sessions = append(sessions, Session{
			ID:          meta.ID,
			CWD:         meta.CWD,
			FirstPrompt: meta.FirstPrompt,
			FullPrompt:  meta.FullPrompt,
			ModTime:     info.ModTime(),
			GitBranch:   meta.GitBranch,
		})
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].ModTime.After(sessions[j].ModTime)
	})

	return sessions, nil
}
