package account

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/state"
)

func TestSetsConfigDir(t *testing.T) {
	for cmd, want := range map[string]bool{
		"claude --dangerously-skip-permissions":      false,
		"":                                           false,
		"echo":                                       false,
		"ccwork --dangerously-skip-permissions":      true,
		"CLAUDE_CONFIG_DIR=~/.cc-work claude":        true,
		"env CLAUDE_CONFIG_DIR=/x claude":            true,
		"claude-ccwork --dangerously-skip-perms":     false,
		"bash -c 'CLAUDE_CONFIG_DIR=/x exec claude'": true,
		"/home/dean/bin/ccwork --x":                  true,
		"~/bin/ccwork":                               true,
		"env FOO=1 ccwork":                           true,
		"FOO=1 ccwork":                               true,
		"env FOO=1 claude":                           false,
	} {
		if got := SetsConfigDir(cmd); got != want {
			t.Errorf("SetsConfigDir(%q) = %v, want %v", cmd, got, want)
		}
	}
}

// TestResolve covers every resolver row of the design (§Decisions 1–3).
func TestResolve(t *testing.T) {
	type want struct {
		name, pin string
		errSub    string // "" = no error
	}
	const cc = "claude --dangerously-skip-permissions"
	cases := []struct {
		desc   string
		active string // "" = login
		l      Launch
		w      want
	}{
		{"nothing configured → login", "", Launch{ClaudeCmd: cc}, want{}},
		{"active applies", "main", Launch{ClaudeCmd: cc}, want{name: "main"}},
		{"flag wins over active, pins", "main", Launch{Flag: "alt", ClaudeCmd: cc}, want{name: "alt", pin: "alt"}},
		{"flag login beats active", "main", Launch{Flag: "login", ClaudeCmd: cc}, want{}},
		{"flag login strips a pin", "main", Launch{Flag: "login", Pinned: "alt", ClaudeCmd: cc}, want{}},
		{"recorded pin beats active", "main", Launch{Pinned: "alt", ClaudeCmd: cc}, want{name: "alt", pin: "alt"}},
		{"flag beats recorded pin", "", Launch{Flag: "main", Pinned: "alt", ClaudeCmd: cc}, want{name: "main", pin: "main"}},
		{"flag + profile → error", "", Launch{Flag: "alt", Profile: "glm", ClaudeCmd: cc}, want{errSub: "model profile glm"}},
		{"flag + claude_config_dir → error", "", Launch{Flag: "alt", ConfigDir: "/h/.cc-work", ClaudeCmd: cc}, want{errSub: "claude_config_dir"}},
		{"flag + cmd sets CLAUDE_CONFIG_DIR → error", "", Launch{Flag: "alt", ClaudeCmd: "CLAUDE_CONFIG_DIR=/x claude"}, want{errSub: "CLAUDE_CONFIG_DIR"}},
		{"flag + ccwork → error", "", Launch{Flag: "alt", ClaudeCmd: "ccwork"}, want{errSub: "CLAUDE_CONFIG_DIR"}},
		{"flag + /abs/ccwork → error", "", Launch{Flag: "alt", ClaudeCmd: "/home/dean/bin/ccwork --x"}, want{errSub: "CLAUDE_CONFIG_DIR"}},
		{"flag + ~/bin/ccwork → error", "", Launch{Flag: "alt", ClaudeCmd: "~/bin/ccwork"}, want{errSub: "CLAUDE_CONFIG_DIR"}},
		{"flag + env FOO=1 ccwork → error", "", Launch{Flag: "alt", ClaudeCmd: "env FOO=1 ccwork"}, want{errSub: "CLAUDE_CONFIG_DIR"}},
		{"active skipped under /abs/ccwork", "main", Launch{ClaudeCmd: "/home/dean/bin/ccwork"}, want{}},
		{"active skipped under env FOO=1 ccwork", "main", Launch{ClaudeCmd: "env FOO=1 ccwork"}, want{}},
		{"pin + profile → error, names the override", "", Launch{Pinned: "alt", Profile: "glm", ClaudeCmd: cc, Ticket: "T-1"}, want{errSub: "gv adopt T-1 --account <name|login>"}},
		{"active skipped under profile", "main", Launch{Profile: "glm", ClaudeCmd: cc}, want{}},
		{"active skipped under claude_config_dir", "main", Launch{ConfigDir: "/h/.cc-work", ClaudeCmd: cc}, want{}},
		{"active skipped under cmd CLAUDE_CONFIG_DIR", "main", Launch{ClaudeCmd: "CLAUDE_CONFIG_DIR=/x claude"}, want{}},
		{"active skipped under ccwork", "main", Launch{ClaudeCmd: "ccwork"}, want{}},
		{"flag names missing account → error", "", Launch{Flag: "nope", ClaudeCmd: cc}, want{errSub: `no account "nope"`}},
		{"recorded pin missing → error, never login", "main", Launch{Pinned: "gone", ClaudeCmd: cc}, want{errSub: "pinned to account gone"}},
		{"recorded pin missing → names the override", "main", Launch{Pinned: "gone", ClaudeCmd: cc, Ticket: "T-9"}, want{errSub: "override: gv adopt T-9 --account <name|login>"}},
		{"flag bad name → error", "", Launch{Flag: "Bad_Name", ClaudeCmd: cc}, want{errSub: "must match"}},
		{"pinned expired → refuse with setup-token hint", "", Launch{Pinned: "old", ClaudeCmd: cc}, want{errSub: "claude setup-token"}},
		{"flag expired → refuse with setup-token hint", "", Launch{Flag: "old", ClaudeCmd: cc}, want{errSub: "EXPIRED"}},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			s := newStore(t)
			s.Now = func() time.Time { return now }
			mustAdd(t, s, "main", sentinel, now)
			mustAdd(t, s, "alt", sentinel, now)
			mustAdd(t, s, "old", sentinel, now.AddDate(-1, 0, -1))
			if c.active != "" {
				if err := s.Use(c.active); err != nil {
					t.Fatal(err)
				}
			}
			r, err := s.Resolve(c.l)
			if c.w.errSub != "" {
				if err == nil || !strings.Contains(err.Error(), c.w.errSub) {
					t.Fatalf("Resolve = %+v, %v; want error containing %q", r, err, c.w.errSub)
				}
				if strings.Contains(err.Error(), sentinel) {
					t.Fatalf("error leaks the token: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if r.Name != c.w.name || r.Pin != c.w.pin {
				t.Fatalf("Resolve = %+v; want name %q pin %q", r, c.w.name, c.w.pin)
			}
			wantPath := ""
			if c.w.name != "" {
				wantPath = s.tokenPath(c.w.name)
			}
			if r.TokenPath != wantPath {
				t.Fatalf("TokenPath = %q, want %q", r.TokenPath, wantPath)
			}
			// One name source for GROVE_ACCOUNT: the launch wrap exports
			// r.Name, shell-init derives it from the token file's basename
			// less `.token` — they must agree.
			if r.Name != "" && strings.TrimSuffix(filepath.Base(r.TokenPath), ".token") != r.Name {
				t.Fatalf("token path %q does not name account %q", r.TokenPath, r.Name)
			}
		})
	}
}

