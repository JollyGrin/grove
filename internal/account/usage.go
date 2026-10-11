package account

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Usage (claude-accounts design §Decision 8). `gv statusline`
// (internal/statusline) writes the 5-hour / weekly windows Claude Code
// hands the status line into <accounts dir>/usage/<name>.json; `gv account
// ls --usage` reads them. Both sides share the record below, so the
// writer and the reader cannot drift. Nothing here touches the network.
//
// The promise: a gauge never claims a number it did not observe. A
// window is live (observed, its reset still ahead), reset (observed, its
// reset since passed — 0% by Claude Code's own rule), or unknown (no
// percentage was recorded). The numbers are the last a session saw,
// stamped seen_at; they are never extrapolated.

// RecordedWindow is one rate-limit window as Claude Code reports it on
// the statusline's stdin: percent 0–100 and Unix epoch seconds. The
// numbers stay json.Number so they round-trip exactly as received.
type RecordedWindow struct {
	UsedPercentage json.Number `json:"used_percentage,omitempty"`
	ResetsAt       json.Number `json:"resets_at,omitempty"`
}

// UsageRecord is one account's usage file. SeenAt is the last sighting.
// An absent window is one the last payload did not carry.
type UsageRecord struct {
	FiveHour *RecordedWindow `json:"five_hour,omitempty"`
	SevenDay *RecordedWindow `json:"seven_day,omitempty"`
	SeenAt   time.Time       `json:"seen_at"`
}

