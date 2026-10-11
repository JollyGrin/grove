package connections

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/account"
)

func accountEnv(t *testing.T) (Env, account.Store) {
	t.Helper()
	state, home := t.TempDir(), t.TempDir()
	return Env{AccountsStateDir: state, Home: home, Stat: os.Stat, ReadFile: os.ReadFile, Getenv: func(string) string { return "" }}, account.Open(state)
}

func accountRowIDs(env Env, now time.Time) string {
	var ids []string
	for _, c := range accountConnections(env, now) {
		want := StateWarn
		if strings.HasPrefix(c.ID, "account-token-expired:") {
			want = StateMissing
			if c.Severity != SeverityError {
				ids = append(ids, "BAD-SEVERITY:"+c.ID)
				continue
			}
		}
		if st := c.Check(env); st.State != want || strings.Contains(st.Info, "ZZZSENTINEL") {
			ids = append(ids, "BAD:"+c.ID)
			continue
		}
		ids = append(ids, c.ID)
	}
	return strings.Join(ids, ",")
}

func TestAccountRowsSilentWhenFine(t *testing.T) {
	env, s := accountEnv(t)
	now := time.Now()
	if got := accountRowIDs(env, now); got != "" {
		t.Fatalf("no accounts: rows %q, want none", got)
	}
	if got := accountRowIDs(Env{}, now); got != "" {
		t.Fatalf("fake env: rows %q, want none", got)
	}
	if _, err := s.Add("alt", strings.NewReader("ZZZSENTINEL"), now); err != nil {
		t.Fatal(err)
	}
	if err := s.Use("alt"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.Home, ".bashrc"), []byte("eval \"$(gv account shell-init)\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := accountRowIDs(env, now); got != "" {
		t.Fatalf("healthy: rows %q, want none", got)
	}
}

func TestAccountRowsEachProblem(t *testing.T) {
	env, s := accountEnv(t)
	now := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	if _, err := s.Add("alt", strings.NewReader("ZZZSENTINEL"), now.AddDate(0, 0, -account.ExpiryWarnDays)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add("fresh", strings.NewReader("tok"), now.AddDate(0, 0, -account.ExpiryWarnDays+1)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(s.Dir, "alt.token"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "active"), []byte("gone\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := "account-token-mode:alt,account-active-missing,account-shell-init,account-token-expiry:alt"
	if got := accountRowIDs(env, now); got != want {
		t.Fatalf("rows = %q\nwant   %q", got, want)
	}
}

func TestAccountRowExpiredIsAFailNotTheWarn(t *testing.T) {
	env, s := accountEnv(t)
	now := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	if _, err := s.Add("dead", strings.NewReader("tok"), now.AddDate(0, 0, -account.ExpiryDays)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.Home, ".zshrc"), []byte("eval \"$(gv account shell-init)\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := accountRowIDs(env, now); got != "account-token-expired:dead" {
		t.Fatalf("rows = %q, want only the expired fail row", got)
	}
}

func TestAccountShellInitRowCommentsAndShell(t *testing.T) {
	env, s := accountEnv(t)
	if _, err := s.Add("alt", strings.NewReader("tok"), time.Now()); err != nil {
		t.Fatal(err)
	}
	rc := "# eval \"$(gv account shell-init)\"\n   #eval \"$(gv account shell-init)\"\n"
	if err := os.WriteFile(filepath.Join(env.Home, ".bashrc"), []byte(rc), 0o644); err != nil {
		t.Fatal(err)
	}
	fixFor := func(shell string) string {
		env.Getenv = func(k string) string {
			if k == "SHELL" {
				return shell
			}
			return ""
		}
		for _, c := range accountConnections(env, time.Now()) {
			if c.ID == "account-shell-init" {
				return c.Fix
			}
		}
		return ""
	}
	if fix := fixFor("/usr/bin/zsh"); !strings.HasSuffix(fix, ">> ~/.zshrc") {
		t.Errorf("zsh fix = %q, want ~/.zshrc (commented lines must not count as installed)", fix)
	}
	if fix := fixFor("/bin/bash"); !strings.HasSuffix(fix, ">> ~/.bashrc") {
		t.Errorf("bash fix = %q, want ~/.bashrc", fix)
	}
}
