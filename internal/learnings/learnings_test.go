package learnings_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/learnings"
)

var since = time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestScanMemoryFixtures walks the fixture shapes the ticket names: an
// absent dir, an empty dir, an index with no notes, mixed types, and a
// note whose frontmatter never closes — reported, never fatal.
func TestScanMemoryFixtures(t *testing.T) {
	t.Run("absent dir is no notes", func(t *testing.T) {
		m := learnings.ScanMemory(filepath.Join(t.TempDir(), "nope"), since)
		if m.Total != 0 || len(m.Notes) != 0 || len(m.Index) != 0 || len(m.Warnings) != 0 {
			t.Errorf("absent dir: %+v", m)
		}
		if m.Notes == nil || m.Index == nil {
			t.Error("notes/index must be empty arrays, not null, for the --json contract")
		}
	})
	t.Run("empty dir", func(t *testing.T) {
		m := learnings.ScanMemory(t.TempDir(), since)
		if m.Total != 0 || len(m.Notes) != 0 {
			t.Errorf("empty dir: %+v", m)
		}
	})
	t.Run("index only: dangling lines warn, index is not a note", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "MEMORY.md", "- [Gone](gone.md) — a hook\nprose line\n- [Also](also.md)\n")
		m := learnings.ScanMemory(dir, since)
		if m.Total != 0 || len(m.Notes) != 0 {
			t.Errorf("index-only: total %d notes %d, want 0/0", m.Total, len(m.Notes))
		}
		if len(m.Index) != 2 || m.Index[0] != (learnings.IndexEntry{Title: "Gone", File: "gone.md", Hook: "a hook"}) || m.Index[1].File != "also.md" || m.Index[1].Hook != "" {
			t.Errorf("index = %+v", m.Index)
		}
		if len(m.Warnings) != 2 || !strings.Contains(m.Warnings[0], "gone.md") {
			t.Errorf("warnings = %v, want one per dangling index line", m.Warnings)
		}
	})
	t.Run("mixed types: feedback first, old notes counted but not listed", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "MEMORY.md", "- [Watch](gv-watch.md) — the detector\n- [Phone](phone.md) — android\n")
		write(t, dir, "gv-watch.md", "---\nname: gv-watch-usage\ndescription: How gv watch behaves\nmetadata:\n  type: reference\nmodified: 2026-10-01T10:00:00Z\n---\n\nbody\n")
		write(t, dir, "fix.md", "---\nname: no-routing\ndescription: never route unasked\ntype: feedback\nmodified: 2026-09-30\n---\n\nbody\n")
		write(t, dir, "phone.md", "---\ntype: user\nmodified: 2020-01-01\n---\nold\n")
		write(t, dir, "bare.md", "no frontmatter at all\n")
		write(t, dir, ".hidden.md", "---\ntype: feedback\n---\n")
		if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		if err := os.Chtimes(filepath.Join(dir, "bare.md"), now, now); err != nil {
			t.Fatal(err)
		}
		m := learnings.ScanMemory(dir, since)
		if m.Total != 4 {
			t.Errorf("total = %d, want 4 (index, dotfile and subdir excluded)", m.Total)
		}
		got := make([]string, 0, len(m.Notes))
		for _, n := range m.Notes {
			got = append(got, n.File)
		}
		if strings.Join(got, " ") != "fix.md bare.md gv-watch.md" {
			t.Fatalf("notes = %v, want feedback first then newest first, phone.md (2020) excluded", got)
		}
		fix, bare, watch := m.Notes[0], m.Notes[1], m.Notes[2]
		if fix.Type != "feedback" || fix.Name != "no-routing" || fix.Description != "never route unasked" || fix.ModifiedBy != "frontmatter" || fix.Indexed {
			t.Errorf("flat-type note = %+v", fix)
		}
		if watch.Type != "reference" || watch.Name != "gv-watch-usage" || !watch.Indexed || watch.Modified != time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC) {
			t.Errorf("nested metadata.type note = %+v", watch)
		}
		if bare.Type != "" || bare.Name != "bare" || bare.ModifiedBy != "mtime" || bare.Error != "" {
			t.Errorf("frontmatter-less note = %+v", bare)
		}
		if len(m.Warnings) != 0 {
			t.Errorf("warnings = %v, want none", m.Warnings)
		}
	})
	t.Run("malformed frontmatter is reported, not fatal", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "broken.md", "---\nname: broken\ntype: feedback\n\nno closing fence\n")
		write(t, dir, "fine.md", "---\ntype: project\nmodified: 2026-10-05\n---\nok\n")
		m := learnings.ScanMemory(dir, since)
		if m.Total != 2 || len(m.Notes) != 2 {
			t.Fatalf("total %d notes %d, want 2/2 (the broken note is listed, not dropped): %+v", m.Total, len(m.Notes), m.Notes)
		}
		var broken *learnings.Note
		for i := range m.Notes {
			if m.Notes[i].File == "broken.md" {
				broken = &m.Notes[i]
			}
		}
		if broken == nil || !strings.Contains(broken.Error, "never closed") || broken.Type != "" {
			t.Errorf("broken note = %+v, want an error and no fields read from the open block", broken)
		}
		if len(m.Warnings) != 1 || !strings.Contains(m.Warnings[0], "broken.md") {
			t.Errorf("warnings = %v", m.Warnings)
		}
	})
}