// ReadUsageRecord reads one usage file.
func ReadUsageRecord(path string) (UsageRecord, error) {
	var u UsageRecord
	raw, err := os.ReadFile(path)
	if err != nil {
		return u, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return u, d.Decode(&u)
}

// UsageDir is where usage files live.
func (s Store) UsageDir() string { return filepath.Join(s.Dir, "usage") }

// UsagePath is name's usage file (name may be Login).
func (s Store) UsagePath(name string) string { return filepath.Join(s.UsageDir(), name+".json") }

// Usage hints, shown in place of numbers.
const (
	HintNoSession = "no session on this account yet"
	HintCorrupt   = "usage file unreadable — the next session status line refresh on this account rewrites it"
)

// Window names, as Claude Code's statusline input spells them, and their
// lengths.
const (
	FiveHour = "five_hour"
	SevenDay = "seven_day"
)

var windowLength = map[string]time.Duration{FiveHour: 5 * time.Hour, SevenDay: 7 * 24 * time.Hour}

// Window states (the `state` field of the --json contract).
const (
	StateLive    = "live"    // observed; its reset is still ahead (or unknown — see Stale)
	StateReset   = "reset"   // observed, and its resets_at has since passed: 0%
	StateUnknown = "unknown" // no percentage recorded: never read as 0
)

// UsageWindow is one window as `ls --usage` reports it. UsedPercentage is
// nil exactly when State is unknown. ResetsAt is set only on a live window
// whose reset time was recorded. Stale marks a live window with no reset
// time whose sighting is older than the window itself: it cannot still be
// current.
type UsageWindow struct {
	Name           string     `json:"-"`
	State          string     `json:"state"`
	UsedPercentage *float64   `json:"used_percentage,omitempty"`
	ResetsAt       *time.Time `json:"resets_at,omitempty"`
	Stale          bool       `json:"stale,omitempty"`
}

// Usage is one account's usage, the `usage` object of an `account ls
// --usage --json` row (a plugin contract — additive-only). With data,
// both windows and SeenAt are set; with none, only Hint is.
type Usage struct {
	FiveHour *UsageWindow `json:"five_hour,omitempty"`
	SevenDay *UsageWindow `json:"seven_day,omitempty"`
	SeenAt   *time.Time   `json:"seen_at,omitempty"`
	Hint     string       `json:"hint,omitempty"`
}

// Windows lists the windows present, 5-hour first.
func (u Usage) Windows() []UsageWindow {
	var out []UsageWindow
	for _, w := range []*UsageWindow{u.FiveHour, u.SevenDay} {
		if w != nil {
			out = append(out, *w)
		}
	}
	return out
}

// Usage reads name's usage file (name may be Login) as of now. A missing
// file is no data (HintNoSession); an unreadable or malformed one is
// HintCorrupt, never an error.
func (s Store) Usage(name string, now time.Time) Usage {
	rec, err := ReadUsageRecord(s.UsagePath(name))
	if os.IsNotExist(err) {
		return Usage{Hint: HintNoSession}
	}
	if err != nil || rec.SeenAt.IsZero() {
		return Usage{Hint: HintCorrupt}
	}
	fh, ok1 := window(FiveHour, rec.FiveHour, rec.SeenAt, now)
	sd, ok2 := window(SevenDay, rec.SevenDay, rec.SeenAt, now)
	if !ok1 || !ok2 {
		return Usage{Hint: HintCorrupt}
	}
	seen := rec.SeenAt.UTC()
	return Usage{FiveHour: fh, SevenDay: sd, SeenAt: &seen}
}

// maxEpoch bounds resets_at before the int64 conversion: year ~5138, far
// past any real reset and far inside int64.
const maxEpoch = 1e11

// window derives one window's state; ok=false when its numbers are not
// sane numbers.
func window(name string, w *RecordedWindow, seen, now time.Time) (*UsageWindow, bool) {
	out := &UsageWindow{Name: name, State: StateUnknown}
	if w == nil || w.UsedPercentage == "" {
		return out, true // never carried, or no percentage: unknown, not 0
	}
	pct, err := w.UsedPercentage.Float64()
	if err != nil || math.IsNaN(pct) || math.IsInf(pct, 0) {
		return nil, false
	}
	if w.ResetsAt == "" {
		out.State, out.UsedPercentage = StateLive, &pct
		out.Stale = now.Sub(seen) >= windowLength[name]
		return out, true
	}
	sec, err := w.ResetsAt.Float64()
	if err != nil || math.IsNaN(sec) || sec < 0 || sec > maxEpoch {
		return nil, false
	}
	at := time.Unix(int64(sec), 0).UTC()
	if !now.Before(at) {
		zero := 0.0
		out.State, out.UsedPercentage = StateReset, &zero
		return out, true
	}
	out.State, out.UsedPercentage, out.ResetsAt = StateLive, &pct, &at
	return out, true
}

// UsageText is the `gv account ls --usage` text for u, e.g. `5h 42%
// (resets 14:10) · 7d 61% (resets Tue 22:00) · as of 3m`. Reset times
// read in loc: a bare clock time today, `tomorrow 22:00`, else the
// weekday and time.
func UsageText(u Usage, now time.Time, loc *time.Location) string {
	if u.SeenAt == nil {
		return "no data — " + u.Hint
	}
	var parts []string
	for _, w := range u.Windows() {
		label := "5h"
		if w.Name == SevenDay {
			label = "7d"
		}
		switch {
		case w.State == StateUnknown || w.UsedPercentage == nil:
			parts = append(parts, label+" — no data")
		case w.State == StateReset:
			parts = append(parts, label+" 0% (reset)")
		case w.ResetsAt != nil:
			parts = append(parts, fmt.Sprintf("%s %.0f%% (resets %s)", label, *w.UsedPercentage, ResetWhen(*w.ResetsAt, now, loc)))
		case w.Stale:
			parts = append(parts, fmt.Sprintf("%s %.0f%% (stale, reset time unknown)", label, *w.UsedPercentage))
		default:
			parts = append(parts, fmt.Sprintf("%s %.0f%% (reset time unknown)", label, *w.UsedPercentage))
		}
	}
	parts = append(parts, "as of "+UsageAge(now.Sub(*u.SeenAt)))
	return strings.Join(parts, " · ")
}

// ResetWhen is a reset time as the usage text reads it in loc: a bare
// clock time today, `tomorrow 22:00`, else the weekday and time.
func ResetWhen(at, now time.Time, loc *time.Location) string {
	at, now = at.In(loc), now.In(loc)
	day := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc) }
	switch days := int(day(at).Sub(day(now)).Hours()/24 + 0.5); days {
	case 0:
		return at.Format("15:04")
	case 1:
		return "tomorrow " + at.Format("15:04")
	}
	return at.Format("Mon 15:04")
}

// UsageAge is a sighting's age, e.g. `4m`, `2h05m`, `3d`.
func UsageAge(d time.Duration) string {
	d = max(d, 0).Round(time.Minute)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