func TestResolveActiveMissingIsError(t *testing.T) {
	s := newStore(t)
	mustAdd(t, s, "main", sentinel, time.Now())
	if err := s.Use("main"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(s.tokenPath("main")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resolve(Launch{ClaudeCmd: "claude"}); err == nil || !strings.Contains(err.Error(), "gv account use login") {
		t.Fatalf("Resolve with a dangling active = %v; want a refusal naming the way back to login", err)
	}
}

func TestPreflight(t *testing.T) {
	s := newStore(t)
	mustAdd(t, s, "alt", sentinel, time.Now())
	if _, err := s.Preflight("alt"); err != nil {
		t.Fatalf("Preflight(healthy) = %v", err)
	}
	if err := os.Chmod(s.tokenPath("alt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Preflight("alt"); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("Preflight(0644) = %v; want a chmod refusal", err)
	}
	if err := os.WriteFile(s.tokenPath("alt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(s.tokenPath("alt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Preflight("alt"); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("Preflight(empty) = %v; want an empty refusal", err)
	}
	if _, err := s.Preflight("nope"); err == nil {
		t.Fatal("Preflight(missing) = nil; want an error")
	}
}

// Done, untracked and handed-off tasks all fold to Done: they don't block
// rm (InUse), but rm names them (PinnedDone) since adopt honours the pin.
func TestPinnedDone(t *testing.T) {
	tasks := []*state.Task{
		{Ticket: "T-3", Account: "alt", Done: true},
		{Ticket: "T-1", Account: "alt", Done: true},
		{Ticket: "T-2", Account: "alt"},
		{Ticket: "T-4", Account: "main", Done: true},
		nil,
	}
	if got := strings.Join(PinnedDone(tasks, "alt"), ","); got != "T-1,T-3" {
		t.Fatalf("PinnedDone = %q, want T-1,T-3", got)
	}
	if got := strings.Join(InUse(tasks, "alt"), ","); got != "T-2" {
		t.Fatalf("InUse = %q, want T-2", got)
	}
}

// TestResolveOverrideHint (grove-471): a non-adopt launch (a revived chat)
// replaces the adopt hint a refused recorded pin carries.
func TestResolveOverrideHint(t *testing.T) {
	s := newStore(t)
	_, err := s.Resolve(Launch{Pinned: "gone", ClaudeCmd: "claude", Override: "override: gv orchestrator new --resume x --account <name|login>"})
	if err == nil || !strings.Contains(err.Error(), "gv orchestrator new --resume x") || strings.Contains(err.Error(), "gv adopt") {
		t.Fatalf("recorded pin refusal = %v; want the chat override, not adopt's", err)
	}
}
