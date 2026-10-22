package guidance

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCap(t *testing.T) {
	if _, err := Cap([]byte("# x\n\nno marker\n")); err == nil {
		t.Error("missing marker must be an error, not a silent zero")
	}
	c, err := Cap([]byte("# x\n> <!-- head-cap: 16384 -->\n"))
	if err != nil || c != 16384 {
		t.Errorf("Cap = %d, %v; want 16384", c, err)
	}
}

func TestCheck(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "TASKS.md")
	os.WriteFile(p, []byte("<!-- head-cap: 40 -->\n- [x] short\n"), 0o644)
	if err := Check(p); err != nil {
		t.Errorf("under cap: %v", err)
	}
	os.WriteFile(p, []byte("<!-- head-cap: 40 -->\n- [x] "+strings.Repeat("x", 60)+"\n"), 0o644)
	if err := Check(p); err == nil || !strings.Contains(err.Error(), "over its head-cap") {
		t.Errorf("over cap must fail with the rotate hint, got %v", err)
	}
	os.WriteFile(p, []byte("- [x] no marker\n"), 0o644)
	if err := Check(p); err == nil {
		t.Error("a head without a marker must fail — the cap is the contract")
	}
}

// TestRepoHeadsUnderCap is the guardrail: the live TASKS.md and LEARNINGS.md
// heads must fit the cap they declare. A PR that appends a row and skips
// the archive rotation fails here instead of quietly growing the heads
// back to the 80k they were before grove-275.
func TestRepoHeadsUnderCap(t *testing.T) {
	for _, name := range []string{"TASKS.md", "LEARNINGS.md"} {
		if err := Check(filepath.Join("..", "..", name)); err != nil {
			t.Error(err)
		}
	}
}

// TestLogAppendRotates runs scripts/log-append.py against a scratch copy:
// the append lands at the top of the right section without the caller
// reading the file, an over-cap head is brought under its cap, and every
// row/entry that leaves a head is found again in docs/archive/ — moved,
// never deleted.
func TestLogAppendRotates(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	script, _ := filepath.Abs(filepath.Join("..", "..", "scripts", "log-append.py"))
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "docs", "archive"), 0o755)

	tasks := "# board\n\n> <!-- head-cap: 700 -->\n\n## Now\n\n" +
		"- [x] newest (grove-9, 2026-09-10): " + strings.Repeat("n", 120) + "\n" +
		"- [x] middle (grove-8, 2026-09-02): " + strings.Repeat("m", 120) + "\n" +
		"- [x] oldest (grove-7, 2026-08-30): " + strings.Repeat("o", 120) + "\n" +
		"      continuation line of the oldest row\n" +
		"\n## Open\n\nkeep this paragraph.\n"
	learn := "# learnings\n\n> <!-- head-cap: 600 -->\n\n## Go / CLI\n\n" +
		"- **2026-09-09 · new go fact** — " + strings.Repeat("g", 100) + "\n" +
		"- **2026-08-20 · old go fact** — " + strings.Repeat("h", 100) + "\n" +
		"  its continuation\n" +
		"\n## Field notes\n\n" +
		"- **2026-09-01 · field fact** — " + strings.Repeat("f", 100) + "\n"
	os.WriteFile(filepath.Join(root, "TASKS.md"), []byte(tasks), 0o644)
	os.WriteFile(filepath.Join(root, "LEARNINGS.md"), []byte(learn), 0o644)

	run := func(stdin string, args ...string) string {
		cmd := exec.Command(py, append([]string{script}, args...)...)
		cmd.Env = append(os.Environ(), "LOG_ROOT="+root)
		cmd.Stdin = strings.NewReader(stdin)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out.String())
		}
		return out.String()
	}

	run("- [x] added (grove-10, 2026-09-15): "+strings.Repeat("a", 120)+"\n", "tasks")
	run("- **2026-09-15 · appended fact** — "+strings.Repeat("z", 100)+"\n", "learnings", "--section", "Go / CLI")

	got, _ := os.ReadFile(filepath.Join(root, "TASKS.md"))
	if err := Check(filepath.Join(root, "TASKS.md")); err != nil {
		t.Error(err)
	}
	if !regexp.MustCompile(`## Now\n\n- \[x\] added`).Match(got) {
		t.Errorf("appended row must be first under §Now:\n%s", got)
	}
	if !bytes.Contains(got, []byte("## Open\n\nkeep this paragraph.")) {
		t.Errorf("§Open must survive rotation:\n%s", got)
	}
	for _, gone := range []string{"oldest (grove-7", "continuation line of the oldest row"} {
		if bytes.Contains(got, []byte(gone)) {
			t.Errorf("oldest row should have been archived out of the head:\n%s", got)
		}
	}
	arch, err := os.ReadFile(filepath.Join(root, "docs", "archive", "TASKS-2026-08.md"))
	if err != nil {
		t.Fatalf("archived row must land in its own month's file: %v", err)
	}
	if !bytes.Contains(arch, []byte("oldest (grove-7")) || !bytes.Contains(arch, []byte("continuation line of the oldest row")) {
		t.Errorf("archive lost the row or its continuation:\n%s", arch)
	}

	got, _ = os.ReadFile(filepath.Join(root, "LEARNINGS.md"))
	if err := Check(filepath.Join(root, "LEARNINGS.md")); err != nil {
		t.Error(err)
	}
	if !regexp.MustCompile(`## Go / CLI\n\n- \*\*2026-09-15 · appended fact`).Match(got) {
		t.Errorf("appended entry must be first in its section:\n%s", got)
	}
	if bytes.Contains(got, []byte("old go fact")) {
		t.Errorf("the oldest entry (2026-08-20) should have been archived first:\n%s", got)
	}
	if !bytes.Contains(got, []byte("field fact")) {
		t.Errorf("a newer entry in another section must not be archived before an older one:\n%s", got)
	}
	arch, err = os.ReadFile(filepath.Join(root, "docs", "archive", "LEARNINGS-2026-08.md"))
	if err != nil {
		t.Fatalf("archived entry must land in its month's file: %v", err)
	}
	if !regexp.MustCompile(`## Go / CLI\n\n- \*\*2026-08-20 · old go fact`).Match(arch) || !bytes.Contains(arch, []byte("its continuation")) {
		t.Errorf("archive must keep the entry under its section with its continuation:\n%s", arch)
	}

	// Idempotent: nothing left to move, nothing changes.
	before, _ := os.ReadFile(filepath.Join(root, "TASKS.md"))
	run("", "rotate")
	after, _ := os.ReadFile(filepath.Join(root, "TASKS.md"))
	if !bytes.Equal(before, after) {
		t.Error("rotate on an under-cap head must be a no-op")
	}
}
