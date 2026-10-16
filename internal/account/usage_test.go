package account

import (
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestUsage(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC) // a Sunday
	sec := func(d time.Duration) string { return strconv.FormatInt(now.Add(d).Unix(), 10) }
	seenAt := func(ago time.Duration) string { return `"seen_at":"` + now.Add(-ago).Format(time.RFC3339) + `"` }
	in2h10 := sec(2*time.Hour + 10*time.Minute)
	in3d := sec(3 * 24 * time.Hour)
	file := func(body string) *string { return &body }

	tests := []struct {
		name     string
		file     *string // nil = no file
		wantHint string
		want5h   string // the window's --json
		want7d   string
		wantText string
	}{
		{
			name:     "fresh data is live",
			file:     file(`{"five_hour":{"used_percentage":42,"resets_at":` + in2h10 + `},"seven_day":{"used_percentage":61.4,"resets_at":` + in3d + `},` + seenAt(3*time.Minute) + `}`),
			want5h:   `{"state":"live","used_percentage":42,"resets_at":"2026-10-11T14:10:00Z"}`,
			want7d:   `{"state":"live","used_percentage":61.4,"resets_at":"2026-10-14T12:00:00Z"}`,
			wantText: "5h 42% (resets 14:10) · 7d 61% (resets Wed 12:00) · as of 3m",
		},
		{
			name:     "a reset tomorrow names the day",
			file:     file(`{"five_hour":{"used_percentage":5,"resets_at":` + sec(14*time.Hour) + `},"seven_day":{"used_percentage":9,"resets_at":` + in3d + `},` + seenAt(0) + `}`),
			want5h:   `{"state":"live","used_percentage":5,"resets_at":"2026-10-12T02:00:00Z"}`,
			want7d:   `{"state":"live","used_percentage":9,"resets_at":"2026-10-14T12:00:00Z"}`,
			wantText: "5h 5% (resets tomorrow 02:00) · 7d 9% (resets Wed 12:00) · as of 0m",
		},
		{
			name:     "a past resets_at is reset at 0, never the old percentage",
			file:     file(`{"five_hour":{"used_percentage":97,"resets_at":` + sec(-time.Minute) + `},"seven_day":{"used_percentage":61,"resets_at":` + in3d + `},` + seenAt(5*time.Hour) + `}`),
			want5h:   `{"state":"reset","used_percentage":0}`,
			want7d:   `{"state":"live","used_percentage":61,"resets_at":"2026-10-14T12:00:00Z"}`,
			wantText: "5h 0% (reset) · 7d 61% (resets Wed 12:00) · as of 5h00m",
		},
		{
			name:     "a window the payload never carried is unknown, not 0% reset",
			file:     file(`{"seven_day":{"used_percentage":10,"resets_at":` + in3d + `},` + seenAt(3*time.Minute) + `}`),
			want5h:   `{"state":"unknown"}`,
			want7d:   `{"state":"live","used_percentage":10,"resets_at":"2026-10-14T12:00:00Z"}`,
			wantText: "5h — no data · 7d 10% (resets Wed 12:00) · as of 3m",
		},
		{
			name:     "no used_percentage is unknown, never 0",
			file:     file(`{"five_hour":{"resets_at":` + sec(-time.Hour) + `},"seven_day":{"resets_at":` + in3d + `},` + seenAt(time.Minute) + `}`),
			want5h:   `{"state":"unknown"}`,
			want7d:   `{"state":"unknown"}`,
			wantText: "5h — no data · 7d — no data · as of 1m",
		},
		{
			name:     "no resets_at is live while young, stale once older than the window",
			file:     file(`{"five_hour":{"used_percentage":30},"seven_day":{"used_percentage":50},` + seenAt(6*time.Hour) + `}`),
			want5h:   `{"state":"live","used_percentage":30,"stale":true}`,
			want7d:   `{"state":"live","used_percentage":50}`,
			wantText: "5h 30% (stale, reset time unknown) · 7d 50% (reset time unknown) · as of 6h00m",
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
			file:     file(`{"five_hour":{"used_percentage":"lots"},` + seenAt(0) + `}`),
			wantHint: HintCorrupt,
			wantText: "no data — " + HintCorrupt,
		},
		{
			name:     "out-of-range resets_at is corrupt",
			file:     file(`{"five_hour":{"used_percentage":1,"resets_at":1e300},` + seenAt(0) + `}`),
			wantHint: HintCorrupt,
			wantText: "no data — " + HintCorrupt,
		},
		{
			name:     "negative resets_at is corrupt",
			file:     file(`{"five_hour":{"used_percentage":1,"resets_at":-5},` + seenAt(0) + `}`),
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
				if err := os.WriteFile(s.UsagePath("alt"), []byte(*tc.file), 0o600); err != nil {
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
				if got := jsonOf(t, u.FiveHour); got != tc.want5h {
					t.Errorf("five_hour = %s, want %s", got, tc.want5h)
				}
				if got := jsonOf(t, u.SevenDay); got != tc.want7d {
					t.Errorf("seven_day = %s, want %s", got, tc.want7d)
				}
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

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// No data is only a hint in the --json contract.
func TestUsageNoDataJSON(t *testing.T) {
	if got, want := jsonOf(t, Usage{Hint: HintNoSession}), `{"hint":"no session on this account yet"}`; got != want {
		t.Errorf("json = %s, want %s", got, want)
	}
}

// The reader decodes exactly what the record type the shim writes encodes.
func TestUsageRecordRoundTrip(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	rec := UsageRecord{FiveHour: &RecordedWindow{UsedPercentage: "42.5", ResetsAt: json.Number(strconv.FormatInt(now.Add(time.Hour).Unix(), 10))}, SeenAt: now}
	b, _ := json.Marshal(rec)
	if err := os.MkdirAll(s.UsageDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.UsagePath("alt"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadUsageRecord(s.UsagePath("alt"))
	if err != nil || *got.FiveHour != *rec.FiveHour || got.SevenDay != nil || !got.SeenAt.Equal(now) {
		t.Fatalf("ReadUsageRecord = %+v, %v", got, err)
	}
	if u := s.Usage("alt", now); u.FiveHour.State != StateLive || *u.FiveHour.UsedPercentage != 42.5 || u.SevenDay.State != StateUnknown {
		t.Errorf("Usage = %+v", u)
	}
}

func TestUsageAt(t *testing.T) {
	read := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	seen := read.Add(-time.Minute)
	resets := read.Add(30 * time.Minute)
	pct := 42.0
	u := Usage{
		FiveHour: &UsageWindow{Name: FiveHour, State: StateLive, UsedPercentage: &pct, ResetsAt: &resets},
		SevenDay: &UsageWindow{Name: SevenDay, State: StateLive, UsedPercentage: &pct},
		SeenAt:   &seen,
	}
	// Before the reset nothing changes.
	if got := u.At(read.Add(29 * time.Minute)); got.FiveHour.State != StateLive || got.FiveHour.ResetsAt == nil || got.SevenDay.Stale {
		t.Errorf("before reset: %+v / %+v", *got.FiveHour, *got.SevenDay)
	}
	// Once resets_at passes: reset, 0%, no reset time — and u is untouched.
	got := u.At(read.Add(30 * time.Minute))
	if w := got.FiveHour; w.State != StateReset || *w.UsedPercentage != 0 || w.ResetsAt != nil {
		t.Errorf("after reset: %+v", *w)
	}
	if u.FiveHour.State != StateLive || *u.FiveHour.UsedPercentage != 42 {
		t.Error("At mutated its receiver")
	}
	// A reset-time-less window goes stale once older than the window.
	if got := u.At(seen.Add(7 * 24 * time.Hour)); !got.SevenDay.Stale {
		t.Error("7d window a week old should be stale")
	}
	// No data stays no data.
	if got := (Usage{Hint: HintNoSession}).At(read); got.Hint != HintNoSession || got.FiveHour != nil {
		t.Errorf("no-data usage changed: %+v", got)
	}
}
