package bootstrap

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func initRepo(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", branch},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
		{"commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// macOS: /var/folders symlinks to /private/var — normalize like git does.
	real, _ := filepath.EvalSymlinks(dir)
	return real
}

type cfgShape struct {
	Provider struct {
		Kind string `yaml:"kind"`
	} `yaml:"provider"`
	Repos map[string]struct {
		Path string `yaml:"path"`
		Base string `yaml:"base"`
	} `yaml:"repos"`
}

func readCfg(t *testing.T, path string) cfgShape {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var c cfgShape
	if err := yaml.Unmarshal(raw, &c); err != nil {
		t.Fatalf("config unparseable: %v\n%s", err, raw)
	}
	return c
}

func TestRunFreshEverything(t *testing.T) {
	repo := initRepo(t, "main")
	cfgPath := filepath.Join(t.TempDir(), "grove", "config.yaml")

	res, err := Run(repo, cfgPath, "2026-07-04")
	if err != nil {
		t.Fatal(err)
	}
	if !res.WroteConfig || !res.WroteSample {
		t.Errorf("fresh init should write config and sample: %+v", res)
	}
	c := readCfg(t, cfgPath)
	if c.Provider.Kind != "markdown" {
		t.Errorf("provider.kind = %q", c.Provider.Kind)
	}
	name := filepath.Base(repo)
	if r, ok := c.Repos[name]; !ok || r.Path != repo || r.Base != "main" {
		t.Errorf("repo entry = %+v (want path=%s base=main)", c.Repos, repo)
	}
	sample, err := os.ReadFile(filepath.Join(repo, ".grove", "tasks", "task-001.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sample), "id: task-001") || !strings.Contains(string(sample), "2026-07-04") {
		t.Errorf("sample task content:\n%s", sample)
	}
}

func TestRunMasterBranchRepo(t *testing.T) {
	repo := initRepo(t, "master")
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	res, err := Run(repo, cfgPath, "2026-07-04")
	if err != nil {
		t.Fatal(err)
	}
	if res.Base != "master" {
		t.Errorf("base = %q, want master", res.Base)
	}
}

func TestRunIdempotent(t *testing.T) {
	repo := initRepo(t, "main")
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if _, err := Run(repo, cfgPath, "2026-07-04"); err != nil {
		t.Fatal(err)
	}
	// Mutate the sample so a re-run would be caught overwriting it.
	taskPath := filepath.Join(repo, ".grove", "tasks", "task-001.md")
	if err := os.WriteFile(taskPath, []byte("---\nid: task-001\ntitle: edited\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(cfgPath)

	res, err := Run(repo, cfgPath, "2026-07-05")
	if err != nil {
		t.Fatal(err)
	}
	if res.WroteConfig || res.WroteSample {
		t.Errorf("re-run must be a no-op: %+v", res)
	}
	after, _ := os.ReadFile(cfgPath)
	if string(before) != string(after) {
		t.Error("re-run changed the config file")
	}
	edited, _ := os.ReadFile(taskPath)
	if !strings.Contains(string(edited), "edited") {
		t.Error("re-run clobbered a user-edited task file")
	}
}

func TestRunPreservesExistingConfig(t *testing.T) {
	repo := initRepo(t, "main")
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.yaml")
	existing := "# my precious comment\nprovider:\n  kind: linear\nlinear:\n  api_key_env: MY_KEY\nrepos:\n  other:\n    path: /somewhere\n    base: develop\nnotify:\n  ntfy: https://ntfy.sh/x\n"
	if err := os.WriteFile(cfgPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Run(repo, cfgPath, "2026-07-04")
	if err != nil {
		t.Fatal(err)
	}
	if !res.WroteConfig {
		t.Error("new repo should be appended")
	}
	raw, _ := os.ReadFile(cfgPath)
	got := string(raw)
	for _, want := range []string{"my precious comment", "kind: linear", "MY_KEY", "/somewhere", "develop", "ntfy.sh/x", filepath.Base(repo)} {
		if !strings.Contains(got, want) {
			t.Errorf("existing config lost %q:\n%s", want, got)
		}
	}
}

func TestRunOutsideGitRepo(t *testing.T) {
	dir := t.TempDir()
	if _, err := Run(dir, filepath.Join(dir, "config.yaml"), "2026-07-04"); err == nil || !strings.Contains(err.Error(), "git repo") {
		t.Errorf("want inside-a-git-repo error, got %v", err)
	}
}
