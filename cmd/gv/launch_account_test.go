package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/account"
	"github.com/JollyGrin/grove/internal/config"
)

// noAccounts resolves a launch against a scratch state dir holding no
// accounts — the operator who never ran `gv account add`.
func noAccounts(t *testing.T) account.Resolved {
	t.Helper()
	t.Setenv("GROVE_STATE_DIR", t.TempDir())
	cfg := &config.Config{}
	acct, err := resolveLaunchAccount("T-1", "", "", "", cfg, &config.Repo{Claude: "claude --dangerously-skip-permissions"})
	if err != nil || acct != (account.Resolved{}) {
		t.Fatalf("resolve with no accounts = %+v, %v; want the login", acct, err)
	}
	return acct
}

// TestLaunchStringsNoAccountsGolden: with no accounts configured, grab and
// adopt render exactly the launch strings they did before accounts.
func TestLaunchStringsNoAccountsGolden(t *testing.T) {
	acct := noAccounts(t)
	const bin = "claude --dangerously-skip-permissions --effort 'high'"
	const prompt = "/s/prompts/grove-9.txt"

	if got, want := grabClaudeCmd(bin, prompt, nil, acct),
		`claude --dangerously-skip-permissions --effort 'high' "$(cat "/s/prompts/grove-9.txt")"`; got != want {
		t.Errorf("grab line changed:\n got %s\nwant %s", got, want)
	}
	if got, want := adoptClaudeCmd(bin, prompt, "", false, nil, acct),
		`claude --dangerously-skip-permissions --effort 'high' "$(cat "/s/prompts/grove-9.txt")"`; got != want {
		t.Errorf("adopt fresh line changed:\n got %s\nwant %s", got, want)
	}
	if got, want := adoptClaudeCmd(bin, prompt, "sess-1", false, nil, acct),
		`claude --dangerously-skip-permissions --effort 'high' --resume sess-1 || claude --dangerously-skip-permissions --effort 'high' "$(cat "/s/prompts/grove-9.txt")"`; got != want {
		t.Errorf("adopt resume line changed:\n got %s\nwant %s", got, want)
	}
	if got, want := adoptClaudeCmd(bin, prompt, "sess-1", true, nil, acct),
		`claude --dangerously-skip-permissions --effort 'high' "$(cat "/s/prompts/grove-9.txt")"`; got != want {
		t.Errorf("adopt manual line changed:\n got %s\nwant %s", got, want)
	}
}

// TestAdoptWrapsBothLimbs: an account wraps resume and fresh separately,
// so the `|| fresh` fallback stays reachable and stays on the account.
func TestAdoptWrapsBothLimbs(t *testing.T) {
	acct := account.Resolved{Name: "alt", TokenPath: "/s/accounts/alt.token", Pin: "alt"}
	got := adoptClaudeCmd("claude", "/p", "sess-1", false, nil, acct)
	resume, fresh, ok := strings.Cut(got, " ) || ( ")
	if !ok {
		t.Fatalf("no || chain between wrapped limbs: %s", got)
	}
	resume, fresh = resume+" )", "( "+fresh
	for _, limb := range []string{resume, fresh} {
		if !strings.HasPrefix(limb, `( t="$(cat '/s/accounts/alt.token')"`) || !strings.HasSuffix(limb, " )") {
			t.Errorf("limb not account-wrapped: %s", limb)
		}
	}
}

// TestWrapWorkerNeverNests (design §Decision 1): a profile wrap is never
// account-wrapped too — `exec ( … )` would be a shell syntax error.
func TestWrapWorkerNeverNests(t *testing.T) {
	p := &config.ModelProfile{BaseURL: "https://x", AuthTokenEnv: "K", Sonnet: "s"}
	acct := account.Resolved{Name: "alt", TokenPath: "/s/accounts/alt.token"}
	got := wrapWorker("claude", p, acct)
	if strings.Contains(got, "CLAUDE_CODE_OAUTH_TOKEN") || strings.Contains(got, "exec (") {
		t.Fatalf("profile and account wraps nested: %s", got)
	}
	if want := config.WrapProfile("claude", p, config.SecretsPath()); got != want {
		t.Fatalf("wrapWorker(profile) = %s, want the profile wrap alone", got)
	}
}

// TestGrabLineCarriesPathNotToken: the rendered send-keys line for an
// account launch names the token file but never its content.
func TestGrabLineCarriesPathNotToken(t *testing.T) {
	state := t.TempDir()
	t.Setenv("GROVE_STATE_DIR", state)
	s := account.Open(state)
	if _, err := s.Add("alt", strings.NewReader("ZZZSENTINEL\n"), time.Now()); err != nil {
		t.Fatal(err)
	}
	acct, err := resolveLaunchAccount("T-1", "alt", "", "", &config.Config{}, &config.Repo{Claude: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	line := grabClaudeCmd("claude", "/p", nil, acct)
	if tok := filepath.Join(state, "accounts", "alt.token"); !strings.Contains(line, tok) {
		t.Errorf("line lacks the token path %s: %s", tok, line)
	}
	if strings.Contains(line, "ZZZSENTINEL") {
		t.Fatalf("line leaks the token: %s", line)
	}
	if acct.Pin != "alt" {
		t.Errorf("explicit --account must pin: %+v", acct)
	}
	if _, err := os.Stat(acct.TokenPath); err != nil {
		t.Fatal(err)
	}
}
