package sub

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Preamble is the default system prompt: terse, structured, cite
// locations verbatim, and never invent an answer that isn't in the input.
const Preamble = `You are a terse technical summariser for another engineer's agent. Output structured bullets only. No preamble, no closing remarks. Cite locations as path:line using the line numbers given in the input, verbatim. If the answer is not in the input, say NOT FOUND.`

// Input is one file (or stdin) handed to Assemble.
type Input struct {
	Path    string
	Content []byte
}

// ErrTooLarge is returned by Assemble when the assembled inputs exceed
// maxChars.
type ErrTooLarge struct {
	Have, Max int
}

func (e ErrTooLarge) Error() string {
	return fmt.Sprintf("input too large: %d chars (max %d)", e.Have, e.Max)
}

// Assemble builds the user message: the prompt, followed by one <file>
// block per input with line numbers so the model can cite path:line
// exactly as given.
func Assemble(prompt string, inputs []Input, maxChars int) (string, error) {
	total := 0
	for _, in := range inputs {
		total += len(in.Content)
	}
	if total > maxChars {
		return "", ErrTooLarge{Have: total, Max: maxChars}
	}

	var b strings.Builder
	b.WriteString(prompt)
	for _, in := range inputs {
		lines := strings.Split(string(in.Content), "\n")
		// A trailing newline in the file produces one trailing empty
		// "line" from strings.Split — drop it so line counts match what a
		// reader (or `wc -l`) would call the file's line count.
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		fmt.Fprintf(&b, "\n<file path=%q lines=\"%d\">\n", in.Path, len(lines))
		for i, l := range lines {
			fmt.Fprintf(&b, "%5d\t%s\n", i+1, l)
		}
		b.WriteString("</file>\n")
	}
	return b.String(), nil
}

// skipDirs are never walked by Collect: vcs metadata, dependency trees,
// build output, and grove's own worktree scratch dirs.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true, ".worktrees": true,
}

// allowExt is the extension allow-list a walked directory's files must
// match; explicitly named files (not from a directory walk) are always
// collected regardless of extension.
var allowExt = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	".json": true, ".yaml": true, ".yml": true, ".md": true, ".txt": true,
	".sh": true, ".toml": true, ".sql": true, ".py": true, ".rs": true,
	".html": true, ".css": true, ".mod": true, ".sum": true,
}

const maxFileSize = 1 << 20 // 1 MiB

// Collect gathers Input for each path: files are read as-is; directories
// are walked, skipping vendored/vcs dirs, oversized files, binary files
// (a NUL in the first 8 KiB), and extensions outside allowExt. Order is
// stable (lexically sorted) so a rerun assembles byte-identical prompts.
func Collect(paths []string) ([]Input, error) {
	// fromWalk tracks which files came from a directory walk — only those
	// are subject to the binary/size/extension filters; an explicitly
	// named file is collected as-is.
	fromWalk := map[string]bool{}
	var files []string
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !st.IsDir() {
			files = append(files, p)
			continue
		}
		err = filepath.Walk(p, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if skipDirs[info.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			if info.Size() > maxFileSize {
				return nil
			}
			if !allowExt[strings.ToLower(filepath.Ext(path))] {
				return nil
			}
			fromWalk[path] = true
			files = append(files, path)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(files)

	var inputs []Input
	for _, f := range files {
		content, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		if fromWalk[f] && isBinary(content) {
			continue
		}
		inputs = append(inputs, Input{Path: f, Content: content})
	}
	return inputs, nil
}

func isBinary(content []byte) bool {
	head := content
	if len(head) > 8192 {
		head = head[:8192]
	}
	for _, b := range head {
		if b == 0 {
			return true
		}
	}
	return false
}
