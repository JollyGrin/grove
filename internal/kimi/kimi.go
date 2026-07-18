// Package kimi is a minimal read-only client for the one Kimi Code plan
// endpoint the cockpit's ACCOUNT tab shows (grove-133): GET /v1/usages,
// the subscription plan's quota windows (~5h request buckets + weekly
// token limits). The API is undocumented; the schema reference is
// kimi-cli's _parse_usage_payload (src/kimi_cli/ui/shell/usage.py), so
// parsing is deliberately tolerant — fields may be absent or oddly typed,
// and anything unexpected degrades to an empty result, never an error
// state (same philosophy as internal/openrouter). Display only: grove
// never mutates the Kimi account.
package kimi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// planHost is the Kimi Code subscription plan host. Only plan keys have
// quota windows — the per-token moonshot.ai endpoints do not — so fuel
// gauges apply only to profiles whose base_url lives under this host.
const planHost = "https://api.kimi.com"

// IsPlanBaseURL reports whether a model profile's base_url targets the
// Kimi Code plan (e.g. https://api.kimi.com/coding).
func IsPlanBaseURL(u string) bool {
	u = strings.TrimSpace(u)
	return u == planHost || strings.HasPrefix(u, planHost+"/")
}

// Window is one quota window row: a compact label ("5h", "weekly", …),
// used/limit counters, and a human reset hint ("" = none). Limit 0 means
// the payload carried no usable limit — callers render a dash gauge.
type Window struct {
	Label     string
	Used      int64
	Limit     int64
	ResetHint string
}

// client: one shared 5s-timeout client — calls are one-shot tea.Cmds fired
// on tab open / manual refresh, never polled (cockpit RAM rule).
var client = &http.Client{Timeout: 5 * time.Second}

// Usages fetches the plan's quota windows: GET <baseURL>/v1/usages with
// the plan key as a Bearer token. Network failures and non-200s are
// errors (the caller shows dashes with a dim hint); a 200 whose body
// doesn't match the expected shape is an empty result, not an error.
func Usages(baseURL, key string) ([]Window, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/v1/usages", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kimi unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kimi HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("kimi unreachable")
	}
	return parseUsages(raw, time.Now()), nil
}

// parseUsages mirrors kimi-cli's _parse_usage_payload: a top-level
// "usage" summary object (the weekly window) plus a "limits" array whose
// items carry counters in "detail" (or inline) and the window size in
// "window". Every field is optional; rows with neither used nor limit
// are dropped; a payload with no recognizable shape yields nil.
func parseUsages(raw []byte, now time.Time) []Window {
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return nil
	}
	var rows []Window
	if u, ok := payload["usage"].(map[string]any); ok {
		if w, ok := toWindow(u, "weekly", now); ok {
			rows = append(rows, w)
		}
	}
	limits, _ := payload["limits"].([]any)
	for i, item := range limits {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		detail := m
		if d, ok := m["detail"].(map[string]any); ok {
			detail = d
		}
		window, _ := m["window"].(map[string]any)
		if w, ok := toWindow(detail, limitLabel(m, detail, window, i), now); ok {
			rows = append(rows, w)
		}
	}
	return rows
}

// toWindow builds one row from a counters map. "used" may instead arrive
// as "remaining" (used = limit − remaining); a row with neither counter
// carries no information and is dropped.
func toWindow(data map[string]any, defaultLabel string, now time.Time) (Window, bool) {
	limit, limitOK := toInt(data["limit"])
	used, usedOK := toInt(data["used"])
	if !usedOK {
		if rem, ok := toInt(data["remaining"]); ok && limitOK {
			used, usedOK = limit-rem, true
		}
	}
	if !usedOK && !limitOK {
		return Window{}, false
	}
	label := defaultLabel
	for _, k := range []string{"name", "title"} {
		if s, ok := data[k].(string); ok && s != "" {
			label = s
			break
		}
	}
	return Window{Label: label, Used: used, Limit: limit, ResetHint: resetHint(data, now)}, true
}

// limitLabel names a limits[] row: an explicit name/title/scope wins;
// otherwise the window duration compacts to "5h"/"300m"/"7d"; otherwise a
// positional fallback.
func limitLabel(item, detail, window map[string]any, idx int) string {
	for _, k := range []string{"name", "title", "scope"} {
		for _, src := range []map[string]any{item, detail} {
			if s, ok := src[k].(string); ok && s != "" {
				return s
			}
		}
	}
	var duration int64
	for _, src := range []map[string]any{window, item, detail} {
		if n, ok := toInt(src["duration"]); ok && n > 0 {
			duration = n
			break
		}
	}
	unit := ""
	for _, src := range []map[string]any{window, item, detail} {
		if s, ok := src["timeUnit"].(string); ok && s != "" {
			unit = s
			break
		}
	}
	if duration > 0 {
		switch {
		case strings.Contains(unit, "MINUTE"):
			if duration >= 60 && duration%60 == 0 {
				return fmt.Sprintf("%dh", duration/60)
			}
			return fmt.Sprintf("%dm", duration)
		case strings.Contains(unit, "HOUR"):
			return fmt.Sprintf("%dh", duration)
		case strings.Contains(unit, "DAY"):
			return fmt.Sprintf("%dd", duration)
		}
		return fmt.Sprintf("%ds", duration)
	}
	return fmt.Sprintf("limit %d", idx+1)
}

// resetHint extracts the row's reset signal: an absolute timestamp
// (reset_at/resetAt/reset_time/resetTime) becomes "resets in <dur>"
// relative to now, or a countdown field (reset_in/resetIn/ttl/window in
// seconds) formats directly. "" when the payload carries neither.
func resetHint(data map[string]any, now time.Time) string {
	for _, k := range []string{"reset_at", "resetAt", "reset_time", "resetTime"} {
		if s, ok := data[k].(string); ok && s != "" {
			return formatResetAt(s, now)
		}
	}
	for _, k := range []string{"reset_in", "resetIn", "ttl", "window"} {
		if n, ok := toInt(data[k]); ok && n > 0 {
			return "resets in " + formatDur(n)
		}
	}
	return ""
}

func formatResetAt(val string, now time.Time) string {
	t, err := time.Parse(time.RFC3339, val)
	if err != nil {
		return "resets at " + val
	}
	secs := int64(t.Sub(now).Seconds())
	if secs <= 0 {
		return "reset"
	}
	return "resets in " + formatDur(secs)
}

func formatDur(secs int64) string {
	switch {
	case secs >= 86400:
		if h := (secs % 86400) / 3600; h > 0 {
			return fmt.Sprintf("%dd %dh", secs/86400, h)
		}
		return fmt.Sprintf("%dd", secs/86400)
	case secs >= 3600:
		if m := (secs % 3600) / 60; m > 0 {
			return fmt.Sprintf("%dh %dm", secs/3600, m)
		}
		return fmt.Sprintf("%dh", secs/3600)
	case secs >= 60:
		return fmt.Sprintf("%dm", secs/60)
	}
	return fmt.Sprintf("%ds", secs)
}

// toInt is the tolerant numeric read: JSON numbers arrive as float64,
// some APIs stringify counters. Anything else is "absent".
func toInt(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case string:
		if i, err := strconv.ParseInt(n, 10, 64); err == nil {
			return i, true
		}
	}
	return 0, false
}
