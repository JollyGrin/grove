// Package learnings is the read side of the promotion loop (grove-439,
// model-fit 07): what Claude wrote into a repo's auto memory recently,
// what LEARNINGS.md gained, and which entries look ready to graduate
// into a skill. It never writes — promotions and retirements are the
// orchestrator's proposal and the operator's yes (orchestrator/CLAUDE.md,
// duty 11). The three surfaces it reads are the ones CLAUDE.md names:
// auto memory (machine-local notes, one lesson per file, indexed by
// MEMORY.md), LEARNINGS.md (dated, committed) and .claude/skills/ (the
// rules that generalized).
package learnings

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// IndexFile is the always-loaded memory index; it is not a note.
const IndexFile = "MEMORY.md"

// TypeFeedback is the note type for corrections and confirmed approaches
// — the ones a review surface wants first.
const TypeFeedback = "feedback"

// Note is one auto-memory file.
type Note struct {
	File        string    `json:"file"`                  // basename inside the memory dir
	Name        string    `json:"name"`                  // frontmatter name, else the basename without .md
	Type        string    `json:"type,omitempty"`        // user|feedback|project|reference (frontmatter; "" when absent)
	Description string    `json:"description,omitempty"` // frontmatter description, else the MEMORY.md hook
	Modified    time.Time `json:"modified"`              // frontmatter `modified`, else the file's mtime
	ModifiedBy  string    `json:"modified_by"`           // "frontmatter" or "mtime"
	Indexed     bool      `json:"indexed"`               // listed in MEMORY.md
	Error       string    `json:"error,omitempty"`       // malformed frontmatter / unreadable — reported, never fatal
}

// IndexEntry is one `- [Title](file.md) — hook` line of MEMORY.md.
type IndexEntry struct {
	Title string `json:"title"`
	File  string `json:"file"`
	Hook  string `json:"hook,omitempty"`
}

// Memory is the scanned auto-memory directory.
type Memory struct {
	Dir      string       `json:"dir"`
	Source   string       `json:"source,omitempty"`   // settings.json that relocated it (autoMemoryDirectory), "" for the default
	Disabled string       `json:"disabled,omitempty"` // what switched the surface off, "" when enabled
	Total    int          `json:"notes_total"`        // every note in the dir, regardless of --since
	Index    []IndexEntry `json:"index"`              // MEMORY.md lines; empty when there is no index
	Notes    []Note       `json:"notes"`              // newer than --since: feedback first, then newest first
	Warnings []string     `json:"warnings,omitempty"` // index lines naming a missing file, notes with errors
}

// Entry is one LEARNINGS.md entry (`- **YYYY-MM-DD · fact** — context`).
type Entry struct {
	Date    string   `json:"date,omitempty"` // "" on an undated (seeded) entry
	Section string   `json:"section"`
	Fact    string   `json:"fact"`              // the bold headline after the date
	Text    string   `json:"text"`              // the whole entry, continuation lines joined
	Tickets []string `json:"tickets,omitempty"` // grove-N ids mentioned
	Skills  []string `json:"skills,omitempty"`  // skill names mentioned (from .claude/skills/)
}

// Candidate is an entry that names a skill whose SKILL.md cites none of
// the entry's ticket ids, its date, or its headline — the rule has not
// been distilled yet, so it is a promotion to propose.
type Candidate struct {
	Skill string `json:"skill"`
	Date  string `json:"date,omitempty"`
	Fact  string `json:"fact"`
	Why   string `json:"why"`
}

// RepoReport is `gv learnings` for one configured repo.
type RepoReport struct {
	Repo          string      `json:"repo"`
	Memory        Memory      `json:"memory"`
	LearningsFile string      `json:"learnings_file,omitempty"` // "" when the repo keeps none
	Entries       []Entry     `json:"entries"`                  // dated on/after --since, newest first
	Candidates    []Candidate `json:"candidates"`
	Skills        []string    `json:"skills"` // skill names found under .claude/skills/
}

// Report is the whole `gv learnings --json` payload.
type Report struct {
	Since time.Time    `json:"since"`
	Repos []RepoReport `json:"repos"`
}

