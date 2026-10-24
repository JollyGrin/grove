//go:build ignore

// gen.go writes archive.tar.gz: every distinct orchestrator/CLAUDE.md body
// in this repository's history, plus the working-tree one, each stored as
// `<SeedStamp>.md`. Run via `go generate ./orchestrator/seeds` from any cwd
// inside the module. Output is deterministic (sorted entries, zero
// timestamps), so an unchanged history regenerates byte-identical bytes.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JollyGrin/grove/internal/bootstrap"
)

const seedPath = "orchestrator/CLAUDE.md"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := git("", "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("not inside the grove repository: %w", err)
	}
	root = strings.TrimSpace(root)
	log, err := git(root, "log", "--format=%H", "--", seedPath)
	if err != nil {
		return err
	}
	bodies := map[string]string{} // stamp → unstamped body
	add := func(content string) {
		body := bootstrap.SeedBody(content)
		bodies[bootstrap.SeedStamp(body)] = body
	}
	for _, sha := range strings.Fields(log) {
		content, err := git(root, "show", sha+":"+seedPath)
		if err != nil {
			return fmt.Errorf("%s: %w", sha, err)
		}
		add(content)
	}
	current, err := os.ReadFile(filepath.Join(root, seedPath))
	if err != nil {
		return err
	}
	add(string(current))

	stamps := make([]string, 0, len(bodies))
	for s := range bodies {
		stamps = append(stamps, s)
	}
	sort.Strings(stamps)

	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	tw := tar.NewWriter(zw)
	for _, s := range stamps {
		body := []byte(bodies[s])
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: s + ".md", Mode: 0o644, Size: int64(len(body)), Format: tar.FormatUSTAR,
		}); err != nil {
			return err
		}
		if _, err := tw.Write(body); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	out := filepath.Join(root, "orchestrator", "seeds", "archive.tar.gz")
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s: %d seeds, %d bytes\n", out, len(stamps), buf.Len())
	return nil
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}
