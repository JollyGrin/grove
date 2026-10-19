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
	"github.com/JollyGrin/grove/internal/config"
)

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

// DepthEnv marks a process as running under the shim. Run sets it on the
// wrapped command and refuses to run when it is already set, so a wrap
// that reaches `gv statusline` by any route IsShim cannot see (a script,
// an alias) ends after one level instead of recursing without limit.
const DepthEnv = "GROVE_STATUSLINE_DEPTH"

// SeenAtRefresh is how stale seen_at may get while the values hold still:
// an unchanged reading rewrites the file only once seen_at is this old,
// so seen_at tracks the last sighting to within a minute without a write
// per status line refresh (Claude Code debounces those at 300ms).
const SeenAtRefresh = time.Minute

// Run is `gv statusline`: it reads stdin once, runs wrap through `sh -c`
// with the same bytes on its stdin (stdout/stderr passed straight
// through), records usage into usageDir/<acct>.json meanwhile, and
// returns the wrapped command's exit status. An empty wrap — or one that
// is the shim itself — prints nothing and exits 0. acct is the
// GROVE_ACCOUNT value. Under DepthEnv (a nested shim) it does nothing at
// all and exits 0.
func Run(stdin io.Reader, stdout, stderr io.Writer, wrap, usageDir, acct string, now time.Time) int {
	if os.Getenv(DepthEnv) != "" {
		return 0
	}
	in, _ := io.ReadAll(stdin)
	if IsShim(wrap) {
		wrap = "" // a recorded shim would call itself forever
	}
	var cmd *exec.Cmd
	if strings.TrimSpace(wrap) != "" {
		cmd = exec.Command("sh", "-c", wrap)
		cmd.Env = append(os.Environ(), DepthEnv+"=1")
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

// Capture records a statusline payload's rate_limits at path. wrote
// reports a write.
//
//   - rate_limits present: the windows it carries are recorded (an absent
//     window is omitted — Claude Code drops one once it resets, so an
//     empty rate_limits records that both reset) with seen_at = now. An
//     unchanged reading rewrites only once seen_at is SeenAtRefresh old.
//   - rate_limits absent with a file already there (this account is a
//     subscriber; the session just has no reading yet): a recorded
//     window whose resets_at has passed is dropped, so a reset never
//     leaves the pre-reset percentage standing. seen_at is kept — nothing
//     was seen.
//   - rate_limits absent and no file: nothing is written.
func Capture(payload []byte, path string, now time.Time) (wrote bool, err error) {
	var p struct {
		RateLimits *struct {
			FiveHour *account.RecordedWindow `json:"five_hour"`
			SevenDay *account.RecordedWindow `json:"seven_day"`
		} `json:"rate_limits"`
	}
	d := json.NewDecoder(bytes.NewReader(payload))
	d.UseNumber()
	if err := d.Decode(&p); err != nil {
		return false, err
	}
	old, oldErr := account.ReadUsageRecord(path)
	var u account.UsageRecord
	switch {
	case p.RateLimits != nil:
		u = account.UsageRecord{FiveHour: p.RateLimits.FiveHour, SevenDay: p.RateLimits.SevenDay, SeenAt: now.UTC()}
		if oldErr == nil && sameWindows(old, u) && now.Sub(old.SeenAt) < SeenAtRefresh {
			return false, nil
		}
	case oldErr == nil:
		u = account.UsageRecord{FiveHour: unexpired(old.FiveHour, now), SevenDay: unexpired(old.SevenDay, now), SeenAt: old.SeenAt}
		if sameWindows(old, u) {
			return false, nil
		}
	default:
		return false, nil
	}
	data, err := json.Marshal(u)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	return true, config.WriteFileAtomic(path, append(data, '\n'), 0o600)
}

// unexpired returns w, or nil once its resets_at has passed.
func unexpired(w *account.RecordedWindow, now time.Time) *account.RecordedWindow {
	if w == nil {
		return nil
	}
	if at, err := w.ResetsAt.Float64(); err == nil && !now.Before(time.Unix(int64(at), 0)) {
		return nil
	}
	return w
}

func sameWindows(a, b account.UsageRecord) bool {
	return sameWindow(a.FiveHour, b.FiveHour) && sameWindow(a.SevenDay, b.SevenDay)
}

func sameWindow(a, b *account.RecordedWindow) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
