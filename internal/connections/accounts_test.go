package connections

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/account"
	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/state"
)

func accountEnv(t *testing.T) (Env, account.Store) {
	t.Helper()
	state, home := t.TempDir(), t.TempDir()
	return Env{AccountsStateDir: state, Home: home, Stat: os.Stat, ReadFile: os.ReadFile, Getenv: func(string) string { return "" }}, account.Open(state)
}

// installShim writes a ~/.claude/settings.json whose status line is the
// gv statusline shim, and the grove config recording what it wraps.
func installShim(t *testing.T, home string) {
	t.Helper()
	writeShimSettings(t, home)
	if err := config.SaveStatuslineWrap(filepath.Join(home, ".config", "grove", "config.yaml"), "echo mine"); err != nil {
		t.Fatal(err)
	}
}

func writeShimSettings(t *testing.T, home string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{"statusLine":{"type":"command","command":"/home/u/go/bin/gv statusline"}}`
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

func accountRowIDs(env Env, now time.Time) string {
	var ids []string
	for _, c := range accountConnections(env, now) {
		want := StateWarn
		if strings.HasPrefix(c.ID, "account-token-expired:") || strings.HasPrefix(c.ID, "account-pin-missing:") {
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
	installShim(t, env.Home)
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
	want := "account-token-mode:alt,account-active-missing,account-shell-init,account-statusline,account-token-expiry:alt"
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
	installShim(t, env.Home)
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

func TestAccountStatuslineRow(t *testing.T) {
	env, s := accountEnv(t)
	has := func() bool {
		for _, c := range accountConnections(env, time.Now()) {
			if c.ID == "account-statusline" {
				return true
			}
		}
		return false
	}
	installShim(t, env.Home) // the shim alone, no accounts: silent
	if has() {
		t.Fatal("statusline row with no accounts")
	}
	if _, err := s.Add("alt", strings.NewReader("tok"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if has() {
		t.Fatal("statusline row with the shim installed")
	}
	other := `{"statusLine":{"type":"command","command":"bash ~/.claude/statusline-command.sh"}}`
	if err := os.WriteFile(filepath.Join(env.Home, ".claude", "settings.json"), []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}
	if !has() {
		t.Fatal("no statusline row: accounts exist and the shim is not installed")
	}
}

// The shim with no recorded wrap is a blank status line: flagged with or
// without accounts, silent once the wrap is recorded.
func TestStatuslineWrapMissingRow(t *testing.T) {
	env, _ := accountEnv(t)
	writeShimSettings(t, env.Home)
	rows := func() string {
		var ids []string
		for _, c := range accountConnections(env, time.Now()) {
			ids = append(ids, c.ID)
		}
		return strings.Join(ids, ",")
	}
	if got := rows(); got != "statusline-wrap-missing" {
		t.Fatalf("rows = %q, want statusline-wrap-missing", got)
	}
	installShim(t, env.Home)
	if got := rows(); got != "" {
		t.Fatalf("rows = %q once wrap is recorded, want none", got)
	}
}

// The row car 02 deferred (design §Decision 7): a non-done task pinning a
// missing account is a fail — its adopt refuses. Done tasks and pins to
// configured accounts stay silent.
func TestAccountRowPinMissing(t *testing.T) {
	env, s := accountEnv(t)
	noTasks := func() []*state.Task { t.Fatal("AccountTasks folded with no accounts configured"); return nil }
	env.AccountTasks = noTasks
	if got := accountRowIDs(env, time.Now()); got != "" {
		t.Fatalf("no accounts: rows %q, want none", got)
	}
	if _, err := s.Add("alt", strings.NewReader("tok"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.Home, ".bashrc"), []byte("eval \"$(gv account shell-init)\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	installShim(t, env.Home)
	called := false
	env.AccountTasks = func() []*state.Task {
		called = true
		return []*state.Task{
			{Ticket: "T-2", Account: "gone"},
			{Ticket: "T-1", Account: "gone"},
			{Ticket: "T-3", Account: "alt"},
			{Ticket: "T-4", Account: "old", Done: true},
			{Ticket: "T-5"},
		}
	}
	if got := accountRowIDs(env, time.Now()); got != "account-pin-missing:gone" || !called {
		t.Fatalf("rows = %q, want only account-pin-missing:gone", got)
	}
	for _, c := range accountConnections(env, time.Now()) {
		if info := c.Check(env).Info; !strings.Contains(info, "T-1, T-2") {
			t.Errorf("info = %q, want the sorted pinning tickets", info)
		}
	}
}
