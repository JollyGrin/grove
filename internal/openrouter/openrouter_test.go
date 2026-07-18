package openrouter

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestKeyPrefersEnvOverFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("export OPENROUTER_API_KEY=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvVar, "from-env")
	if got := Key(path, EnvVar); got != "from-env" {
		t.Errorf("Key = %q, want env to win", got)
	}
}

func TestKeyFromFileForms(t *testing.T) {
	cases := map[string]string{
		"export OPENROUTER_API_KEY=plain\n":                        "plain",
		"OPENROUTER_API_KEY=bare\n":                                "bare",
		`export OPENROUTER_API_KEY="double-quoted"` + "\n":         "double-quoted",
		"export OPENROUTER_API_KEY='single-quoted'\n":              "single-quoted",
		"# comment\nOTHER=x\nexport OPENROUTER_API_KEY=after\n":    "after",
		"export OPENROUTER_API_KEY=first\nOPENROUTER_API_KEY=last": "last", // sourcing semantics: last wins
		"OTHER=x\n":                        "",
		"":                                 "",
		"# OPENROUTER_API_KEY=commented\n": "",
	}
	t.Setenv(EnvVar, "")
	dir := t.TempDir()
	for content, want := range cases {
		path := filepath.Join(dir, ".env")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := Key(path, EnvVar); got != want {
			t.Errorf("Key(%q) = %q, want %q", content, got, want)
		}
	}
}

func TestKeyMissingFile(t *testing.T) {
	t.Setenv(EnvVar, "")
	if got := Key(filepath.Join(t.TempDir(), "nope"), EnvVar); got != "" {
		t.Errorf("Key on missing file = %q, want empty", got)
	}
}

func TestSaveKeyCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := SaveKey(path, EnvVar, "sk-or-v1-new"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "export OPENROUTER_API_KEY=sk-or-v1-new\n" {
		t.Errorf("content = %q", raw)
	}
	fi, _ := os.Stat(path)
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %o, want 600", perm)
	}
}

func TestSaveKeyPreservesOtherEntriesAndReplaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	before := "# secrets\nexport LINEAR_API_KEY=lin\nexport OPENROUTER_API_KEY=old\nexport OTHER=keep\n"
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveKey(path, EnvVar, "sk-or-v1-new"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	want := "# secrets\nexport LINEAR_API_KEY=lin\nexport OPENROUTER_API_KEY=sk-or-v1-new\nexport OTHER=keep\n"
	if string(raw) != want {
		t.Errorf("content:\n%q\nwant:\n%q", raw, want)
	}
}

func TestSaveKeyAppendsWhenAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("export LINEAR_API_KEY=lin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveKey(path, EnvVar, "sk"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	want := "export LINEAR_API_KEY=lin\nexport OPENROUTER_API_KEY=sk\n"
	if string(raw) != want {
		t.Errorf("content = %q, want %q", raw, want)
	}
	// Round-trip: the file SaveKey writes must be readable by Key.
	t.Setenv(EnvVar, "")
	if got := Key(path, EnvVar); got != "sk" {
		t.Errorf("Key after SaveKey = %q", got)
	}
}

// grove-104: the key-file helpers are var-agnostic — saving one profile's
// var must never disturb another's, and each resolves independently.
func TestSaveKeyMultipleVars(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := SaveKey(path, EnvVar, "sk-or-old"); err != nil {
		t.Fatal(err)
	}
	if err := SaveKey(path, "KIMI_API_KEY", "kimi-1"); err != nil {
		t.Fatal(err)
	}
	if err := SaveKey(path, EnvVar, "sk-or-new"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	want := "export OPENROUTER_API_KEY=sk-or-new\nexport KIMI_API_KEY=kimi-1\n"
	if string(raw) != want {
		t.Errorf("content = %q, want %q", raw, want)
	}
	t.Setenv(EnvVar, "")
	t.Setenv("KIMI_API_KEY", "")
	if got := Key(path, EnvVar); got != "sk-or-new" {
		t.Errorf("Key(%s) = %q", EnvVar, got)
	}
	if got := Key(path, "KIMI_API_KEY"); got != "kimi-1" {
		t.Errorf("Key(KIMI_API_KEY) = %q", got)
	}
	// A var sharing a suffix must not match (prefix discipline).
	if got := Key(path, "API_KEY"); got != "" {
		t.Errorf("Key(API_KEY) = %q, want empty", got)
	}
}

func TestMask(t *testing.T) {
	cases := map[string]string{
		"sk-or-v1-f9b1234567890abcdefa6a": "sk-or-v1-f9b...a6a",
		"short":                           "shor…",
		"ab":                              "…",
		"":                                "…",
	}
	for in, want := range cases {
		if got := Mask(in); got != want {
			t.Errorf("Mask(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFetchCreditsAndKeyUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/credits":
			_, _ = w.Write([]byte(`{"data":{"total_credits":40.0,"total_usage":27.19}}`))
		case "/v1/key":
			_, _ = w.Write([]byte(`{"data":{"label":"k","usage_daily":0.5,"usage_weekly":7.94,"usage_monthly":27.19}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c, err := FetchCredits(srv.URL, "good")
	if err != nil {
		t.Fatal(err)
	}
	if c.TotalCredits != 40 || c.TotalUsage != 27.19 {
		t.Errorf("credits = %+v", c)
	}
	if got := c.Remaining(); got < 12.80 || got > 12.82 {
		t.Errorf("remaining = %f", got)
	}

	u, err := FetchKeyUsage(srv.URL, "good")
	if err != nil {
		t.Fatal(err)
	}
	if u.Daily != 0.5 || u.Weekly != 7.94 || u.Monthly != 27.19 {
		t.Errorf("usage = %+v", u)
	}

	if _, err := FetchCredits(srv.URL, "bad"); err == nil {
		t.Error("non-200 credits fetch should error")
	}
	if err := ValidateKey(srv.URL, "bad"); err == nil {
		t.Error("ValidateKey should reject a non-200")
	}
	if err := ValidateKey(srv.URL, "good"); err != nil {
		t.Errorf("ValidateKey(good) = %v", err)
	}
}
