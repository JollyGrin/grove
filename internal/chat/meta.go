package chat

// grove-315: harness wrappers.
//
// Claude Code writes its own chrome into the transcript as `user` lines — a
// slash command's echo, its local stdout, a `!` shell escape, a background
// task's completion notice — and only the caveat that precedes a local
// command is flagged isMeta. Shown verbatim, each one reads as the operator
// talking (a raw-tag bubble in the chat view) and, when it comes first, as
// the chat's TITLE: half the rows of `gv chat ls` were named
// `<local-command-caveat>Caveat: The messages below…`.
//
// The cleaning lives here, not in internal/transcript, because that package
// stays byte-comparable with ovs upstream (docs/seed-manifest.md).

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"
)

// Tools a meta entry names — which wrapper the line was.
const (
	MetaCommand          = "command"           // a slash command: `/model opus`
	MetaTaskNotification = "task-notification" // a background agent/Bash finished
	MetaBash             = "bash"              // a `!` shell escape's command line
	MetaBashOutput       = "bash-output"       // that escape's stdout/stderr
	MetaLocalStdout      = "local-stdout"      // a local command's output
	// MetaInterrupt (grove-334) is Claude Code's own notice that the
	// operator stopped the turn — `[Request interrupted by user]`, `… for
	// tool use]`. It is written as a user line, but it is the harness
	// speaking, and it ENDS the turn: a client reads it as "stopped", never
	// as the operator's message or as a turn still going.
	MetaInterrupt = "interrupt"
)

var (
	// A system reminder is injected context, never the operator's words.
	reSystemReminder = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)
	// A paste wrapper keeps its inner text. Seen both plain and with
	// backslash-escaped brackets (`<\pasted_content …>…<\/pasted_content>`).
	rePasted = regexp.MustCompile(`<\\?/?\\?/?pasted_content\b[^>]*>`)
)

// tagText is the inner text of the first <tag>…</tag> in s, trimmed. An
// unclosed tag (a label truncated mid-wrapper) runs to the end of s.
func tagText(s, tag string) (string, bool) {
	open := "<" + tag + ">"
	i := strings.Index(s, open)
	if i < 0 {
		return "", false
	}
	rest := s[i+len(open):]
	if j := strings.Index(rest, "</"+tag+">"); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest), true
}

