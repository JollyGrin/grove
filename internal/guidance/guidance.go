// Package guidance guards the size of the repo's always-appended docs
// (grove-275). TASKS.md and LEARNINGS.md are HEADS: a small append target
// whose older rows/entries live in docs/archive/<FILE>-YYYY-MM.md. Each
// head declares its own cap in a marker line,
//
//	<!-- head-cap: 16384 -->
//
// which scripts/log-append.py reads to decide when to archive and which
// TestRepoHeadsUnderCap reads to fail `go test ./...` when a PR let a head
// grow past it. One marker, two readers, no constant to drift.
package guidance

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
)

var capRe = regexp.MustCompile(`<!--\s*head-cap:\s*(\d+)`)

// Cap returns the byte cap declared by a head's marker line.
func Cap(content []byte) (int, error) {
	m := capRe.FindSubmatch(content)
	if m == nil {
		return 0, fmt.Errorf("no `<!-- head-cap: N -->` marker")
	}
	return strconv.Atoi(string(m[1]))
}

// Check reads a head and reports whether it fits its declared cap.
func Check(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	c, err := Cap(b)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if len(b) > c {
		return fmt.Errorf("%s is %d bytes, over its head-cap of %d: run scripts/log-append.py rotate "+
			"(archives the oldest rows into docs/archive/, never deletes)", path, len(b), c)
	}
	return nil
}
