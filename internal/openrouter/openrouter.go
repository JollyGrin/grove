// Package openrouter is a minimal read-only client for the two OpenRouter
// account endpoints the cockpit's ACCOUNT tab shows (grove-87): credits
// (lifetime purchased/used counters) and key usage (daily/weekly/monthly
// burn). It also resolves — and, for the paste-key connect flow, persists —
// OPENROUTER_API_KEY in the same secrets file worker profiles self-source
// (~/.config/grove/.env). Display only: grove never mutates the OpenRouter
// account, and there is no in-TUI payment flow, ever.
package openrouter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
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

// Key resolves the API key: the process environment first, then a tolerant
// parse of envPath (the profile secrets file — `export NAME=value`,
// bare `NAME=value`, quoted or not; last assignment wins, like sourcing).
// "" means no key — callers degrade to dashes, never an error state.
func Key(envPath string) string {
	if k := os.Getenv(EnvVar); k != "" {
		return k
	}
	return keyFromFile(envPath)
}

func keyFromFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	key := ""
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		v, ok := strings.CutPrefix(line, EnvVar+"=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		if v != "" {
			key = v
		}
	}
	return key
}

// SaveKey persists key as the EnvVar assignment in envPath, creating the
// file 0600 if absent and preserving every other line byte-for-byte. An
// existing assignment (export or bare) is replaced in place so the file
// never accumulates stale keys.
func SaveKey(envPath, key string) error {
	line := "export " + EnvVar + "=" + key
	raw, err := os.ReadFile(envPath)
	if os.IsNotExist(err) {
		return os.WriteFile(envPath, []byte(line+"\n"), 0o600)
	}
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	replaced := false
	for i, l := range lines {
		t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "export "))
		if strings.HasPrefix(t, EnvVar+"=") {
			lines[i] = line
			replaced = true
		}
	}
	if !replaced {
		lines = append(lines, line)
	}
	return os.WriteFile(envPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

// Mask renders a key safely for screen: enough of the prefix to recognize
// it, the tail to disambiguate, nothing in between — e.g.
// "sk-or-v1-f9b...a6a". The full key must never render in the TUI.
func Mask(key string) string {
	r := []rune(key)
	if len(r) <= 15 {
		if len(r) <= 4 {
			return "…"
		}
		return string(r[:4]) + "…"
	}
	return string(r[:12]) + "..." + string(r[len(r)-3:])
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
