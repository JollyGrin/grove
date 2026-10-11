package account

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestUsage(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	future5h := now.Add(2*time.Hour + 10*time.Minute)
	future7d := now.Add(3 * 24 * time.Hour)
	seen := now.Add(-3 * time.Minute)
	sec := func(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }
	file := func(body string) *string { return &body }

	tests := []struct {
		name     string
		file     *string // nil = no file
		wantHint string
		want5h   *UsageWindow
		want7d   *UsageWindow
		wantText string
	}{
		{
			name: "fresh data",
			file: file(`{"five_hour":{"used_percentage":42,"resets_at":` + sec(future5h) + `},` +
				`"seven_day":{"used_percentage":61.4,"resets_at":` + sec(future7d) + `},"seen_at":"` + seen.Format(time.RFC3339) + `"}`),
			want5h:   &UsageWindow{Name: FiveHour, UsedPercentage: 42, ResetsAt: &future5h},
			want7d:   &UsageWindow{Name: SevenDay, UsedPercentage: 61.4, ResetsAt: &future7d},
			wantText: "5h 42% (resets 14:10) · 7d 61% (resets Wed) · as of 3m",
		},
		{
			name: "stale resets_at reads as reset, never the old percentage",
			file: file(`{"five_hour":{"used_percentage":97,"resets_at":` + sec(now.Add(-time.Minute)) + `},` +
				`"seven_day":{"used_percentage":61,"resets_at":` + sec(future7d) + `},"seen_at":"` + now.Add(-5*time.Hour).Format(time.RFC3339) + `"}`),
			want5h:   &UsageWindow{Name: FiveHour, Reset: true},
			want7d:   &UsageWindow{Name: SevenDay, UsedPercentage: 61, ResetsAt: &future7d},
			wantText: "5h 0% (reset) · 7d 61% (resets Wed) · as of 5h00m",
		},
		{
			name:     "window recorded absent (Claude Code dropped it on reset) reads as reset",
			file:     file(`{"seven_day":{"used_percentage":10,"resets_at":` + sec(future7d) + `},"seen_at":"` + seen.Format(time.RFC3339) + `"}`),
			want5h:   &UsageWindow{Name: FiveHour, Reset: true},
			want7d:   &UsageWindow{Name: SevenDay, UsedPercentage: 10, ResetsAt: &future7d},
			wantText: "5h 0% (reset) · 7d 10% (resets Wed) · as of 3m",
		},
		{
			name:     "missing file is no data with a hint",
			wantHint: HintNoSession,
			wantText: "no data — " + HintNoSession,
		},
		{
			name:     "corrupt file is a hint, no crash",
			file:     file(`{"five_hour":{"used_perc`),
			wantHint: HintCorrupt,
			wantText: "no data — " + HintCorrupt,
		},
		{
			name:     "non-numeric percentage is corrupt",
			file:     file(`{"five_hour":{"used_percentage":"lots"},"seen_at":"` + seen.Format(time.RFC3339) + `"}`),
			wantHint: HintCorrupt,
			wantText: "no data — " + HintCorrupt,
		},
		{
			name:     "no seen_at is corrupt",
			file:     file(`{"five_hour":{"used_percentage":5}}`),
			wantHint: HintCorrupt,
			wantText: "no data — " + HintCorrupt,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := Store{Dir: t.TempDir()}
			if tc.file != nil {
				if err := os.MkdirAll(s.UsageDir(), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(s.UsageDir(), "alt.json"), []byte(*tc.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			u := s.Usage("alt", now)
			if u.Hint != tc.wantHint {
				t.Errorf("hint = %q, want %q", u.Hint, tc.wantHint)
			}
			if tc.wantHint != "" {
				if u.FiveHour != nil || u.SevenDay != nil || u.SeenAt != nil {
					t.Errorf("no-data usage carries numbers: %+v", u)
				}
			} else {
				checkWindow(t, u.FiveHour, tc.want5h)
				checkWindow(t, u.SevenDay, tc.want7d)
				if u.SeenAt == nil {
					t.Error("seen_at missing")
				}
			}
			if got := UsageText(u, now, time.UTC); got != tc.wantText {
				t.Errorf("text = %q, want %q", got, tc.wantText)
			}
		})
	}
}

func checkWindow(t *testing.T, got, want *UsageWindow) {
	t.Helper()
	if got == nil || want == nil {
		if got != want {
			t.Errorf("window = %+v, want %+v", got, want)
		}
		return
	}
	if got.Name != want.Name || got.UsedPercentage != want.UsedPercentage || got.Reset != want.Reset ||
		(got.ResetsAt == nil) != (want.ResetsAt == nil) ||
		(got.ResetsAt != nil && !got.ResetsAt.Equal(*want.ResetsAt)) {
		t.Errorf("window = %+v (resets %v), want %+v (resets %v)", got, got.ResetsAt, want, want.ResetsAt)
	}
}

// A reset window's JSON has 0% and no resets_at; no-data JSON only a hint.
func TestUsageJSON(t *testing.T) {
	seen := time.Date(2026, 10, 11, 11, 57, 0, 0, time.UTC)
	b, _ := json.Marshal(Usage{FiveHour: &UsageWindow{Name: FiveHour, Reset: true}, SevenDay: &UsageWindow{Name: SevenDay, UsedPercentage: 3}, SeenAt: &seen})
	if got, want := string(b), `{"five_hour":{"used_percentage":0},"seven_day":{"used_percentage":3},"seen_at":"2026-10-11T11:57:00Z"}`; got != want {
		t.Errorf("json = %s, want %s", got, want)
	}
	b, _ = json.Marshal(Usage{Hint: HintNoSession})
	if !strings.HasPrefix(string(b), `{"hint":`) || strings.Contains(string(b), "five_hour") {
		t.Errorf("no-data json = %s", b)
	}
}
