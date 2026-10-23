package hooks

import (
	"encoding/json"
	"io"
	"regexp"
	"strings"
)

// secretsGuardEvent is the receiver argument `gv hooks install` wires to
// PreToolUse (grove-370). Unlike every other receiver it is not scoped to
// a tracked worker: it runs in every Claude Code session of the profile,
// because the secrets it protects are the host's, not a task's.
const secretsGuardEvent = "secrets-guard"

// secretsGuardPatterns are what a tool payload may not mention: the
// host's private age identity, the verb that prints every stored value,
// and the ciphertext store. `gv keys ls` and `gv keys exec` are
// deliberately absent — whether a lane may use them is the brain's rule,
// not this hook's (a lane that abuses exec gets it added here later).
var secretsGuardPatterns = []*regexp.Regexp{
	regexp.MustCompile(`age/identity\.txt`),
	regexp.MustCompile(`\bgv\s+keys\s+env\b`),
	regexp.MustCompile(`\.config/grove/secrets/`),
}

// secretsGuardVerdict returns the pattern a tool_input mentions, or "".
// It scans every string inside the decoded JSON, so a `\/`-escaped path
// or a nested field cannot slip past a raw-text match; undecodable input
// falls back to the raw text.
func secretsGuardVerdict(toolInput json.RawMessage) string {
	if len(toolInput) == 0 {
		return ""
	}
	var texts []string
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			texts = append(texts, x)
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	var v any
	if err := json.Unmarshal(toolInput, &v); err != nil {
		texts = []string{string(toolInput)}
	} else {
		walk(v)
	}
	text := strings.Join(texts, "\n")
	for _, re := range secretsGuardPatterns {
		if re.MatchString(text) {
			return re.String()
		}
	}
	return ""
}

// secretsGuard writes a PreToolUse deny decision to stdout when the
// payload mentions a guarded path or verb; otherwise it stays silent so
// Claude Code reads no decision and the tool call proceeds.
func secretsGuard(p Payload, stdout io.Writer) error {
	if secretsGuardVerdict(p.ToolInput) == "" {
		return nil
	}
	out, _ := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]string{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "deny",
			"permissionDecisionReason": "grove: this call touches the host's secret store (age identity, `gv keys env` or the secrets directory). Use `gv keys ls` to see names, or `gv keys exec NAME -- cmd` to use a key without reading it.",
		},
	})
	_, err := stdout.Write(append(out, '\n'))
	return err
}
