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

// Usage reading (claude-accounts design §Decision 8). `gv statusline`
// (internal/statusline) records the 5-hour / weekly windows Claude Code
// hands the status line into <accounts dir>/usage/<name>.json; this side
// only reads those files — never the network. The numbers are the last
// values a session on the account saw, stamped seen_at; they are never
// extrapolated.

// Usage hints, shown in place of numbers.
const (
	HintNoSession = "no session on this account yet"
	HintCorrupt   = "usage file unreadable — the next session status line refresh on this account rewrites it"
)

// Window names, as Claude Code's statusline input spells them.
const (
	FiveHour = "five_hour"
	SevenDay = "seven_day"
)

// UsageWindow is one rate-limit window as last seen. ResetsAt is nil when
// no reset time is known — including a window already reset, which reads
// 0% (Reset true).
type UsageWindow struct {
	Name           string     `json:"-"`
	UsedPercentage float64    `json:"used_percentage"`
	ResetsAt       *time.Time `json:"resets_at,omitempty"`
	Reset          bool       `json:"-"`
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

// UsageDir is where `gv statusline` records usage files.
func (s Store) UsageDir() string { return filepath.Join(s.Dir, "usage") }

// Usage reads name's usage file (name may be Login) as of now. A missing
// file is no data (HintNoSession); an unreadable or malformed one is
// HintCorrupt, never an error. A window whose resets_at has passed — or
// that the file records as absent, which is how Claude Code reports a
// reset — reads as reset: 0%, no reset time.
func (s Store) Usage(name string, now time.Time) Usage {
	raw, err := os.ReadFile(filepath.Join(s.UsageDir(), name+".json"))
	if os.IsNotExist(err) {
		return Usage{Hint: HintNoSession}
	}
	if err != nil {
		return Usage{Hint: HintCorrupt}
	}
	return parseUsage(raw, now)
}

type rawWindow struct {
	UsedPercentage json.Number `json:"used_percentage"`
	ResetsAt       json.Number `json:"resets_at"`
}

func parseUsage(raw []byte, now time.Time) Usage {
	var f struct {
		FiveHour *rawWindow `json:"five_hour"`
		SevenDay *rawWindow `json:"seven_day"`
		SeenAt   time.Time  `json:"seen_at"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&f) != nil || f.SeenAt.IsZero() {
		return Usage{Hint: HintCorrupt}
	}
	fh, ok1 := window(FiveHour, f.FiveHour, now)
	sd, ok2 := window(SevenDay, f.SevenDay, now)
	if !ok1 || !ok2 {
		return Usage{Hint: HintCorrupt}
	}
	seen := f.SeenAt.UTC()
	return Usage{FiveHour: fh, SevenDay: sd, SeenAt: &seen}
}

// window converts one recorded window; ok=false when its numbers are not
// numbers.
func window(name string, w *rawWindow, now time.Time) (*UsageWindow, bool) {
	reset := &UsageWindow{Name: name, Reset: true}
	if w == nil {
		return reset, true
	}
	out := &UsageWindow{Name: name}
	if w.UsedPercentage != "" {
		pct, err := w.UsedPercentage.Float64()
		if err != nil || math.IsNaN(pct) || math.IsInf(pct, 0) {
			return nil, false
		}
		out.UsedPercentage = pct
	}
	if w.ResetsAt != "" {
		sec, err := w.ResetsAt.Float64()
		if err != nil || math.IsNaN(sec) || math.IsInf(sec, 0) {
			return nil, false
		}
		at := time.Unix(int64(sec), 0).UTC()
		if !now.Before(at) {
			return reset, true
		}
		out.ResetsAt = &at
	}
	return out, true
}

// UsageText is the `gv account ls --usage` text for u, e.g. `5h 42%
// (resets 14:10) · 7d 61% (resets Mon) · as of 3m`. Reset times read in
// loc: a clock time within a day, else the weekday.
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
		part := fmt.Sprintf("%s %.0f%%", label, w.UsedPercentage)
		switch {
		case w.Reset:
			part += " (reset)"
		case w.ResetsAt != nil:
			at := w.ResetsAt.In(loc)
			when := at.Format("15:04")
			if at.Sub(now) >= 24*time.Hour {
				when = at.Format("Mon")
			}
			part += " (resets " + when + ")"
		}
		parts = append(parts, part)
	}
	parts = append(parts, "as of "+usageAge(now.Sub(*u.SeenAt)))
	return strings.Join(parts, " · ")
}

func usageAge(d time.Duration) string {
	d = max(d, 0).Round(time.Minute)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
