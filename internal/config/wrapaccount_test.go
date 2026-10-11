package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWrapAccountGolden(t *testing.T) {
	got := WrapAccount(`claude --model 'opus' "$(cat "/s/prompts/X-1.txt")"`, "/s/accounts/alt.token", "alt")
	want := `( t="$(cat '/s/accounts/alt.token')" && [ -n "$t" ] || { echo 'gv: account alt token unreadable — gv account ls' >&2; exit 1; }; export CLAUDE_CODE_OAUTH_TOKEN="$t" GROVE_ACCOUNT='alt' && exec claude --model 'opus' "$(cat "/s/prompts/X-1.txt")" )`
	if got != want {
		t.Fatalf("WrapAccount =\n%s\nwant\n%s", got, want)
	}
}

func TestWrapAccountEmptyPathPassthrough(t *testing.T) {
	const cmd = `claude "$(cat "/p")"`
	if got := WrapAccount(cmd, "", "alt"); got != cmd {
		t.Fatalf("WrapAccount(empty path) = %q, want the command unchanged", got)
	}
}

func TestWrapAccountQuotesPath(t *testing.T) {
	got := WrapAccount("claude", "/o'dd dir/a.token", "alt")
	if !strings.Contains(got, `cat '/o'\''dd dir/a.token'`) {
		t.Fatalf("path not shell-quoted: %s", got)
	}
}

// TestWrapAccountRunsInShell: the rendered line carries the path but never
// the token, yet the shell it runs in exports it — and an empty token
// file refuses instead of running on the login.
func TestWrapAccountRunsInShell(t *testing.T) {
	dir := t.TempDir()
	tok := filepath.Join(dir, "alt.token")
	if err := os.WriteFile(tok, []byte("ZZZSENTINEL\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	line := WrapAccount(`printf '%s|%s' "${CLAUDE_CODE_OAUTH_TOKEN:+set}" "$GROVE_ACCOUNT"`, tok, "alt")
	if !strings.Contains(line, tok) || strings.Contains(line, "ZZZSENTINEL") {
		t.Fatalf("line must carry the path, never the token: %s", line)
	}
	out, err := exec.Command("sh", "-c", line).Output()
	if err != nil || string(out) != "set|alt" {
		t.Fatalf("sh -c wrapped = %q, %v; want set|alt", out, err)
	}
	for _, setup := range []func() error{
		func() error { return os.WriteFile(tok, nil, 0o600) },
		func() error { return os.Remove(tok) },
	} {
		if err := setup(); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("sh", "-c", line)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err == nil || len(out) != 0 {
			t.Fatalf("empty/unreadable token ran anyway: %q, %v", out, err)
		}
		if !strings.Contains(stderr.String(), "gv: account alt token unreadable — gv account ls") {
			t.Fatalf("no reason on stderr: %q", stderr.String())
		}
	}
}
