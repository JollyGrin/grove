package kimi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// now is fixed so reset-hint math is deterministic.
var now = time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

// fullPayload exercises every shape kimi-cli's parser accepts: a usage
// summary, a duration-labeled window with nested detail, a named row
// carrying "remaining" instead of "used", and a countdown reset.
const fullPayload = `{
  "usage": {"limit": 1000000, "used": 250000, "resetTime": "2026-07-20T12:00:00Z"},
  "limits": [
    {
      "window": {"duration": 300, "timeUnit": "TIME_UNIT_MINUTE"},
      "detail": {"limit": 800, "used": 304, "resetAt": "2026-07-18T14:20:00.443553353Z"}
    },
    {"name": "weekly tokens", "detail": {"limit": 100, "remaining": 25, "reset_in": 7200}}
  ]
}`

func TestParseUsagesFull(t *testing.T) {
	rows := parseUsages([]byte(fullPayload), now)
	if len(rows) != 3 {
		t.Fatalf("rows = %+v, want 3", rows)
	}
	want := []Window{
		{Label: "weekly", Used: 250000, Limit: 1000000, ResetHint: "resets in 2d"},
		{Label: "5h", Used: 304, Limit: 800, ResetHint: "resets in 2h 20m"},
		{Label: "weekly tokens", Used: 75, Limit: 100, ResetHint: "resets in 2h"},
	}
	for i, w := range want {
		if rows[i] != w {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], w)
		}
	}
}

func TestParseUsagesPartial(t *testing.T) {
	// Counters only — no reset, no window, stringified numbers.
	rows := parseUsages([]byte(`{"limits":[{"limit":"500","used":"20"}]}`), now)
	if len(rows) != 1 || rows[0] != (Window{Label: "limit 1", Used: 20, Limit: 500}) {
		t.Errorf("rows = %+v", rows)
	}

	// A limit-only summary still shows; a counterless row is dropped.
	rows = parseUsages([]byte(`{"usage":{"limit":100},"limits":[{"name":"x"},"junk"]}`), now)
	if len(rows) != 1 || rows[0] != (Window{Label: "weekly", Limit: 100}) {
		t.Errorf("rows = %+v", rows)
	}

	// An unparseable timestamp degrades to the verbatim value.
	rows = parseUsages([]byte(`{"usage":{"used":1,"reset_at":"tomorrow-ish"}}`), now)
	if len(rows) != 1 || rows[0].ResetHint != "resets at tomorrow-ish" {
		t.Errorf("rows = %+v", rows)
	}

	// A past timestamp reads as already reset.
	rows = parseUsages([]byte(`{"usage":{"used":1,"resetAt":"2026-07-18T11:00:00Z"}}`), now)
	if len(rows) != 1 || rows[0].ResetHint != "reset" {
		t.Errorf("rows = %+v", rows)
	}
}

func TestParseUsagesGarbage(t *testing.T) {
	for _, raw := range []string{
		"not json at all",
		`"a string"`,
		`[1,2,3]`,
		`{}`,
		`{"usage": 42, "limits": "nope"}`,
		`{"usage": {"note": "no counters"}, "limits": [17, null]}`,
	} {
		if rows := parseUsages([]byte(raw), now); len(rows) != 0 {
			t.Errorf("parseUsages(%q) = %+v, want empty", raw, rows)
		}
	}
}

func TestLimitLabelForms(t *testing.T) {
	cases := []struct {
		window map[string]any
		want   string
	}{
		{map[string]any{"duration": float64(300), "timeUnit": "TIME_UNIT_MINUTE"}, "5h"},
		{map[string]any{"duration": float64(90), "timeUnit": "TIME_UNIT_MINUTE"}, "90m"},
		{map[string]any{"duration": float64(6), "timeUnit": "TIME_UNIT_HOUR"}, "6h"},
		{map[string]any{"duration": float64(7), "timeUnit": "TIME_UNIT_DAY"}, "7d"},
		{map[string]any{"duration": float64(30)}, "30s"},
		{nil, "limit 3"},
	}
	for _, c := range cases {
		if got := limitLabel(map[string]any{}, map[string]any{}, c.window, 2); got != c.want {
			t.Errorf("limitLabel(window=%v) = %q, want %q", c.window, got, c.want)
		}
	}
	// An explicit name wins over the duration.
	got := limitLabel(map[string]any{"scope": "5h requests"}, map[string]any{},
		map[string]any{"duration": float64(300), "timeUnit": "TIME_UNIT_MINUTE"}, 0)
	if got != "5h requests" {
		t.Errorf("scope label = %q", got)
	}
}

func TestFormatDur(t *testing.T) {
	cases := map[int64]string{
		45:     "45s",
		120:    "2m",
		3600:   "1h",
		8400:   "2h 20m",
		86400:  "1d",
		90000:  "1d 1h",
		604800: "7d",
	}
	for secs, want := range cases {
		if got := formatDur(secs); got != want {
			t.Errorf("formatDur(%d) = %q, want %q", secs, got, want)
		}
	}
}

func TestIsPlanBaseURL(t *testing.T) {
	for u, want := range map[string]bool{
		"https://api.kimi.com/coding":  true,
		"https://api.kimi.com":         true,
		"https://api.kimi.com/":        true,
		"https://api.moonshot.ai/v1":   false, // per-token endpoint: no quota API
		"https://openrouter.ai/api":    false,
		"https://api.kimi.com.evil.io": false,
		"":                             false,
	} {
		if got := IsPlanBaseURL(u); got != want {
			t.Errorf("IsPlanBaseURL(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestUsagesHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/usages" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch r.Header.Get("Authorization") {
		case "Bearer good":
			_, _ = w.Write([]byte(fullPayload))
		case "Bearer garbage":
			_, _ = w.Write([]byte("<html>maintenance</html>"))
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer srv.Close()

	rows, err := Usages(srv.URL, "good")
	if err != nil || len(rows) != 3 {
		t.Fatalf("Usages = %+v, %v; want 3 rows", rows, err)
	}
	// Trailing slashes in a profile's base_url must not break the path.
	if rows, err = Usages(srv.URL+"/", "good"); err != nil || len(rows) != 3 {
		t.Fatalf("Usages with trailing slash = %+v, %v", rows, err)
	}

	// A 200 with a garbage body is an empty result, not an error.
	if rows, err = Usages(srv.URL, "garbage"); err != nil || len(rows) != 0 {
		t.Errorf("garbage body → %+v, %v; want empty, nil", rows, err)
	}

	// HTTP 401 and 404 are errors (caller shows dashes with the hint).
	if _, err := Usages(srv.URL, "bad"); err == nil || err.Error() != "kimi HTTP 401" {
		t.Errorf("401 err = %v", err)
	}
	if _, err := Usages(srv.URL+"/wrong-mount", "good"); err == nil || err.Error() != "kimi HTTP 404" {
		t.Errorf("404 err = %v", err)
	}
}
