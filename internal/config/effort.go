package config

// grove-435: effort as a first-class dial beside --model. Claude Code's
// model-config doc (https://code.claude.com/docs/en/model-config) lists the
// levels low|medium|high|xhigh|max and resolves them as
// CLAUDE_CODE_EFFORT_LEVEL env → --effort flag → settings → model default;
// the env var wins over the flag silently, which is why `gv doctor` warns
// on it (internal/doctor).

import (
	"fmt"
	"regexp"
	"strings"
)

// EffortLevels is the documented set, lowest to highest. Anything else is
// a typo and must fail before a worker exists, never launch on the model
// default while gv reports a pin.
var EffortLevels = []string{"low", "medium", "high", "xhigh", "max"}

// CheckEffort refuses an effort outside EffortLevels. "" is always fine —
// it is "no pin": Claude Code's own default for the model.
func CheckEffort(effort string) error {
	if effort == "" {
		return nil
	}
	for _, l := range EffortLevels {
		if l == effort {
			return nil
		}
	}
	return fmt.Errorf("unknown effort %q (want one of %s)", effort, strings.Join(EffortLevels, "|"))
}

// effortFlagAny matches an --effort flag in any spelling a hand-written
// `claude:` line might use: `--effort x`, `--effort=x`, single- or
// double-quoted.
var effortFlagAny = regexp.MustCompile(`(?:^|\s)--effort(?:=|\s+)('[^']*'|"[^"]*"|[^\s'"]+)`)

// EffortFlag returns the --effort value a command carries, unquoted, or ""
// when it carries none. The last one wins, as it does for claude's parser.
func EffortFlag(cmd string) string {
	all := effortFlagAny.FindAllStringSubmatch(cmd, -1)
	if len(all) == 0 {
		return ""
	}
	return strings.Trim(all[len(all)-1][1], `'"`)
}

// WithEffort sets a command's effort to exactly effort: any --effort
// already in it (a repo's hand-written `claude:` line) is stripped first,
// then `--effort <effort>` is injected right after the executable token,
// the slot WithModel uses. Strip-then-inject is the grove-142 lesson: a
// flag injected at the head loses to the same flag later in the command
// under claude's last-flag-wins parser, so without the strip the repo's
// own value would silently beat the pin gv reports. "" returns cmd
// unchanged. The caller validates with CheckEffort; the value is a bare
// word from EffortLevels, so it is not shell-quoted.
func WithEffort(cmd, effort string) string {
	effort = strings.TrimSpace(effort)
	if effort == "" {
		return cmd
	}
	cmd = strings.TrimSpace(effortFlagAny.ReplaceAllString(cmd, ""))
	if cmd == "" {
		return cmd
	}
	head, rest := cmd, ""
	if i := strings.IndexAny(cmd, " \t"); i >= 0 {
		head, rest = cmd[:i], strings.TrimLeft(cmd[i:], " \t")
	}
	out := head + " --effort " + effort
	if rest != "" {
		out += " " + rest
	}
	return out
}
