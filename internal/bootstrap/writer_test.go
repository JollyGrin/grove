package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const handEdited = `# my precious comment
provider:
  kind: markdown
repos:
  app:
    path: /home/x/app
    base: main
    setup: pnpm install --frozen   # hand-tuned, do not lose
    my_unknown_key: kept
notify:
  ntfy: https://ntfy.sh/secret
`

func loadDoc(t *testing.T, content string) (*Doc, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := LoadDoc(path)
	if err != nil {
		t.Fatal(err)
	}
	return d, path
}

func TestDocNoOpNeverRewrites(t *testing.T) {
	d, path := loadDoc(t, handEdited)
	// Setting the same values must not dirty the doc.
	d.SetRepoField("app", "base", "main")
	d.Set("markdown", "provider", "kind")
	if d.Dirty() {
		t.Fatal("no-op sets must not dirty the doc")
	}
	if err := d.Save(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != handEdited {
		t.Error("no-op save must leave the file byte-identical")
	}
}

func TestDocFieldMergePreservesEverythingElse(t *testing.T) {
	d, path := loadDoc(t, handEdited)
	d.SetRepoField("app", "claude", "claude --dangerously-skip-permissions")
	if !d.Dirty() {
		t.Fatal("real change must dirty the doc")
	}
	if err := d.Save(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	got := string(raw)
	for _, want := range []string{
		"my precious comment",
		"hand-tuned, do not lose",
		"pnpm install --frozen",
		"my_unknown_key: kept",
		"ntfy.sh/secret",
		"claude --dangerously-skip-permissions",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("field merge lost %q:\n%s", want, got)
		}
	}
	// Re-load: values readable through Get.
	d2, err := LoadDoc(path)
	if err != nil {
		t.Fatal(err)
	}
	if d2.Get("repos", "app", "setup") != "pnpm install --frozen" {
		t.Errorf("Get(setup) = %q", d2.Get("repos", "app", "setup"))
	}
	if d2.Get("repos", "app", "claude") == "" {
		t.Error("new field not persisted")
	}
}

func TestDocCreatesIntermediateMappings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	d, err := LoadDoc(path) // missing file → empty doc
	if err != nil {
		t.Fatal(err)
	}
	d.Set("markdown", "provider", "kind")
	d.SetRepoField("fresh", "path", "/tmp/fresh")
	if err := d.Save(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	got := string(raw)
	if strings.Contains(got, "{") {
		t.Errorf("must emit block style, got:\n%s", got)
	}
	d2, _ := LoadDoc(path)
	if d2.Get("repos", "fresh", "path") != "/tmp/fresh" {
		t.Errorf("round-trip: %q", d2.Get("repos", "fresh", "path"))
	}
}

// TestDocSettingsFreeConfig: every shape of a config file that carries no
// settings must load clean and round-trip a Set. yaml.v3 parses the first
// three to a document with no Content and the rest to a lone !!null
// scalar — the shape that slipped the original guard (grove-201).
func TestDocSettingsFreeConfig(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"empty", ""},
		{"comment only", "# just a comment\n"},
		{"whitespace only", "   \n  \n"},
		{"null", "null\n"},
		{"tilde", "~\n"},
		{"bare doc marker", "---\n"},
		{"whitespace then doc marker", "  \n---\n"},
		{"comment then doc marker", "# just a comment\n---\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			d, err := LoadDoc(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := d.Get("linear", "api_key_env"); got != "" {
				t.Errorf("Get on settings-free doc = %q, want empty", got)
			}
			d.Set("linear-api", "linear", "api_key_env")
			d.Set("markdown", "provider", "kind")
			if err := d.Save(); err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(path)
			if got := string(raw); !strings.Contains(got, "linear:") || !strings.Contains(got, "provider:") {
				t.Errorf("round-trip lost settings, file is:\n%s", got)
			}
			// Reload: the settings must survive a parse, not just an emit.
			d2, err := LoadDoc(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := d2.Get("linear", "api_key_env"); got != "linear-api" {
				t.Errorf("reload: linear.api_key_env = %q, want linear-api", got)
			}
			if got := d2.Get("provider", "kind"); got != "markdown" {
				t.Errorf("reload: provider.kind = %q, want markdown", got)
			}
		})
	}
}

// A top-level scalar or sequence holds content a Set would clobber, so it
// is rejected rather than silently overwritten.
func TestDocNonMappingRoot(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"scalar", "hello\n"},
		{"sequence", "- a\n- b\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadDoc(path); err == nil || !strings.Contains(err.Error(), "not a mapping") {
				t.Fatalf("LoadDoc(%q) err = %v, want a not-a-mapping error", tc.body, err)
			}
			raw, _ := os.ReadFile(path)
			if string(raw) != tc.body {
				t.Errorf("file was rewritten: %q", string(raw))
			}
		})
	}
}

// Save refuses to write a document whose root the emitter would drop,
// rather than reporting success with the settings gone.
func TestDocSaveRejectsNonMappingRoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	d := &Doc{path: path, dirty: true}
	if err := yaml.Unmarshal([]byte("null\n"), &d.node); err != nil {
		t.Fatal(err)
	}
	if err := d.Save(); err == nil || !strings.Contains(err.Error(), "not a mapping") {
		t.Fatalf("Save() err = %v, want a not-a-mapping error", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("Save() wrote the file anyway: %v", err)
	}
}
