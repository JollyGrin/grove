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