// ParseSince reads a `--since` value: Go durations (`48h`) plus `Nd`
// (days) and `Nw` (weeks).
func ParseSince(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if n := len(s); n > 1 {
		if v, err := strconv.Atoi(s[:n-1]); err == nil && v >= 0 {
			switch s[n-1] {
			case 'd':
				return time.Duration(v) * 24 * time.Hour, nil
			case 'w':
				return time.Duration(v) * 7 * 24 * time.Hour, nil
			}
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("--since %q: want a duration like 14d, 2w or 48h", s)
	}
	return d, nil
}

// --- auto memory -------------------------------------------------------

var indexLine = regexp.MustCompile(`^\s*-\s*\[([^\]]*)\]\(([^)]+)\)\s*(?:[—–-]+\s*(.*))?$`)

// ParseIndex reads MEMORY.md's `- [Title](file.md) — hook` lines; any
// other line is index prose and skipped.
func ParseIndex(text string) []IndexEntry {
	var out []IndexEntry
	for _, line := range strings.Split(text, "\n") {
		m := indexLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		out = append(out, IndexEntry{Title: m[1], File: m[2], Hook: strings.TrimSpace(m[3])})
	}
	return out
}

// Frontmatter is the subset of a note's YAML header a report needs.
type Frontmatter struct {
	Name        string
	Type        string
	Description string
	Modified    time.Time
}

// ParseFrontmatter reads the `---` block at the top of a note. Keys are
// read flat (`type: feedback`) and one level under `metadata:` (Claude
// Code's shape). ok is false when the note has no frontmatter at all;
// err reports a block that opens and never closes.
func ParseFrontmatter(text string) (fm Frontmatter, ok bool, err error) {
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return fm, false, nil
	}
	body := text[strings.Index(text, "\n")+1:]
	end := -1
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if strings.TrimRight(line, "\r") == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return fm, true, fmt.Errorf("frontmatter opened with --- but never closed")
	}
	for _, line := range lines[:end] {
		line = strings.TrimRight(line, "\r")
		key, val, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found {
			continue
		}
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		switch strings.TrimSpace(key) {
		case "name":
			fm.Name = val
		case "type":
			fm.Type = val
		case "description":
			fm.Description = val
		case "modified":
			fm.Modified = parseTime(val)
		}
	}
	return fm, true, nil
}

func parseTime(v string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t
		}
	}
	return time.Time{}
}

// ScanMemory reads one auto-memory directory: the index, every note
// (frontmatter, else mtime), and the ones newer than since, feedback
// first. A missing directory is "no notes yet", not an error; a note
// whose frontmatter is malformed is reported in Warnings and listed with
// its Error set, never dropped.
func ScanMemory(dir string, since time.Time) Memory {
	m := Memory{Dir: dir, Index: []IndexEntry{}, Notes: []Note{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return m
	}
	if raw, err := os.ReadFile(filepath.Join(dir, IndexFile)); err == nil {
		m.Index = ParseIndex(string(raw))
	}
	indexed := map[string]IndexEntry{}
	for _, ie := range m.Index {
		indexed[ie.File] = ie
	}
	present := map[string]bool{}
	var all []Note
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || name == IndexFile || strings.HasPrefix(name, ".") {
			continue
		}
		present[name] = true
		n := Note{File: name, Name: strings.TrimSuffix(name, filepath.Ext(name))}
		if ie, ok := indexed[name]; ok {
			n.Indexed = true
			n.Description = ie.Hook
		}
		if fi, err := ent.Info(); err == nil {
			n.Modified, n.ModifiedBy = fi.ModTime(), "mtime"
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			n.Error = err.Error()
		} else if fm, _, err := ParseFrontmatter(string(raw)); err != nil {
			n.Error = err.Error()
		} else {
			if fm.Name != "" {
				n.Name = fm.Name
			}
			n.Type = fm.Type
			if fm.Description != "" {
				n.Description = fm.Description
			}
			if !fm.Modified.IsZero() {
				n.Modified, n.ModifiedBy = fm.Modified, "frontmatter"
			}
		}
		if n.Error != "" {
			m.Warnings = append(m.Warnings, name+": "+n.Error)
		}
		all = append(all, n)
	}
	m.Total = len(all)
	for _, ie := range m.Index {
		if !present[ie.File] {
			m.Warnings = append(m.Warnings, IndexFile+" lists "+ie.File+", which does not exist")
		}
	}
	for _, n := range all {
		if n.Error != "" || !n.Modified.Before(since) {
			m.Notes = append(m.Notes, n)
		}
	}
	sort.SliceStable(m.Notes, func(i, j int) bool {
		a, b := m.Notes[i], m.Notes[j]
		if (a.Type == TypeFeedback) != (b.Type == TypeFeedback) {
			return a.Type == TypeFeedback
		}
		if !a.Modified.Equal(b.Modified) {
			return a.Modified.After(b.Modified)
		}
		return a.File < b.File
	})
	return m
}

// --- LEARNINGS.md --------------------------------------------------------

var (
	entryHead = regexp.MustCompile(`^- \*\*(20\d\d-\d\d-\d\d) · (.*)$`)
	ticketRe  = regexp.MustCompile(`grove-\d+`)
)

