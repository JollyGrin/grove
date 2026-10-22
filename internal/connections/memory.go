package connections

// grove-437: the auto-memory row. Claude Code gives every session a
// machine-local memory directory, derived from the git repository so all
// of a repo's worktrees share one
// (https://code.claude.com/docs/en/memory): <config dir>/projects/<encoded
// repo path>/memory/, relocated by a settings `autoMemoryDirectory`
// (absolute or ~/ — read from any scope) and switched off by
// `autoMemoryEnabled: false` or CLAUDE_CODE_DISABLE_AUTO_MEMORY=1. Nothing
// else in grove can see whether workers actually write there; this row
// does, per repo.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/transcript"
)

// MemoryDisableEnvVar is Claude Code's environment switch for auto memory.
const MemoryDisableEnvVar = "CLAUDE_CODE_DISABLE_AUTO_MEMORY"

// memoryIndexFile is the always-loaded index; it is not a note, so the
// count leaves it out.
const memoryIndexFile = "MEMORY.md"

// memoryConnections adds one warn row per repo: where that repo's
// workers' auto memory resolves to, how many notes it holds and when the
// newest changed. Warn when the surface is disabled for that profile.
func memoryConnections(env Env) []Connection {
	var conns []Connection
	for _, name := range repoNames(env.Cfg) {
		r := env.Cfg.Repos[name]
		if r == nil || r.Path == "" {
			continue
		}
		conns = append(conns, Connection{
			ID:          "memory:" + name,
			Kind:        KindFile,
			Severity:    SeverityWarn,
			RequiredFor: []string{"grab"},
			Title:       "auto memory for " + name,
			Fix:         "set autoMemoryEnabled true in the worker profile's settings.json / unset " + MemoryDisableEnvVar,
			Check:       checkMemory(r),
		})
	}
	return conns
}

// MemorySurface is the resolved auto-memory surface for one repo's
// workers.
type MemorySurface struct {
	Dir      string // the directory sessions write into
	Source   string // "" (Claude Code default) or the settings.json that relocated it
	Disabled string // "" when enabled, else what disabled it
}

// ResolveMemory works out where a repo's workers keep auto memory, from
// the settings scopes grove can read. Scope precedence is Claude Code's
// own (local > project > user — managed policy is not a file grove reads):
// the first scope that sets a key wins. The profile is the one the repo's
// worker command runs under (the same derivation as the hooks rows), so a
// ccwork fleet resolves under ~/.cc-work, a plain claude one under
// ~/.claude.
func ResolveMemory(e Env, r *config.Repo) MemorySurface {
	configDir := filepath.Join(e.Home, ".claude")
	if e.HookSettingsPaths != nil {
		if paths := e.HookSettingsPaths([]string{r.Claude}); len(paths) > 0 {
			configDir = filepath.Dir(paths[0])
		}
	}
	scopes := []string{
		filepath.Join(r.Path, ".claude", "settings.local.json"),
		filepath.Join(r.Path, ".claude", "settings.json"),
		filepath.Join(configDir, "settings.json"),
	}
	out := MemorySurface{Dir: filepath.Join(configDir, "projects", transcript.EncodePath(filepath.Clean(r.Path)), "memory")}
	if v := e.Getenv(MemoryDisableEnvVar); v != "" && v != "0" {
		out.Disabled = MemoryDisableEnvVar + "=" + v + " in the launching env"
	}
	dirFound, enabledFound := false, out.Disabled != ""
	for _, path := range scopes {
		raw, err := e.ReadFile(path)
		if err != nil {
			continue
		}
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		if !enabledFound {
			if v, ok := m["autoMemoryEnabled"].(bool); ok {
				enabledFound = true
				if !v {
					out.Disabled = "autoMemoryEnabled: false in " + path
				}
			}
		}
		if !dirFound {
			if v, ok := m["autoMemoryDirectory"].(string); ok && v != "" {
				dirFound = true
				if strings.HasPrefix(v, "~/") {
					v = filepath.Join(e.Home, v[2:])
				}
				out.Dir = v
				out.Source = path
			}
		}
	}
	return out
}

// checkMemory renders the surface: warn naming the switch when disabled;
// otherwise ok with the directory, the note count and the newest
// `modified` (a note's frontmatter, else its mtime) — "no notes yet" when
// the directory is empty or absent.
func checkMemory(r *config.Repo) func(Env) Status {
	return func(e Env) Status {
		m := ResolveMemory(e, r)
		if m.Disabled != "" {
			return Status{State: StateWarn, Info: m.Disabled + " — workers cannot write " + m.Dir}
		}
		info := m.Dir
		if m.Source != "" {
			info += " (autoMemoryDirectory in " + m.Source + ")"
		}
		n, newest := memoryNotes(e, m.Dir)
		switch {
		case n == 0:
			info += " — no notes yet"
		case newest.IsZero():
			info += fmt.Sprintf(" — %d note(s)", n)
		default:
			info += fmt.Sprintf(" — %d note(s), newest %s", n, newest.UTC().Format("2006-01-02 15:04Z"))
		}
		return Status{State: StateOK, Info: info}
	}
}

// memoryNotes counts the regular files in dir other than the index and
// returns the newest modification among them: the `modified` frontmatter
// Claude Code stamps on a note when present, else the file's mtime.
func memoryNotes(e Env, dir string) (int, time.Time) {
	if e.ReadDir == nil {
		return 0, time.Time{}
	}
	entries, err := e.ReadDir(dir)
	if err != nil {
		return 0, time.Time{}
	}
	n := 0
	var newest time.Time
	for _, ent := range entries {
		if ent.IsDir() || ent.Name() == memoryIndexFile || strings.HasPrefix(ent.Name(), ".") {
			continue
		}
		n++
		t := noteModified(e, filepath.Join(dir, ent.Name()))
		if t.IsZero() {
			if fi, err := ent.Info(); err == nil {
				t = fi.ModTime()
			}
		}
		if t.After(newest) {
			newest = t
		}
	}
	return n, newest
}

// noteModified parses the `modified:` frontmatter field of a memory note
// (ISO 8601); zero when the file has no frontmatter or the field is
// missing or malformed.
func noteModified(e Env, path string) time.Time {
	raw, err := e.ReadFile(path)
	if err != nil {
		return time.Time{}
	}
	s := string(raw)
	if !strings.HasPrefix(s, "---\n") {
		return time.Time{}
	}
	end := strings.Index(s[4:], "\n---")
	if end < 0 {
		return time.Time{}
	}
	for _, line := range strings.Split(s[4:4+end], "\n") {
		v, ok := strings.CutPrefix(strings.TrimSpace(line), "modified:")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
			if t, err := time.Parse(layout, v); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}
