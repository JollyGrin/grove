// Package remote runs a gv verb on a named remote grove host over ssh
// (grove-176, the remote-overflow train). The Mac is home, a
// Tailscale-reachable box is overflow; nothing syncs — the remote's own
// config maps repo names to its paths, and its output (including the
// --json envelope) is printed unchanged.
package remote

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/JollyGrin/grove/internal/config"
)

// Supported lists the verbs that pass through today (grove-177 added
// adopt + handoff for the remote half of `gv handoff`); anything else is
// "not supported yet".
var Supported = map[string]bool{"grab": true, "ls": true, "adopt": true, "handoff": true}

// SupportedList is the human-readable form for error messages.
const SupportedList = "grab, ls, adopt, handoff"

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

// Argv builds the local command line: ssh <target> -- <gv> <verb> <args…>.
// ssh hands the remote command to the remote login shell as ONE string,
// so every passthrough arg is single-quoted — `--brief "with spaces"`
// arrives on the host as the same single argument it was here. BatchMode
// keeps a missing key from hanging on a password prompt.
func Argv(h *config.Host, verb string, args []string) []string {
	parts := make([]string, 0, len(args)+2)
	parts = append(parts, quote(h.GV), verb)
	for _, a := range args {
		parts = append(parts, quote(a))
	}
	return []string{"ssh", "-o", "BatchMode=yes", h.SSH, "--", strings.Join(parts, " ")}
}

// quote single-quotes s for a POSIX shell; a token of plain safe
// characters is left bare so the remote command stays readable in
// process listings and ssh logs.
func quote(s string) string {
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
