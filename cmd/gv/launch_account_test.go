package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/account"
	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/tmux"
	"github.com/JollyGrin/grove/internal/workspace"
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

// orchCfg is an orchestrator config with no claude_config_dir.
func orchCfg() *config.Config {
	cfg := &config.Config{}
	cfg.Orchestrator.Claude = "claude --dangerously-skip-permissions"
	return cfg
}

// activeAlt is a scratch store with account alt added and active.
func activeAlt(t *testing.T) string {
	t.Helper()
	state := t.TempDir()
	t.Setenv("GROVE_STATE_DIR", state)
	s := account.Open(state)
	if _, err := s.Add("alt", strings.NewReader("ZZZSENTINEL\n"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.Use("alt"); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(state, "accounts", "alt.token")
}

const cockpitGolden = `claude --dangerously-skip-permissions --add-dir '/w' --continue 2>/dev/null || claude --dangerously-skip-permissions --add-dir '/w'`

// TestChatSitesNoAccountsGolden (car 04): with no accounts, the cockpit's
// first pane, a minted orchestrator pane and a detached chat render
// exactly what they did before accounts.
func TestChatSitesNoAccountsGolden(t *testing.T) {
	t.Setenv("GROVE_STATE_DIR", t.TempDir())
	cfg := orchCfg()
	acct, err := resolveChatAccount("", "", cfg)
	if err != nil || acct != (account.Resolved{}) {
		t.Fatalf("resolve with no accounts = %+v, %v; want the login", acct, err)
	}
	if got := orchestratorCmd(cfg, "/w", acct); got != cockpitGolden {
		t.Errorf("cockpit first pane changed:\n got %s\nwant %s", got, cockpitGolden)
	}
	got, id, err := mintedOrchestratorLaunch(orchestratorLaunch(cfg, "/w"), "", "", nil, acct)
	if err != nil {
		t.Fatal(err)
	}
	if want := "claude --dangerously-skip-permissions --add-dir '/w' --session-id " + id; got != want {
		t.Errorf("orchestrator new line changed:\n got %s\nwant %s", got, want)
	}
	ws := &workspace.Workspace{Label: "w", Root: "/w"}
	plan, err := chatSpawnPlan(cfg, ws, "", "", "", "", "", nil, acct)
	if err != nil {
		t.Fatal(err)
	}
	if want := "claude --dangerously-skip-permissions --add-dir '/w' --session-id " + plan.SessionID; plan.Cmd != want {
		t.Errorf("chat line changed:\n got %s\nwant %s", plan.Cmd, want)
	}
	if line := chatAccountLine(acct); line != "" {
		t.Errorf("login chat must print no account line, got %q", line)
	}
}

// TestChatSitesActiveAccountWrapped: with alt active, every site runs on
// it — the cockpit's two limbs wrapped separately so `|| fresh` stays
// reachable and on the account, and the token never in the line.
func TestChatSitesActiveAccountWrapped(t *testing.T) {
	tok := activeAlt(t)
	cfg := orchCfg()
	acct, err := resolveChatAccount("", "", cfg)
	if err != nil || acct.Name != "alt" || acct.Pin != "" || acct.TokenPath != tok {
		t.Fatalf("resolve = %+v, %v; want active alt, unpinned", acct, err)
	}
	wrapPrefix := `( t="$(cat '` + tok + `')"`

	cockpit := orchestratorCmd(cfg, "/w", acct)
	resume, fresh, ok := strings.Cut(cockpit, " ) || ( ")
	if !ok {
		t.Fatalf("no || chain between wrapped limbs: %s", cockpit)
	}
	resume, fresh = resume+" )", "( "+fresh
	if !strings.HasPrefix(resume, wrapPrefix) || !strings.HasSuffix(resume, "exec claude --dangerously-skip-permissions --add-dir '/w' --continue 2>/dev/null )") {
		t.Errorf("resume limb not account-wrapped: %s", resume)
	}
	if !strings.HasPrefix(fresh, wrapPrefix) || !strings.HasSuffix(fresh, "exec claude --dangerously-skip-permissions --add-dir '/w' )") {
		t.Errorf("fresh limb not account-wrapped: %s", fresh)
	}

	minted, id, err := mintedOrchestratorLaunch(orchestratorLaunch(cfg, "/w"), "", "", nil, acct)
	if err != nil {
		t.Fatal(err)
	}
	if want := config.WrapAccount("claude --dangerously-skip-permissions --add-dir '/w' --session-id "+id, tok, "alt"); minted != want {
		t.Errorf("orchestrator new line:\n got %s\nwant %s", minted, want)
	}

	ws := &workspace.Workspace{Label: "w", Root: "/w"}
	plan, err := chatSpawnPlan(cfg, ws, "", "", "", "", "brief", nil, acct)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plan.Cmd, wrapPrefix) || !strings.HasSuffix(plan.Cmd, `"$(cat "`+plan.BriefPath+`")" )`) {
		t.Errorf("chat line not wrapped around the whole bare launch: %s", plan.Cmd)
	}
	for _, line := range []string{cockpit, minted, plan.Cmd} {
		if strings.Contains(line, "ZZZSENTINEL") {
			t.Fatalf("line leaks the token: %s", line)
		}
	}
	if plan.Account.Pin != "" {
		t.Errorf("an active account is not a pin: %+v", plan.Account)
	}
	if got := chatAccountLine(acct); !strings.Contains(got, "account alt (active)") || !strings.Contains(got, "no claude.ai connectors, no Remote Control") {
		t.Errorf("token chat must say what it loses: %q", got)
	}
	if got := chatNewOptions(cfg, "/w", "", acct.Name); got[0].Account != "alt" {
		t.Errorf("web sheet's host-default row must name the account: %+v", got[0])
	}
}

// TestChatSitesConfigDirUnwrapped: inside a workspace with
// claude_config_dir set, the active account is skipped — every site
// renders the golden line — and an explicit --account errors.
func TestChatSitesConfigDirUnwrapped(t *testing.T) {
	activeAlt(t)
	cfg := orchCfg()
	cfg.ClaudeConfigDir = "/home/x/.cc-work"
	acct, err := resolveChatAccount("", "", cfg)
	if err != nil || acct != (account.Resolved{}) {
		t.Fatalf("resolve = %+v, %v; want unwrapped", acct, err)
	}
	if got := orchestratorCmd(cfg, "/w", acct); got != cockpitGolden {
		t.Errorf("cockpit first pane wrapped under claude_config_dir:\n got %s", got)
	}
	ws := &workspace.Workspace{Label: "w", Root: "/w"}
	plan, err := chatSpawnPlan(cfg, ws, "", "", "", "", "", nil, acct)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.Cmd, "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Errorf("chat wrapped under claude_config_dir: %s", plan.Cmd)
	}
	if _, err := resolveChatAccount("alt", "", cfg); err == nil {
		t.Error("--account under claude_config_dir must error")
	}
}

