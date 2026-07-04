package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
