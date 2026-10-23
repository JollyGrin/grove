// Package openrouter is a minimal read-only client for the two OpenRouter
// account endpoints the cockpit's ACCOUNT tab shows (grove-87): credits
// (lifetime purchased/used counters) and key usage (daily/weekly/monthly
// burn). Keys live in the encrypted store (internal/secrets), not here.
// Display only: grove never mutates any provider account, and there is no
// in-TUI payment flow, ever.
package openrouter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	// DefaultBaseURL matches the base_url worker model profiles use.
	DefaultBaseURL = "https://openrouter.ai/api"
	// CreditsURL is the human top-up page (crypto is a payment option on the
	// web page — the TUI only ever opens the link).
	CreditsURL = "https://openrouter.ai/settings/credits"
	// EnvVar is the canonical key variable — the same name model profiles
	// reference via auth_token_env.
	EnvVar = "OPENROUTER_API_KEY"
)

// Credits is GET /api/v1/credits: both fields are lifetime cumulative
// counters — show them as a dim caption, never as a bar (a ratio of two
// monotonic counters decays into noise).
type Credits struct {
	TotalCredits float64
	TotalUsage   float64
}

// Remaining is the spendable balance: purchased minus used, lifetime.
func (c Credits) Remaining() float64 { return c.TotalCredits - c.TotalUsage }

// KeyUsage is the burn-rate slice of GET /api/v1/key.
type KeyUsage struct {
	Daily   float64
	Weekly  float64
	Monthly float64
}

// client: one shared 5s-timeout client — calls are one-shot tea.Cmds fired
// on tab open / manual refresh, never polled (cockpit RAM rule).
var client = &http.Client{Timeout: 5 * time.Second}

func get(baseURL, path, key string, out any) error {
	req, err := http.NewRequest(http.MethodGet, baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("openrouter unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("openrouter HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// FetchCredits reads the lifetime purchased/used counters.
func FetchCredits(baseURL, key string) (Credits, error) {
	var body struct {
		Data struct {
			TotalCredits float64 `json:"total_credits"`
			TotalUsage   float64 `json:"total_usage"`
		} `json:"data"`
	}
	if err := get(baseURL, "/v1/credits", key, &body); err != nil {
		return Credits{}, err
	}
	return Credits{TotalCredits: body.Data.TotalCredits, TotalUsage: body.Data.TotalUsage}, nil
}

// FetchKeyUsage reads the key's rolling usage windows (burn rate).
func FetchKeyUsage(baseURL, key string) (KeyUsage, error) {
	var body struct {
		Data struct {
			Daily   float64 `json:"usage_daily"`
			Weekly  float64 `json:"usage_weekly"`
			Monthly float64 `json:"usage_monthly"`
		} `json:"data"`
	}
	if err := get(baseURL, "/v1/key", key, &body); err != nil {
		return KeyUsage{}, err
	}
	return KeyUsage{Daily: body.Data.Daily, Weekly: body.Data.Weekly, Monthly: body.Data.Monthly}, nil
}

// ValidateKey is the paste-flow gate: one-shot GET /api/v1/key with the
// candidate key; any non-200 (or network failure) rejects — the key is
// never saved unvalidated.
func ValidateKey(baseURL, key string) error {
	var body json.RawMessage
	return get(baseURL, "/v1/key", key, &body)
}