// TestOrchestratorNewAccountWithProfileErrors: `gv orchestrator new
// --account alt --profile <non-anthropic>` refuses before anything is
// built; `--profile anthropic` is the Anthropic path and takes it.
func TestOrchestratorNewAccountWithProfileErrors(t *testing.T) {
	activeAlt(t)
	cfg := orchCfg()
	cfg.ModelProfiles = map[string]*config.ModelProfile{
		"openrouter-glm": {BaseURL: "https://x", AuthTokenEnv: "K", Sonnet: "s"},
	}
	_, err := spawnOrchestratorProfileBrief(cfg, "openrouter-glm", "alt", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "model profile openrouter-glm") {
		t.Fatalf("--account + --profile = %v, want the profile refusal", err)
	}
	if acct, err := resolveChatAccount("alt", "", cfg); err != nil || acct.Pin != "alt" {
		t.Errorf("--account alt on Anthropic = %+v, %v; want a pin", acct, err)
	}
}

// TestAccountRmLiveChats: rm's in-use check names live chats pinned to
// the account alongside tasks; an unpinned pane never blocks.
func TestAccountRmLiveChats(t *testing.T) {
	panes := []tmux.LivePane{
		{Session: "grove-chat-w-2", Pane: "%9", Account: "alt"},
		{Session: "grove-w", Pane: "%3", Account: "alt"},
		{Session: "grove-chat-w-1", Pane: "%7"},
		{Session: "grove-chat-w-3", Pane: "%8", Account: "other"},
	}
	chats := liveChatsPinned(panes, "alt")
	if want := []string{"grove-chat-w-2 (%9)", "grove-w (%3)"}; strings.Join(chats, ";") != strings.Join(want, ";") {
		t.Errorf("live chats = %v, want %v", chats, want)
	}
	err := accountInUse("alt", []string{"T-1"}, chats)
	if err == nil || !strings.Contains(err.Error(), "non-done task(s) T-1 and live chat(s) grove-chat-w-2 (%9), grove-w (%3)") {
		t.Errorf("rm refusal = %v", err)
	}
	if err := accountInUse("alt", nil, chats); err == nil || strings.Contains(err.Error(), "task") {
		t.Errorf("chat-only refusal = %v", err)
	}
	if err := accountInUse("alt", nil, nil); err != nil {
		t.Errorf("nothing pinned must not refuse: %v", err)
	}
}
