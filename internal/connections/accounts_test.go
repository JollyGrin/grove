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
	return Env{AccountsStateDir: state, Home: home, Stat: os.Stat, ReadFile: os.ReadFile}, account.Open(state)
}

func accountRowIDs(env Env, now time.Time) string {
	var ids []string
	for _, c := range accountConnections(env, now) {
		if st := c.Check(env); st.State != StateWarn || strings.Contains(st.Info, "ZZZSENTINEL") {
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
