// Package remote runs a gv verb on a named remote grove host over ssh
// (grove-176, the remote-overflow train). The Mac is home, a
// Tailscale-reachable box is overflow; nothing syncs — the remote's own
// config maps repo names to its paths, and its output (including the
// --json envelope) is printed unchanged.
package remote

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/JollyGrin/grove/internal/config"
)

// Supported lists the verbs that pass through today (grove-177 added
// adopt + handoff for the remote half of `gv handoff`; grove-184 the
// relay/read/control five); anything else is "not supported yet".
// grove-198 added `orchestrator` — only its `new` subcommand relays; the
// dispatcher rejects the others with the same friendly shape.
var Supported = map[string]bool{
	"grab": true, "ls": true, "adopt": true, "handoff": true,
	"answer": true, "nudge": true, "diff": true, "pause": true, "untrack": true,
	"orchestrator": true,
}

// SupportedList is the human-readable form for error messages.
const SupportedList = "grab, ls, adopt, handoff, answer, nudge, diff, pause, untrack, orchestrator new"

// ExtractHost strips `--host <name>` / `--host=<name>` from args and
// returns the name plus the remaining args in their original order. A
// trailing bare `--host` (no value) is left in place for the verb's own
// flag parser to reject.
func ExtractHost(args []string) (host string, rest []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--host" || a == "-host":
			if i+1 < len(args) {
				host = args[i+1]
				i++
				continue
			}
		case strings.HasPrefix(a, "--host=") || strings.HasPrefix(a, "-host="):
			host = a[strings.Index(a, "=")+1:]
			continue
		}
		rest = append(rest, a)
	}
	return host, rest
}

// ExtractHostPrefix is ExtractHost for the relay verbs (answer/nudge):
// same stripping of `--host <name>` / `--host=<name>`, same
// trailing-bare-`--host` pass-through — except scanning stops at the
// first arg that does not start with `-`; that arg and everything after
// it are returned in rest verbatim. Relay free text may legitimately
// contain `--host` (`gv nudge grove-7 try gv ls --host pc`), and once
// the ticket position is reached every remaining word is payload.
func ExtractHostPrefix(args []string) (host string, rest []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			rest = append(rest, args[i:]...)
			break
		}
		switch {
		case a == "--host" || a == "-host":
			if i+1 < len(args) {
				host = args[i+1]
				i++
				continue
			}
		case strings.HasPrefix(a, "--host=") || strings.HasPrefix(a, "-host="):
			host = a[strings.Index(a, "=")+1:]
			continue
		}
		rest = append(rest, a)
	}
	return host, rest
}

// NewOpID mints a client op id for an idempotent relayed mutation
// (grove-186): 16 bytes of crypto/rand as lowercase hex (32 chars).
// go.mod deliberately has no uuid dependency. The id rides the ssh hop as
// `--op-id <v>` and lands on the remote's `answered` event as
// `data.op_id` — the receipt a retried hop dedups against.
func NewOpID() string {
	b := make([]byte, 16)
	// crypto/rand.Read only fails on a broken system, and an op id is
	// unguessable-or-nothing — panic is the honest failure shape.
	if _, err := rand.Read(b); err != nil {
		panic("remote: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// ExtractOpIDPrefix strips `--op-id <v>` / `--op-id=<v>` from args and
// returns the value plus the remaining args in order. The relay-verb twin
// of ExtractHostPrefix: scanning stops at the first arg that does not
// start with `-` (free text may legitimately mention --op-id), a leading
// unrecognized flag is kept in rest, and a trailing bare `--op-id` (no
// value) is left in place for the verb's own handling.
func ExtractOpIDPrefix(args []string) (opID string, rest []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			rest = append(rest, args[i:]...)
			break
		}
		switch {
		case a == "--op-id" || a == "-op-id":
			if i+1 < len(args) {
				opID = args[i+1]
				i++
				continue
			}
		case strings.HasPrefix(a, "--op-id=") || strings.HasPrefix(a, "-op-id="):
			opID = a[strings.Index(a, "=")+1:]
			continue
		}
		rest = append(rest, a)
	}
	return opID, rest
}

// Argv builds the local command line: ssh <target> -- <gv> <verb> <args…>.
// ssh hands the remote command to the remote login shell as ONE string,
// so every passthrough arg is single-quoted — `--brief "with spaces"`
// arrives on the host as the same single argument it was here. BatchMode
// keeps a missing key from hanging on a password prompt.
func Argv(h *config.Host, verb string, args []string) []string {
	parts := make([]string, 0, len(args)+2)
	parts = append(parts, Quote(h.GV), verb)
	for _, a := range args {
		parts = append(parts, Quote(a))
	}
	return []string{"ssh", "-o", "BatchMode=yes", h.SSH, "--", strings.Join(parts, " ")}
}

// Quote single-quotes s for a POSIX shell; a token of plain safe
// characters is left bare so the remote command stays readable in
// process listings and ssh logs.
func Quote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_=/.:@,+%") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Run executes verb on the named host with stdout/stderr streamed to the
// given writers and returns the remote exit code (ssh propagates it; 255
// is ssh's own connection failure). A non-exit error (ssh missing) is
// returned as err.
func Run(cfg *config.Config, host, verb string, args []string, stdout, stderr io.Writer) (int, error) {
	if !Supported[verb] {
		return 0, fmt.Errorf("--host is not supported for `gv %s` yet (supported: %s)", verb, SupportedList)
	}
	h, err := cfg.Host(host)
	if err != nil {
		return 0, err
	}
	argv := Argv(h, verb, args)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), nil
		}
		return 0, fmt.Errorf("ssh %s: %w", h.SSH, err)
	}
	return 0, nil
}

// chatAttachPrefix is the line the receiving half of `gv orchestrator new
// --workspace` prints for its detached chat session (grove-198). It is
// both the human's paste-able attach command when they are already logged
// into the host AND the machine-readable carrier the relaying half parses
// (ParseChatSession) to render the ssh form: the session's number is
// picked remotely, so the local side cannot know the name any other way.
const chatAttachPrefix = "attach: tmux attach -t ="

// ChatAttachLine renders the receiving half's attach hint for session.
func ChatAttachLine(session string) string { return chatAttachPrefix + session }

// ParseChatSession extracts the chat session name from a relayed
// `orchestrator new` run's stdout, or "" when the output carries no attach
// line (an error, an older remote gv, a spawn that never happened).
func ParseChatSession(out string) string {
	for _, line := range strings.Split(out, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), chatAttachPrefix)
		if !ok {
			continue
		}
		if name := strings.TrimSpace(rest); name != "" {
			return name
		}
	}
	return ""
}
