// Package statusline is `gv statusline` (claude-accounts design,
// docs/plans/2026-10-07-claude-accounts-design.md §Decision 8): a
// pass-through shim in front of the operator's own status line command
// that records the per-account 5-hour / weekly usage Claude Code puts on
// the statusline's stdin. That JSON is the documented usage source
// (https://code.claude.com/docs/en/statusline): `rate_limits.five_hour`
// and `rate_limits.seven_day`, each `{used_percentage, resets_at}`
// (percent 0–100, Unix epoch seconds), present only for Pro/Max after the
// session's first API response, each window dropped once it resets.
//
// The status line must never break because of grove: the wrapped
// command's stdout, stderr and exit status pass through untouched, and
// every capture error is swallowed.
package statusline

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/JollyGrin/grove/internal/account"
)

// Window is one rate-limit window as Claude Code reports it. The numbers
// are kept as json.Number so they round-trip exactly as received.
type Window struct {
	UsedPercentage json.Number `json:"used_percentage,omitempty"`
	ResetsAt       json.Number `json:"resets_at,omitempty"`
}

// Usage is one account's recorded usage file. SeenAt is when these values
// were first seen; an unchanged reading never rewrites the file.
type Usage struct {
	FiveHour *Window   `json:"five_hour,omitempty"`
	SevenDay *Window   `json:"seven_day,omitempty"`
	SeenAt   time.Time `json:"seen_at"`
}

// UsageDir is where usage files live under a grove state dir.
func UsageDir(stateDir string) string {
	return filepath.Join(account.Open(stateDir).Dir, "usage")
}

// AccountName is the usage file's name for a GROVE_ACCOUNT value: the
// value when it is a valid account name, `login` when unset. ok=false for
// anything else (it would not be a safe file name).
func AccountName(env string) (string, bool) {
	if env == "" || env == account.Login {
		return account.Login, true
	}
	if account.ValidateName(env) != nil {
		return "", false
	}
	return env, true
}

// Run is `gv statusline`: it reads stdin once, runs wrap through `sh -c`
// with the same bytes on its stdin (stdout/stderr passed straight
// through), records usage into usageDir/<acct>.json meanwhile, and
// returns the wrapped command's exit status. An empty wrap — or one that
// is the shim itself — prints nothing and exits 0. acct is the GROVE_ACCOUNT value.
func Run(stdin io.Reader, stdout, stderr io.Writer, wrap, usageDir, acct string, now time.Time) int {
	in, _ := io.ReadAll(stdin)
	if IsShim(wrap) {
		wrap = "" // a recorded shim would call itself forever
	}
	var cmd *exec.Cmd
	if strings.TrimSpace(wrap) != "" {
		cmd = exec.Command("sh", "-c", wrap)
		cmd.Stdin = bytes.NewReader(in)
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		if err := cmd.Start(); err != nil {
			cmd = nil
		}
	}
	// The capture runs while the wrapped command does, so it adds no
	// latency beyond the process's own start-up.
	if name, ok := AccountName(acct); ok {
		_, _ = Capture(in, filepath.Join(usageDir, name+".json"), now)
	}
	if cmd == nil {
		if strings.TrimSpace(wrap) != "" {
			return 127 // what sh reports for a command it cannot run
		}
		return 0
	}
	if err := cmd.Wait(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() >= 0 {
			return ee.ExitCode()
		}
		return 1
	}
	return 0
}

// Capture writes the rate_limits in a statusline payload to path, when
// present and different from what path already holds. wrote reports a
// write; a payload without rate_limits (or without either window) writes
// nothing.
func Capture(payload []byte, path string, now time.Time) (wrote bool, err error) {
	var p struct {
		RateLimits *struct {
			FiveHour *Window `json:"five_hour"`
			SevenDay *Window `json:"seven_day"`
		} `json:"rate_limits"`
	}
	d := json.NewDecoder(bytes.NewReader(payload))
	d.UseNumber()
	if err := d.Decode(&p); err != nil {
		return false, err
	}
	if p.RateLimits == nil || (p.RateLimits.FiveHour == nil && p.RateLimits.SevenDay == nil) {
		return false, nil
	}
	u := Usage{FiveHour: p.RateLimits.FiveHour, SevenDay: p.RateLimits.SevenDay, SeenAt: now.UTC()}
	if old, err := ReadUsage(path); err == nil && sameWindow(old.FiveHour, u.FiveHour) && sameWindow(old.SevenDay, u.SevenDay) {
		return false, nil
	}
	data, err := json.Marshal(u)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	return true, writeAtomic(path, append(data, '\n'), 0o600)
}

// ReadUsage reads one usage file.
func ReadUsage(path string) (Usage, error) {
	var u Usage
	raw, err := os.ReadFile(path)
	if err != nil {
		return u, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return u, d.Decode(&u)
}

func sameWindow(a, b *Window) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// writeAtomic replaces path via a temp file in the same dir + rename.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op after a successful rename
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