// stripWrappers removes the wrappers that can sit INSIDE a real operator
// message: system reminders (dropped whole) and paste wrappers (dropped,
// inner text kept).
func stripWrappers(s string) string {
	s = reSystemReminder.ReplaceAllString(s, "")
	s = rePasted.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// classify reads one user-authored text. A text that is ONLY a harness
// wrapper returns meta=true, the wrapper's tool name, and its cleaned inner
// text; anything else is the operator's own prose, returned with in-message
// wrappers stripped. A local-command caveat returns meta=true with an empty
// tool: pure chrome, nothing to show.
func classify(s string) (tool, text string, meta bool) {
	s = stripWrappers(s)
	switch {
	case strings.HasPrefix(s, "<local-command-caveat>"):
		return "", "", true
	case strings.HasPrefix(s, "<command-name>"), strings.HasPrefix(s, "<command-message>"):
		name, _ := tagText(s, "command-name")
		if name == "" {
			if msg, _ := tagText(s, "command-message"); msg != "" {
				name = "/" + strings.TrimPrefix(msg, "/")
			}
		}
		args, _ := tagText(s, "command-args")
		return MetaCommand, strings.TrimSpace(name + " " + args), true
	case strings.HasPrefix(s, "<task-notification>"):
		text, ok := tagText(s, "summary")
		if !ok {
			text, _ = tagText(s, "status")
		}
		if result, ok := tagText(s, "result"); ok && result != "" {
			text = strings.TrimSpace(text + "\n\n" + result)
		}
		return MetaTaskNotification, text, true
	case strings.HasPrefix(s, "<bash-input>"):
		text, _ := tagText(s, "bash-input")
		return MetaBash, text, true
	case strings.HasPrefix(s, "<bash-stdout>"), strings.HasPrefix(s, "<bash-stderr>"):
		out, _ := tagText(s, "bash-stdout")
		errText, _ := tagText(s, "bash-stderr")
		return MetaBashOutput, strings.TrimSpace(out + "\n" + errText), true
	case isInterrupt(s):
		return MetaInterrupt, strings.TrimSuffix(strings.TrimPrefix(s, "["), "]"), true
	case strings.HasPrefix(s, "<local-command-stdout>"), strings.HasPrefix(s, "<local-command-stderr>"):
		out, _ := tagText(s, "local-command-stdout")
		errText, _ := tagText(s, "local-command-stderr")
		return MetaLocalStdout, strings.TrimSpace(out + "\n" + errText), true
	}
	return "", s, false
}

// isInterrupt is Claude Code's interrupt notice in any of its variants —
// `[Request interrupted by user]`, `[Request interrupted by user for tool
// use]` — and only when it is the whole text, so prose that merely quotes
// one stays the operator's.
func isInterrupt(s string) bool {
	return strings.HasPrefix(s, "[Request interrupted") && strings.HasSuffix(s, "]") && !strings.Contains(s, "\n")
}

// labelMax matches transcript.FirstPrompt's 80-rune list label.
const labelMax = 80

// CleanLabel turns a transcript's first prompt into a human title: wrappers
// stripped, a slash command titled by the command itself (`/model`), and a
// pure-chrome prompt (a caveat, a stdout echo) titled "" so the caller can
// look further. Pure; the `gv chat ls` contract keeps `label` a string.
func CleanLabel(s string) string {
	tool, text, meta := classify(s)
	if meta && tool != MetaCommand {
		return ""
	}
	return shortLabel(text)
}

// shortLabel is transcript's truncate: newlines collapsed, 80 runes.
func shortLabel(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	r := []rune(s)
	if len(r) <= labelMax {
		return s
	}
	return string(r[:labelMax-3]) + "..."
}

// labelScanLines bounds how far into a transcript Label looks for the first
// real prompt. A command-first chat reaches its prose within a handful of
// user lines; a chat with none in 400 lines is titled by what it has.
const labelScanLines = 400

// Label is the title for the transcript at path: the first user prompt
// that is the operator's own words, cleaned. Falls back to the first slash
// command (`/model`), then the first `!` shell escape (`$ tmux kill-pane`)
// for a chat that never said anything else, then to CleanLabel(fallback) —
// transcript.Session.FirstPrompt — when the file cannot be read.
func Label(path, fallback string) string {
	f, err := os.Open(path)
	if err != nil {
		return CleanLabel(fallback)
	}
	defer f.Close()
	if l, ok := labelFrom(f); ok {
		return l
	}
	return CleanLabel(fallback)
}

// labelFrom scans transcript lines for Label; ok=false when nothing in the
// scanned span could title the chat. Prose wins; a chat of pure chrome is
// titled by its first slash command, then by its first `!` shell escape
// (grove-341) — otherwise those chats title as "" and clients show the raw
// session id.
func labelFrom(r io.Reader) (string, bool) {
	br := bufio.NewReader(r)
	command := ""
	bash := ""
	for range labelScanLines {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			for _, s := range userTexts(line) {
				tool, text, meta := classify(s)
				switch {
				case meta:
					if tool == MetaCommand && command == "" {
						command = text
					}
					if tool == MetaBash && bash == "" {
						bash = text
					}
				case text == "" || isBoilerplate(text):
				default:
					return shortLabel(text), true
				}
			}
		}
		if err != nil {
			break
		}
	}
	if command != "" {
		return shortLabel(command), true
	}
	if bash != "" {
		return shortLabel("$ " + bash), true
	}
	return "", false
}

// isBoilerplate mirrors transcript's skip list: harness notices that are
// user lines but never a chat's subject.
func isBoilerplate(s string) bool {
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "[request interrupted") || strings.HasPrefix(lower, "resume")
}

// userTexts is the operator-authored text of one transcript line: a user
// line's string content or its text blocks. Meta and sidechain lines, and
// tool_result blocks, contribute nothing.
func userTexts(line []byte) []string {
	var raw rawLine
	if err := json.Unmarshal(trimLine(line), &raw); err != nil {
		return nil
	}
	if raw.Type != RoleUser || raw.IsMeta || raw.IsSidechain {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw.Message.Content, &s); err == nil {
		return []string{s}
	}
	var blocks []rawBlock
	if err := json.Unmarshal(raw.Message.Content, &blocks); err != nil {
		return nil
	}
	var out []string
	for _, b := range blocks {
		if b.Type == "text" {
			out = append(out, b.Text)
		}
	}
	return out
}
