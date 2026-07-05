package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/config"
)

// loadConfig round-trips a yaml body through config.Load's defaulting by
// writing it to a scratch HOME.
func loadConfig(t *testing.T, body string) *config.Config {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "grove")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestFromConfigDefaultsToMarkdown(t *testing.T) {
	repo := t.TempDir()
	cfg := loadConfig(t, "repos:\n  app:\n    path: "+repo+"\n")
	p, err := FromConfig(cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind() != "markdown" {
		t.Errorf("kind = %s, want markdown (the zero-config default)", p.Kind())
	}
	if !p.Capabilities().CanList {
		t.Error("markdown provider must support listing")
	}
	m := p.(*Markdown)
	if want := filepath.Join(repo, ".grove", "tasks"); m.dir != want {
		t.Errorf("dir = %s, want %s", m.dir, want)
	}
}

func TestFromConfigLinear(t *testing.T) {
	repo := t.TempDir()
	cfg := loadConfig(t, "provider:\n  kind: linear\nlinear:\n  api_key_env: GROVE_TEST_LINEAR_KEY\nrepos:\n  app:\n    path: "+repo+"\n")

	// Missing key → the api_key_env indirection error surfaces.
	os.Unsetenv("GROVE_TEST_LINEAR_KEY")
	if _, err := FromConfig(cfg, repo); err == nil || !strings.Contains(err.Error(), "GROVE_TEST_LINEAR_KEY") {
		t.Errorf("want missing-key error naming the env var, got %v", err)
	}

	t.Setenv("GROVE_TEST_LINEAR_KEY", "lin_api_test")
	p, err := FromConfig(cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind() != "linear" || p.Capabilities().CanList {
		t.Errorf("kind=%s canList=%v", p.Kind(), p.Capabilities().CanList)
	}
	if id, err := p.ParseID("https://linear.app/grid/issue/DEV-1234/some-slug"); err != nil || id != "DEV-1234" {
		t.Errorf("ParseID = %q, %v", id, err)
	}
	if id, err := p.ParseID("dev-77"); err != nil || id != "DEV-77" {
		t.Errorf("lowercase linear id: %q, %v", id, err)
	}
}

func TestFromConfigUnknownKind(t *testing.T) {
	repo := t.TempDir()
	cfg := loadConfig(t, "provider:\n  kind: jira\nrepos:\n  app:\n    path: "+repo+"\n")
	if _, err := FromConfig(cfg, repo); err == nil || !strings.Contains(err.Error(), "jira") {
		t.Errorf("want unknown-kind error, got %v", err)
	}
}

func TestProviderKindForOverride(t *testing.T) {
	repoA := t.TempDir()
	repoB := t.TempDir()
	cfg := loadConfig(t, "provider:\n  kind: linear\nrepos:\n  grid:\n    path: "+repoA+"\n  side:\n    path: "+repoB+"\n    provider: markdown\n")
	if got := cfg.ProviderKindFor(cfg.Repos["grid"]); got != "linear" {
		t.Errorf("no override must inherit global, got %s", got)
	}
	if got := cfg.ProviderKindFor(cfg.Repos["side"]); got != "markdown" {
		t.Errorf("override must win, got %s", got)
	}
	if got := cfg.ProviderKindFor(nil); got != "linear" {
		t.Errorf("nil repo falls back to global, got %s", got)
	}
	p, err := FromConfigKind(cfg, "markdown", "side", repoB)
	if err != nil || p.Kind() != "markdown" {
		t.Errorf("FromConfigKind override: %v %v", p, err)
	}
}

func TestProviderKindValidation(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "grove")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "repos:\n  x:\n    path: " + repo + "\n    provider: jira\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), "jira") {
		t.Errorf("invalid per-repo provider must fail load, got %v", err)
	}
}

func TestIDCandidates(t *testing.T) {
	cases := map[string][]string{
		"DEV-1234":                               {"DEV-1234", "dev-1234"}, // linear shape; lowercase is also a valid md slug
		"task-001":                               {"TASK-001", "task-001"}, // md id uppercases into the linear regex too
		"https://linear.app/x/issue/DEV-77/slug": {"DEV-77"},
		"!!!":                                    nil,
	}
	for raw, want := range cases {
		got := IDCandidates(raw)
		if len(got) != len(want) {
			t.Errorf("IDCandidates(%q) = %v, want %v", raw, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("IDCandidates(%q) = %v, want %v", raw, got, want)
			}
		}
	}
}