// ParseEntries splits a LEARNINGS.md into entries: a top-level `- `
// bullet plus its two-space continuation lines, under the nearest `## `
// heading (the same shape scripts/log-append.py rotates). The preamble
// before the first heading is skipped. A headline that wraps onto a
// continuation line is read up to its closing `**`.
func ParseEntries(text string, skills []string) []Entry {
	var out []Entry
	section := ""
	var cur *Entry
	headOpen := false // the bold headline has not met its closing ** yet
	flush := func() {
		if cur != nil {
			cur.Text = strings.TrimSpace(cur.Text)
			cur.Fact = strings.TrimSpace(cur.Fact)
			cur.Tickets = uniq(ticketRe.FindAllString(cur.Text, -1))
			cur.Skills = mentionedSkills(cur.Text, skills)
			out = append(out, *cur)
			cur = nil
		}
		headOpen = false
	}
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "## "):
			flush()
			section = strings.TrimSpace(line[3:])
		case strings.HasPrefix(line, "- ") && section != "":
			flush()
			e := Entry{Section: section, Text: line}
			if m := entryHead.FindStringSubmatch(line); m != nil {
				e.Date = m[1]
				e.Fact, headOpen = cutHeadline(m[2])
			}
			cur = &e
		case cur != nil && strings.TrimSpace(line) == "":
			// blank lines inside an entry are ignored; the next line decides
		case cur != nil && strings.HasPrefix(line, "  "):
			cont := strings.TrimSpace(line)
			cur.Text += " " + cont
			if headOpen {
				var more string
				more, headOpen = cutHeadline(cont)
				cur.Fact += " " + more
			}
		default:
			flush()
		}
	}
	flush()
	return out
}

// cutHeadline returns the text before the closing `**` and whether the
// headline is still open (no `**` on this line).
func cutHeadline(s string) (string, bool) {
	if i := strings.Index(s, "**"); i >= 0 {
		return s[:i], false
	}
	return s, true
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// mentionedSkills returns the skill names that appear in text as whole
// words (names are kebab-case, so a hyphen on either side is not a
// boundary).
func mentionedSkills(text string, skills []string) []string {
	var out []string
	for _, s := range skills {
		if s != "" && regexp.MustCompile(`(^|[^A-Za-z0-9-])`+regexp.QuoteMeta(s)+`([^A-Za-z0-9-]|$)`).MatchString(text) {
			out = append(out, s)
		}
	}
	return out
}

// SkillNames lists the skills a repo keeps: the directories under
// .claude/skills/ that hold a SKILL.md, sorted.
func SkillNames(repoRoot string) []string {
	entries, err := os.ReadDir(filepath.Join(repoRoot, ".claude", "skills"))
	if err != nil {
		return nil
	}
	var out []string
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(repoRoot, ".claude", "skills", ent.Name(), "SKILL.md")); err == nil {
			out = append(out, ent.Name())
		}
	}
	sort.Strings(out)
	return out
}

// Candidates finds dated entries that name a skill whose text cites none
// of the entry's ticket ids, its date, or its headline. skillText maps a
// skill name to its SKILL.md.
func Candidates(entries []Entry, skillText map[string]string) []Candidate {
	out := []Candidate{}
	for _, e := range entries {
		if e.Date == "" {
			continue
		}
		for _, s := range e.Skills {
			text, ok := skillText[s]
			if !ok {
				continue
			}
			if cites(text, e) {
				continue
			}
			why := "names " + s + " but the skill cites neither " + e.Date
			if len(e.Tickets) > 0 {
				why += " nor " + strings.Join(e.Tickets, "/")
			}
			out = append(out, Candidate{Skill: s, Date: e.Date, Fact: e.Fact, Why: why + " nor the headline"})
		}
	}
	return out
}

func cites(skill string, e Entry) bool {
	if strings.Contains(skill, e.Date) {
		return true
	}
	for _, t := range e.Tickets {
		if strings.Contains(skill, t) {
			return true
		}
	}
	return e.Fact != "" && strings.Contains(norm(skill), norm(e.Fact))
}

func norm(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer("`", "", "*", "", "\n", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// --- per repo ------------------------------------------------------------

// RepoInput is what main.go resolves for one repo before the package
// reads anything: the repo's root and its memory surface (from
// connections.ResolveMemory).
type RepoInput struct {
	Name           string
	Root           string
	MemoryDir      string
	MemorySource   string
	MemoryDisabled string
}

// Build reads one repo: its memory dir, its LEARNINGS.md (when it keeps
// one) and its skills, and derives the entries newer than since plus
// the promotion candidates over every entry in the file.
func Build(in RepoInput, since time.Time) RepoReport {
	r := RepoReport{
		Repo:       in.Name,
		Memory:     ScanMemory(in.MemoryDir, since),
		Entries:    []Entry{},
		Candidates: []Candidate{},
		Skills:     []string{},
	}
	r.Memory.Source, r.Memory.Disabled = in.MemorySource, in.MemoryDisabled
	if skills := SkillNames(in.Root); skills != nil {
		r.Skills = skills
	}
	path := filepath.Join(in.Root, "LEARNINGS.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		return r
	}
	r.LearningsFile = path
	all := ParseEntries(string(raw), r.Skills)
	sinceDay := since.UTC().Format("2006-01-02")
	for _, e := range all {
		if e.Date != "" && e.Date >= sinceDay {
			r.Entries = append(r.Entries, e)
		}
	}
	sort.SliceStable(r.Entries, func(i, j int) bool { return r.Entries[i].Date > r.Entries[j].Date })
	skillText := map[string]string{}
	for _, s := range r.Skills {
		if b, err := os.ReadFile(filepath.Join(in.Root, ".claude", "skills", s, "SKILL.md")); err == nil {
			skillText[s] = string(b)
		}
	}
	r.Candidates = Candidates(all, skillText)
	return r
}
