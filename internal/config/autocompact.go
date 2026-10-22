package config

// grove-440: compaction over discard. `claude --autocompact <auto|tokens>`
// (Claude Code `--help`: "Auto-compact window size (auto, or 100k–1M
// tokens)") caps the context a worker carries before Claude Code
// compacts it. On a Claude lane a compaction re-orients in 2–10k tokens
// (docs/plans/2026-09-06-token-diet-research.md L6) while a kill-and-
// restart takes 16–55 turns to pay back on Fable 5.1
// (docs/plans/2026-09-28-boot-cost-analytics-investigation.md §3.4), so a
// per-repo `autocompact:` beside `claude:` is the cheaper reset. Default
// off: unset passes no flag and leaves Claude Code's own window.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Autocompact window bounds, from the flag's own help text. A value
// outside them is refused at config load — claude would reject it at
// launch, after the worktree and window exist.
const (
	AutocompactAuto = "auto"
	autocompactMin  = 100_000
	autocompactMax  = 1_000_000
)

// CheckAutocompact refuses an autocompact value that is neither "auto"
// nor a plain integer token count in [100000, 1000000]. "" is always
// fine — it is "no cap": Claude Code's own default window.
func CheckAutocompact(v string) error {
	if v == "" || v == AutocompactAuto {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("autocompact %q (want auto or a token count, e.g. 150000)", v)
	}
	if n < autocompactMin || n > autocompactMax {
		return fmt.Errorf("autocompact %d (want auto or %d–%d tokens)", n, autocompactMin, autocompactMax)
	}
	return nil
}

// autocompactFlagAny matches an --autocompact flag in any spelling a
// hand-written `claude:` line might use: `--autocompact x`,
// `--autocompact=x`, single- or double-quoted.
var autocompactFlagAny = regexp.MustCompile(`(?:^|\s)--autocompact(?:=|\s+)('[^']*'|"[^"]*"|[^\s'"]+)`)

// AutocompactFlag returns the --autocompact value a command carries,
// unquoted, or "" when it carries none. The last one wins, as it does for
// claude's parser.
func AutocompactFlag(cmd string) string {
	all := autocompactFlagAny.FindAllStringSubmatch(cmd, -1)
	if len(all) == 0 {
		return ""
	}
	return strings.Trim(all[len(all)-1][1], `'"`)
}

// WithAutocompact sets a command's autocompact window to exactly v: any
// --autocompact already in it (a repo's hand-written `claude:` line, the
// token-diet research's config-only experiment) is stripped first, then
// `--autocompact <v>` is injected right after the executable token, the
// slot WithModel and WithEffort use. Strip-then-inject is the grove-142
// lesson: under claude's last-flag-wins parser a flag injected at the
// head loses to the same flag later in the command. "" returns cmd
// unchanged. The caller validates with CheckAutocompact; the value is
// "auto" or digits, so it is not shell-quoted.
func WithAutocompact(cmd, v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return cmd
	}
	cmd = strings.TrimSpace(autocompactFlagAny.ReplaceAllString(cmd, ""))
	if cmd == "" {
		return cmd
	}
	head, rest := cmd, ""
	if i := strings.IndexAny(cmd, " \t"); i >= 0 {
		head, rest = cmd[:i], strings.TrimLeft(cmd[i:], " \t")
	}
	out := head + " --autocompact " + v
	if rest != "" {
		out += " " + rest
	}
	return out
}
