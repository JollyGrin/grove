package account

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/state"
)

const sentinel = "ZZZSENTINEL"

var now = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func newStore(t *testing.T) Store {
	t.Helper()
	return Open(t.TempDir())
}

func mustAdd(t *testing.T, s Store, name, tok string, at time.Time) {
	t.Helper()
	if _, err := s.Add(name, strings.NewReader(tok+"\n"), at); err != nil {
		t.Fatalf("Add(%s): %v", name, err)
	}
}

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"alt", "a", "max-2", "a" + strings.Repeat("b", 31)} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"login", "Bad_Name", "", "2alt", "-x", "a" + strings.Repeat("b", 32), "a.b", "a/b"} {
		if err := ValidateName(bad); !errors.Is(err, ErrUsage) {
			t.Errorf("ValidateName(%q) = %v, want ErrUsage", bad, err)
		}
	}
}

func TestAddWritesPrivateFiles(t *testing.T) {
	s := newStore(t)
	replaced, err := s.Add("alt", strings.NewReader("  "+sentinel+"\n"), now)
	if err != nil || replaced {
		t.Fatalf("Add = %v, %v", replaced, err)
	}
	fi, err := os.Stat(s.Dir)
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, %v; want 0700", fi.Mode().Perm(), err)
	}
	p := filepath.Join(s.Dir, "alt.token")
	fi, err = os.Stat(p)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("token mode = %v, %v; want 0600", fi.Mode().Perm(), err)
	}
	raw, _ := os.ReadFile(p)
	if string(raw) != sentinel+"\n" {
		t.Fatalf("token file = %q, want trimmed value + newline", raw)
	}
	accts, _ := s.List()
	if len(accts) != 1 || !accts[0].Minted.Equal(now) {
		t.Fatalf("List = %+v, want alt minted %v", accts, now)
	}
	// Re-add replaces (renewal) and re-stamps the mint date.
	later := now.Add(48 * time.Hour)
	replaced, err = s.Add("alt", strings.NewReader("NEWTOKEN"), later)
	if err != nil || !replaced {
		t.Fatalf("re-Add = %v, %v; want replaced", replaced, err)
	}
	accts, _ = s.List()
	if !accts[0].Minted.Equal(later) {
		t.Fatalf("minted after re-add = %v, want %v", accts[0].Minted, later)
	}
}

func TestAddRefusals(t *testing.T) {
	s := newStore(t)
	for _, name := range []string{"login", "Bad_Name"} {
		if _, err := s.Add(name, strings.NewReader(sentinel), now); !errors.Is(err, ErrUsage) {
			t.Errorf("Add(%q) = %v, want ErrUsage", name, err)
		}
	}
	for _, in := range []string{"", "  \n", sentinel + " trailing words", strings.Repeat("x", maxTokenBytes+1)} {
		_, err := s.Add("alt", strings.NewReader(in), now)
		if err == nil {
			t.Errorf("Add(%.20q) = nil, want error", in)
			continue
		}
		if strings.Contains(err.Error(), sentinel) || strings.Contains(err.Error(), "xxxx") {
			t.Errorf("error echoes the input: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "alt.token")); err == nil {
		t.Fatal("a refused add wrote a token")
	}
}

func TestUseActiveAndTokenPath(t *testing.T) {
	s := newStore(t)
	if p, err := s.ActiveTokenPath(); p != "" || err != nil {
		t.Fatalf("fresh ActiveTokenPath = %q, %v; want login", p, err)
	}
	mustAdd(t, s, "alt", sentinel, now)
	err := s.Use("nosuch")
	if err == nil || !strings.Contains(err.Error(), "login, alt") {
		t.Fatalf("Use(nosuch) = %v, want an error listing login, alt", err)
	}
	if err := s.Use("Bad_Name"); !errors.Is(err, ErrUsage) {
		t.Fatalf("Use(Bad_Name) = %v, want ErrUsage", err)
	}
	if err := s.Use("alt"); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Active(); a != "alt" {
		t.Fatalf("Active = %q, want alt", a)
	}
	p, err := s.ActiveTokenPath()
	if err != nil || p != filepath.Join(s.Dir, "alt.token") {
		t.Fatalf("ActiveTokenPath = %q, %v", p, err)
	}
	if err := s.Use(Login); err != nil {
		t.Fatal(err)
	}
	if p, err := s.ActiveTokenPath(); p != "" || err != nil {
		t.Fatalf("after use login: %q, %v", p, err)
	}
	if err := s.Use(Login); err != nil { // idempotent
		t.Fatal(err)
	}
}

func TestActiveNamingMissingAccountErrors(t *testing.T) {
	s := newStore(t)
	mustAdd(t, s, "alt", sentinel, now)
	if err := os.WriteFile(filepath.Join(s.Dir, "active"), []byte("gone\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := s.ActiveTokenPath()
	if p != "" || err == nil {
		t.Fatalf("ActiveTokenPath = %q, %v; want an error, never a silent login", p, err)
	}
}

func TestRemove(t *testing.T) {
	s := newStore(t)
	mustAdd(t, s, "alt", sentinel, now)
	mustAdd(t, s, "max", sentinel, now)
	if err := s.Use("alt"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Remove("nosuch"); err == nil {
		t.Fatal("Remove(nosuch) = nil")
	}
	if _, err := s.Remove(Login); !errors.Is(err, ErrUsage) {
		t.Fatalf("Remove(login) = %v, want ErrUsage", err)
	}
	was, err := s.Remove("alt")
	if err != nil || !was {
		t.Fatalf("Remove(alt) = %v, %v; want wasActive", was, err)
	}
	if a, _ := s.Active(); a != "" {
		t.Fatalf("Active after removing it = %q, want login", a)
	}
	for _, f := range []string{"alt.token", "alt.minted"} {
		if _, err := os.Stat(filepath.Join(s.Dir, f)); err == nil {
			t.Errorf("%s survived rm", f)
		}
	}
	if names := s.Names(); strings.Join(names, ",") != "login,max" {
		t.Fatalf("Names = %v", names)
	}
}

func TestInUse(t *testing.T) {
	tasks := []*state.Task{
		{Ticket: "b-2", Account: "alt"},
		{Ticket: "a-1", Account: "alt"},
		{Ticket: "c-3", Account: "alt", Done: true},
		{Ticket: "d-4"},
		nil,
	}
	if got := InUse(tasks, "alt"); strings.Join(got, ",") != "a-1,b-2" {
		t.Fatalf("InUse = %v", got)
	}
}

func TestRowsAndEmail(t *testing.T) {
	s := newStore(t)
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".claude.json"),
		[]byte(`{"oauthAccount":{"emailAddress":"me@example.com"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	mustAdd(t, s, "alt", sentinel, now.Add(-40*24*time.Hour))
	rows, err := s.Rows(home, now, []*state.Task{{Ticket: "x-1", Account: "alt"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []Row{
		{Name: "login", Active: true, Email: "me@example.com", Connectors: true},
		{Name: "alt", TokenAgeDays: 40, Pinned: 1},
	}
	if len(rows) != 2 || rows[0] != want[0] || rows[1] != want[1] {
		t.Fatalf("Rows = %+v, want %+v", rows, want)
	}
	if err := s.Use("alt"); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.Rows(t.TempDir(), now, nil)
	if rows[0].Active || !rows[1].Active || rows[0].Email != "" {
		t.Fatalf("after use alt: %+v", rows)
	}
}
