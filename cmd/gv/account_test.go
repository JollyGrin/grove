package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gvAsTestBinaryEnv makes the test binary act as `gv` (TestMain below),
// so the shell-init test runs the real `gv account token-path`.
const gvAsTestBinaryEnv = "GV_TEST_RUN_AS_GV"

func TestMain(m *testing.M) {
	if os.Getenv(gvAsTestBinaryEnv) == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

const accountSentinel = "ZZZSENTINEL"

// accountSandbox is a scratch HOME + GROVE_STATE_DIR with a `gv` on PATH
// that is this test binary.
type accountSandbox struct {
	home, state, bin string
}

func newAccountSandbox(t *testing.T) accountSandbox {
	t.Helper()
	root := t.TempDir()
	sb := accountSandbox{home: filepath.Join(root, "home"), state: filepath.Join(root, "state"), bin: filepath.Join(root, "bin")}
	for _, d := range []string{sb.home, sb.state, sb.bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(sb.bin, "gv")); err != nil {
		t.Fatal(err)
	}
	return sb
}

func (sb accountSandbox) env(extra ...string) []string {
	return append([]string{
		"HOME=" + sb.home,
		"GROVE_STATE_DIR=" + sb.state,
		"PATH=" + sb.bin + ":" + os.Getenv("PATH"),
		gvAsTestBinaryEnv + "=1",
	}, extra...)
}

// gv runs `gv args...` in the sandbox and returns combined output + error.
func (sb accountSandbox) gv(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(filepath.Join(sb.bin, "gv"), args...)
	cmd.Dir = sb.home
	cmd.Env = sb.env()
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// The token value never reaches any output or error message.
func TestAccountSentinelNeverPrinted(t *testing.T) {
	sb := newAccountSandbox(t)
	if out, err := sb.gv(t, accountSentinel+"\n", "account", "add", "alt"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	runs := [][]string{
		{"account", "ls"},
		{"account", "ls", "--json"},
		{"account", "token-path"},
		{"account", "shell-init"},
		{"account", "use", "alt"},
		{"account", "token-path"},
		{"account", "ls", "--json"},
		{"account", "use", "nosuch"},
		{"account", "use", "Bad_Name"},
		{"account", "add", "login"},
		{"account", "add", "Bad_Name"},
		{"account", "rm", "nosuch"},
		{"account", "bogus"},
	}
	for _, args := range runs {
		out, _ := sb.gv(t, "", args...)
		if strings.Contains(out, accountSentinel) {
			t.Errorf("gv %s printed the token:\n%s", strings.Join(args, " "), out)
		}
	}
	// Refused adds with the sentinel as input don't echo it either.
	for _, in := range []string{accountSentinel + " extra words", ""} {
		out, err := sb.gv(t, in, "account", "add", "beta")
		if err == nil || strings.Contains(out, accountSentinel) {
			t.Errorf("add with %q: err=%v out=%s", in, err, out)
		}
	}
}

func TestAccountUsageErrors(t *testing.T) {
	sb := newAccountSandbox(t)
	if out, err := sb.gv(t, "tok\n", "account", "add", "alt"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	out, err := sb.gv(t, "", "account", "use", "nosuch")
	if err == nil || !strings.Contains(out, "login, alt") {
		t.Errorf("use nosuch: err=%v, want the configured names listed:\n%s", err, out)
	}
	for _, name := range []string{"login", "Bad_Name"} {
		out, err := sb.gv(t, "tok\n", "account", "add", name)
		if err == nil || !strings.Contains(out, "usage: gv account add") {
			t.Errorf("add %s: err=%v, want a usage error:\n%s", name, err, out)
		}
	}
}

func TestAccountLsJSON(t *testing.T) {
	sb := newAccountSandbox(t)
	if err := os.WriteFile(filepath.Join(sb.home, ".claude.json"),
		[]byte(`{"oauthAccount":{"emailAddress":"me@example.com"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := sb.gv(t, "tok\n", "account", "add", "alt"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	out, err := sb.gv(t, "", "account", "ls", "--json")
	if err != nil {
		t.Fatalf("ls --json: %v\n%s", err, out)
	}
	for _, want := range []string{`"schema_version"`, `"accounts"`, `"name": "login"`, `"email": "me@example.com"`,
		`"name": "alt"`, `"token_age_days": 0`, `"pinned": 0`, `"connectors": false`, `"active": true`, `"expired": false`} {
		if !strings.Contains(out, want) {
			t.Errorf("ls --json lacks %s:\n%s", want, out)
		}
	}
	out, _ = sb.gv(t, "", "account", "ls")
	if !strings.Contains(out, "● login") || !strings.Contains(out, "no claude.ai connectors") {
		t.Errorf("ls text:\n%s", out)
	}
	// Past a year: ls marks it EXPIRED with the remedy, use refuses it.
	if err := os.WriteFile(filepath.Join(sb.state, "accounts", "alt.minted"), []byte("2020-01-01T00:00:00Z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _ = sb.gv(t, "", "account", "ls")
	if !strings.Contains(out, "token EXPIRED") || !strings.Contains(out, "claude setup-token") {
		t.Errorf("ls text for an expired token:\n%s", out)
	}
	if out, err := sb.gv(t, "", "account", "use", "alt"); err == nil || !strings.Contains(out, "EXPIRED") {
		t.Errorf("use of an expired account: err=%v\n%s", err, out)
	}
}

// gvStdout runs `gv args...` in the sandbox and returns stdout only.
func (sb accountSandbox) gvStdout(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(filepath.Join(sb.bin, "gv"), args...)
	cmd.Dir = sb.home
	cmd.Env = sb.env()
	out, err := cmd.Output()
	return string(out), err
}

// The design §Decision 6 function, run for real: bash -n clean, and a
// fake claude reports which token it was handed.
func TestAccountShellInitRuns(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	sb := newAccountSandbox(t)
	fn, err := sb.gvStdout(t, "account", "shell-init")
	if err != nil {
		t.Fatalf("shell-init: %v\n%s", err, fn)
	}
	fnFile := filepath.Join(sb.home, "shell-init.sh")
	if err := os.WriteFile(fnFile, []byte(fn), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("bash", "-n", fnFile).CombinedOutput(); err != nil {
		t.Fatalf("bash -n: %v\n%s", err, out)
	}
	// The fake prints the token and the account the session is attributed
	// to (GROVE_ACCOUNT, what gv statusline files usage under).
	fakeClaude := []byte("#!/bin/sh\necho \"${CLAUDE_CODE_OAUTH_TOKEN:-none} ${GROVE_ACCOUNT:-none}\"\n")
	if err := os.WriteFile(filepath.Join(sb.bin, "claude"), fakeClaude, 0o755); err != nil {
		t.Fatal(err)
	}
	// run calls the function with an inherited token and GROVE_ACCOUNT in
	// the env (neither may leak through a login or pass-through path);
	// extra env wins.
	run := func(extra ...string) (stdout, stderr string, err error) {
		cmd := exec.Command("bash", "-c", `. "$1" && claude`, "bash", fnFile)
		cmd.Dir = sb.home
		cmd.Env = sb.env(append([]string{"CLAUDE_CODE_OAUTH_TOKEN=inherited", "GROVE_ACCOUNT=inherited"}, extra...)...)
		var eb strings.Builder
		cmd.Stderr = &eb
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), eb.String(), err
	}
	wantExit1 := func(what, out, stderr string, err error, stderrHas ...string) {
		t.Helper()
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 || out != "" {
			t.Errorf("%s: stdout %q, %v; want exit 1, claude never run", what, out, err)
		}
		for _, h := range stderrHas {
			if !strings.Contains(stderr, h) {
				t.Errorf("%s: stderr lacks %q:\n%s", what, h, stderr)
			}
		}
	}

	if out, _, err := run(); err != nil || out != "none none" {
		t.Errorf("login: %q, %v; want none none", out, err)
	}
	if out, err := sb.gv(t, "alt-token\n", "account", "add", "alt"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	if out, err := sb.gv(t, "", "account", "use", "alt"); err != nil {
		t.Fatalf("use: %v\n%s", err, out)
	}
	if out, _, err := run(); err != nil || out != "alt-token alt" {
		t.Errorf("active alt: %q, %v; want alt-token alt", out, err)
	}

	// ~/.claude spelled another way is still the personal dir.
	claudeDir := filepath.Join(sb.home, ".claude")
	link := filepath.Join(sb.home, "claude-link")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(claudeDir, link); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{claudeDir, claudeDir + "/", link, link + "/"} {
		if out, _, err := run("CLAUDE_CONFIG_DIR=" + d); err != nil || out != "alt-token alt" {
			t.Errorf("CLAUDE_CONFIG_DIR=%s: %q, %v; want alt-token alt", d, out, err)
		}
	}
	// A foreign dir passes through, inherited token stripped.
	if out, _, err := run("CLAUDE_CONFIG_DIR=" + filepath.Join(sb.home, ".cc-work")); err != nil || out != "none none" {
		t.Errorf("CLAUDE_CONFIG_DIR=$HOME/.cc-work: %q, %v; want none none", out, err)
	}

	// token-path failing refuses, with gv's own error on stderr.
	active := filepath.Join(sb.state, "accounts", "active")
	if err := os.WriteFile(active, []byte("gone\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, stderr, err := run()
	wantExit1("active names a missing account", out, stderr, err, `no account "gone"`, "refusing")
	if err := os.WriteFile(active, []byte("alt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A gv too old to know the verb refuses too.
	oldBin := filepath.Join(sb.home, "old-gv")
	if err := os.MkdirAll(oldBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldBin, "gv"), []byte("#!/bin/sh\necho 'unknown command \"account\"' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, stderr, err = run("PATH=" + oldBin + ":" + sb.bin + ":" + os.Getenv("PATH"))
	wantExit1("old gv", out, stderr, err, "unknown command", "refusing")
	// No gv at all: the login, token stripped.
	noGv := filepath.Join(sb.home, "no-gv")
	if err := os.MkdirAll(noGv, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(noGv, "claude"), fakeClaude, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, _, err := run("PATH=" + noGv + ":/usr/bin:/bin"); err != nil || out != "none none" {
		t.Errorf("no gv on PATH: %q, %v; want none none", out, err)
	}

	if err := os.WriteFile(filepath.Join(sb.state, "accounts", "alt.token"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	out, stderr, err = run()
	wantExit1("empty token file", out, stderr, err, "token unreadable")
}

// token-path is read-only: run from inside a workspace with no state dir
// anywhere, it creates neither the workspace's .grove/state nor the
// global state dir.
func TestAccountTokenPathReadOnly(t *testing.T) {
	sb := newAccountSandbox(t)
	ws := filepath.Join(sb.home, "ws")
	if err := os.MkdirAll(filepath.Join(ws, ".grove"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".grove", "config.yaml"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(sb.bin, "gv"), "account", "token-path")
	cmd.Dir = ws
	cmd.Env = []string{"HOME=" + sb.home, "PATH=" + os.Getenv("PATH"), gvAsTestBinaryEnv + "=1"}
	out, err := cmd.CombinedOutput()
	if err != nil || len(out) != 0 {
		t.Fatalf("token-path: %q, %v; want empty (login)", out, err)
	}
	for _, d := range []string{filepath.Join(ws, ".grove", "state"), filepath.Join(sb.home, ".local")} {
		if _, err := os.Stat(d); err == nil {
			t.Errorf("token-path created %s", d)
		}
	}
}