func TestParseFrontmatter(t *testing.T) {
	fm, ok, err := learnings.ParseFrontmatter("---\nname: x\ndescription: \"quoted, with: colon\"\nmetadata:\n  type: feedback\nmodified: '2026-10-07T11:41:00Z'\n---\nbody")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if fm.Name != "x" || fm.Type != "feedback" || fm.Description != "quoted, with: colon" || fm.Modified.Year() != 2026 {
		t.Errorf("fm = %+v", fm)
	}
	if _, ok, _ := learnings.ParseFrontmatter("plain\n"); ok {
		t.Error("plain text reported as having frontmatter")
	}
	if _, _, err := learnings.ParseFrontmatter("---\nname: x\n"); err == nil {
		t.Error("unterminated block did not error")
	}
}

func TestParseSince(t *testing.T) {
	for in, want := range map[string]time.Duration{"14d": 14 * 24 * time.Hour, "2w": 14 * 24 * time.Hour, "48h": 48 * time.Hour, "0d": 0} {
		got, err := learnings.ParseSince(in)
		if err != nil || got != want {
			t.Errorf("ParseSince(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "soon", "-3d", "3 days"} {
		if _, err := learnings.ParseSince(bad); err == nil {
			t.Errorf("ParseSince(%q) accepted", bad)
		}
	}
}

const learningsFixture = `# Grove — learnings

> preamble with a bullet that is not an entry:
> - not an entry

## Claude Code behavior

- **2026-10-07 · Auto memory is keyed on the git REPO, not the cwd, so
  every worktree shares it.** Found in grove-437. Rule distilled into
  ` + "`.claude/skills/claude-code-facts`" + `.

- **2026-09-01 · Old fact.** Mentions the tmux-discipline skill (grove-7).

## Go / CLI

- **2026-10-01 · Fresh fact.** No skill named; grove-451.
- seeded entry without a date, mentions ticket-writing.
`

func TestParseEntries(t *testing.T) {
	skills := []string{"claude-code-facts", "ticket-writing", "tmux-discipline"}
	es := learnings.ParseEntries(learningsFixture, skills)
	if len(es) != 4 {
		t.Fatalf("got %d entries, want 4: %+v", len(es), es)
	}
	wrapped := es[0]
	if wrapped.Date != "2026-10-07" || wrapped.Section != "Claude Code behavior" {
		t.Errorf("entry 0 = %+v", wrapped)
	}
	if wrapped.Fact != "Auto memory is keyed on the git REPO, not the cwd, so every worktree shares it." {
		t.Errorf("wrapped headline = %q", wrapped.Fact)
	}
	if strings.Join(wrapped.Tickets, ",") != "grove-437" || strings.Join(wrapped.Skills, ",") != "claude-code-facts" {
		t.Errorf("tickets %v skills %v", wrapped.Tickets, wrapped.Skills)
	}
	if !strings.Contains(wrapped.Text, "Rule distilled into") || strings.Contains(wrapped.Text, "\n") {
		t.Errorf("text not joined: %q", wrapped.Text)
	}
	if es[1].Date != "2026-09-01" || es[1].Fact != "Old fact." || strings.Join(es[1].Skills, ",") != "tmux-discipline" {
		t.Errorf("entry 1 = %+v", es[1])
	}
	if es[2].Section != "Go / CLI" || es[2].Date != "2026-10-01" || len(es[2].Skills) != 0 || strings.Join(es[2].Tickets, ",") != "grove-451" {
		t.Errorf("entry 2 = %+v", es[2])
	}
	if es[3].Date != "" || es[3].Fact != "" || strings.Join(es[3].Skills, ",") != "ticket-writing" {
		t.Errorf("undated entry = %+v", es[3])
	}
}

// TestCandidates: an entry naming a skill is a promotion candidate only
// when the skill cites none of its ticket, date or headline; undated
// entries never are.
func TestCandidates(t *testing.T) {
	skills := []string{"claude-code-facts", "ticket-writing", "tmux-discipline"}
	es := learnings.ParseEntries(learningsFixture, skills)
	got := learnings.Candidates(es, map[string]string{
		"claude-code-facts": "# facts\n\nnothing about memory here\n",
		"tmux-discipline":   "# tmux\n\nThe grove-7 crash: never kill the real server.\n",
		"ticket-writing":    "# tickets\n",
	})
	if len(got) != 1 || got[0].Skill != "claude-code-facts" || got[0].Date != "2026-10-07" {
		t.Fatalf("candidates = %+v, want only the uncited claude-code-facts entry", got)
	}
	if !strings.Contains(got[0].Why, "grove-437") || !strings.Contains(got[0].Why, "2026-10-07") {
		t.Errorf("why = %q", got[0].Why)
	}
	// Each citation form on its own silences the candidate.
	for name, text := range map[string]string{
		"ticket":   "see grove-437",
		"date":     "verified 2026-10-07",
		"headline": "Rule: *auto memory is keyed on the git repo, not the cwd, so every worktree shares it.*",
	} {
		if c := learnings.Candidates(es[:1], map[string]string{"claude-code-facts": text}); len(c) != 0 {
			t.Errorf("citation by %s still a candidate: %+v", name, c)
		}
	}
	// A skill the repo does not keep is never a candidate target.
	if c := learnings.Candidates(es[:1], map[string]string{}); len(c) != 0 {
		t.Errorf("unknown skill produced %+v", c)
	}
}

// TestBuild runs the per-repo assembly on a fixture repo: skills are
// discovered from .claude/skills/, entries filtered by --since (by day),
// candidates computed over the whole file, and a repo with no
// LEARNINGS.md still reports its memory.
func TestBuild(t *testing.T) {
	root := t.TempDir()
	mem := filepath.Join(root, "mem")
	write(t, root, "LEARNINGS.md", learningsFixture)
	write(t, root, ".claude/skills/claude-code-facts/SKILL.md", "# facts\n")
	write(t, root, ".claude/skills/tmux-discipline/SKILL.md", "grove-7\n")
	write(t, root, ".claude/skills/not-a-skill/README.md", "no SKILL.md here\n")
	write(t, mem, "n.md", "---\ntype: feedback\nmodified: 2026-10-06\n---\n")
	r := learnings.Build(learnings.RepoInput{Name: "demo", Root: root, MemoryDir: mem, MemorySource: "/x/settings.json"}, since)
	if r.Repo != "demo" || r.Memory.Dir != mem || r.Memory.Source != "/x/settings.json" || r.Memory.Total != 1 {
		t.Errorf("report head = %+v", r)
	}
	if strings.Join(r.Skills, ",") != "claude-code-facts,tmux-discipline" {
		t.Errorf("skills = %v", r.Skills)
	}
	if r.LearningsFile != filepath.Join(root, "LEARNINGS.md") {
		t.Errorf("learnings_file = %q", r.LearningsFile)
	}
	dates := make([]string, 0, len(r.Entries))
	for _, e := range r.Entries {
		dates = append(dates, e.Date)
	}
	if strings.Join(dates, ",") != "2026-10-07,2026-10-01" {
		t.Errorf("entries since %s = %v", since.Format("2006-01-02"), dates)
	}
	if len(r.Candidates) != 1 || r.Candidates[0].Skill != "claude-code-facts" {
		t.Errorf("candidates = %+v", r.Candidates)
	}

	bare := learnings.Build(learnings.RepoInput{Name: "bare", Root: t.TempDir(), MemoryDir: mem}, since)
	if bare.LearningsFile != "" || len(bare.Entries) != 0 || bare.Entries == nil || bare.Candidates == nil || bare.Skills == nil || bare.Memory.Total != 1 {
		t.Errorf("repo without LEARNINGS.md = %+v", bare)
	}
}
