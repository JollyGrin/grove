package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/state"
)

// gateRig is one scratch fleet for the done gate: a bare "origin", a
// clone as the task's worktree on branch b-<ticket> (pushed, upstream
// set), a stub gh on PATH, and a scratch HOME whose global config carries
// repos.r.done_gate. The task's SessionID is recorded so the payload's
// id passes the grove-250 gate.
type gateRig struct {
	t        *testing.T
	stateDir string
	wt       string // realpath'd worktree
	branch   string
	ticket   string
}

func newGateRig(t *testing.T, mode string, remote bool) *gateRig {
	t.Helper()
	withNtfy(t, config.Notify{})
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "nogitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")

	stateDir := t.TempDir()
	ticket := "grove-1"
	branch := "b-" + ticket
	root := t.TempDir()
	wt := filepath.Join(root, "wt")

	if remote {
		origin := filepath.Join(root, "origin.git")
		gitRun(t, root, "init", "-q", "--bare", origin)
		gitRun(t, root, "clone", "-q", origin, wt)
	} else {
		if err := os.MkdirAll(wt, 0o755); err != nil {
			t.Fatal(err)
		}
		gitRun(t, wt, "init", "-q")
	}
	gitRun(t, wt, "config", "user.email", "t@grove.test")
	gitRun(t, wt, "config", "user.name", "grove test")
	gitRun(t, wt, "checkout", "-q", "-b", branch)
	if err := os.WriteFile(filepath.Join(wt, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, wt, "add", "-A")
	gitRun(t, wt, "commit", "-q", "-m", "init")
	if remote {
		gitRun(t, wt, "push", "-q", "-u", "origin", branch)
	}

	cfg := "repos:\n  r:\n    path: " + wt + "\n"
	if mode != "" {
		cfg += "    done_gate: " + mode + "\n"
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "grove"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "grove", "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	real := seedFleet(t, stateDir, ticket, wt)
	if err := state.Append(stateDir, state.Event{Type: state.EvSessionStarted, Ticket: ticket, Data: map[string]string{"session_id": "s1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Load(stateDir); err != nil {
		t.Fatal(err)
	}
	return &gateRig{t: t, stateDir: stateDir, wt: real, branch: branch, ticket: ticket}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// stubGh puts a fake gh first on PATH. body is what `gh pr list` prints;
// sleep delays it (for the timeout case).
func stubGh(t *testing.T, body string, sleep time.Duration) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n"
	if sleep > 0 {
		script += "sleep " + strings.TrimSuffix(sleep.String(), "s") + "\n"
	}
	script += "printf '%s' '" + body + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// stop sends one Stop for the rig's worker and returns the hook's stdout
// plus the data of the agent_status it appended.
func (r *gateRig) stop(msg string, active bool) (stdout string, data map[string]string) {
	r.t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"session_id": "s1", "cwd": r.wt, "hook_event_name": "Stop",
		"last_assistant_message": msg, "stop_hook_active": active,
	})
	var out bytes.Buffer
	if err := ReceiveTo(single(r.stateDir), "stop", bytes.NewReader(payload), &out); err != nil {
		r.t.Fatalf("ReceiveTo: %v", err)
	}
	evs, err := state.ReadEvents(r.stateDir, 0)
	if err != nil {
		r.t.Fatal(err)
	}
	last := evs[len(evs)-1]
	if last.Type != state.EvAgentStatus {
		r.t.Fatalf("last event = %s, want agent_status", last.Type)
	}
	return out.String(), last.Data
}

func (r *gateRig) dirty() {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.wt, "scratch.txt"), []byte("wip\n"), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

const doneMsg = "All green.\n\nSTATUS: DONE — shipped"

func assertBlock(t *testing.T, stdout, wantReason string) {
	t.Helper()
	var d struct{ Decision, Reason string }
	if err := json.Unmarshal([]byte(stdout), &d); err != nil {
		t.Fatalf("stdout %q is not the block JSON: %v", stdout, err)
	}
	if d.Decision != "block" {
		t.Errorf("decision = %q, want block", d.Decision)
	}
	want := "STATUS: DONE claimed but " + wantReason + ". Finish that, then restate your STATUS line."
	if d.Reason != want {
		t.Errorf("reason = %q\nwant     %q", d.Reason, want)
	}
}

func TestDoneGateVerifiedPassesUntouched(t *testing.T) {
	r := newGateRig(t, "block", true)
	stubGh(t, `[{"number":7,"state":"OPEN"}]`, 0)
	stdout, data := r.stop(doneMsg, false)
	if stdout != "" {
		t.Errorf("verified DONE must write nothing, got %q", stdout)
	}
	// Byte-for-byte today's record: the five keys, nothing additive.
	want := map[string]string{"status": state.AgentIdle, "sentinel": "done", "question": "", "message": doneMsg, "session_id": "s1"}
	if len(data) != len(want) {
		t.Errorf("data = %v, want exactly %v", data, want)
	}
	for k, v := range want {
		if data[k] != v {
			t.Errorf("data[%s] = %q, want %q", k, data[k], v)
		}
	}
}

func TestDoneGateDirtyBlocks(t *testing.T) {
	r := newGateRig(t, "block", true)
	stubGh(t, `[{"number":7,"state":"OPEN"}]`, 0)
	r.dirty()
	stdout, data := r.stop(doneMsg, false)
	assertBlock(t, stdout, "1 uncommitted file")
	if data["sentinel"] != SentinelDoneUnverified || data["status"] != state.AgentWorking {
		t.Errorf("blocked DONE must land as done_unverified/working, got %s/%s", data["sentinel"], data["status"])
	}
	if data["gate"] != "block" || data["gate_decision"] != "block" || data["gate_reason"] != "1 uncommitted file" {
		t.Errorf("gate data = %v", data)
	}
}

func TestDoneGateUnpushedBlocks(t *testing.T) {
	r := newGateRig(t, "block", true)
	stubGh(t, `[{"number":7,"state":"OPEN"}]`, 0)
	if err := os.WriteFile(filepath.Join(r.wt, "more.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, r.wt, "add", "-A")
	gitRun(t, r.wt, "commit", "-q", "-m", "more")
	stdout, _ := r.stop(doneMsg, false)
	assertBlock(t, stdout, "1 unpushed commit(s)")
}

func TestDoneGateNoUpstreamBlocks(t *testing.T) {
	r := newGateRig(t, "block", true)
	stubGh(t, `[]`, 0)
	gitRun(t, r.wt, "checkout", "-q", "-b", "b-other")
	stdout, _ := r.stop(doneMsg, false)
	// The task's recorded branch names the PR; the checkout has no upstream.
	assertBlock(t, stdout, "branch not pushed / no PR for "+r.branch)
}

func TestDoneGateNoPRBlocks(t *testing.T) {
	r := newGateRig(t, "block", true)
	stubGh(t, `[{"number":3,"state":"CLOSED"}]`, 0) // a closed PR is not evidence
	stdout, _ := r.stop(doneMsg, false)
	assertBlock(t, stdout, "no PR for "+r.branch)
}

func TestDoneGateGhTimeoutPasses(t *testing.T) {
	r := newGateRig(t, "block", true)
	old := ghTimeout
	ghTimeout = 200 * time.Millisecond
	t.Cleanup(func() { ghTimeout = old })
	stubGh(t, `[]`, 2*time.Second)
	stdout, data := r.stop(doneMsg, false)
	if stdout != "" {
		t.Errorf("gh timeout must let the stop through, got %q", stdout)
	}
	if data["sentinel"] != "done" || data["gate"] != "" {
		t.Errorf("gh timeout must not mark the DONE unverified: %v", data)
	}
}

func TestDoneGateCapsConsecutiveBlocks(t *testing.T) {
	r := newGateRig(t, "block", true)
	stubGh(t, `[]`, 0)
	r.dirty()
	for i := 1; i <= 2; i++ {
		stdout, _ := r.stop(doneMsg, false)
		if !strings.Contains(stdout, `"decision":"block"`) {
			t.Fatalf("stop %d: want block, got %q", i, stdout)
		}
	}
	stdout, data := r.stop(doneMsg, false)
	if stdout != "" {
		t.Errorf("third consecutive DONE must pass (cap), got %q", stdout)
	}
	if data["sentinel"] != SentinelDoneUnverified || data["gate_decision"] != "pass:capped" || data["status"] != state.AgentIdle {
		t.Errorf("capped pass must still record done_unverified/idle: %v", data)
	}
	// Still capped: a fourth never blocks either (no loop, ever).
	if stdout, _ := r.stop(doneMsg, false); stdout != "" {
		t.Errorf("fourth DONE must stay capped, got %q", stdout)
	}
}

func TestDoneGateNonDoneStopResetsCap(t *testing.T) {
	r := newGateRig(t, "block", true)
	stubGh(t, `[]`, 0)
	r.dirty()
	r.stop(doneMsg, false)
	r.stop(doneMsg, false)
	if stdout, _ := r.stop(doneMsg, false); stdout != "" {
		t.Fatalf("expected the cap, got %q", stdout)
	}
	if stdout, data := r.stop("STATUS: QUESTION — which base?", false); stdout != "" || data["sentinel"] != "question" {
		t.Fatalf("question must never be gated: %q %v", stdout, data)
	}
	stdout, _ := r.stop(doneMsg, false)
	assertBlock(t, stdout, "1 uncommitted file / no PR for "+r.branch)
}

func TestDoneGateStopHookActivePasses(t *testing.T) {
	r := newGateRig(t, "block", true)
	stubGh(t, `[]`, 0)
	r.dirty()
	stdout, data := r.stop(doneMsg, true)
	if stdout != "" {
		t.Errorf("stop_hook_active must never be blocked, got %q", stdout)
	}
	if data["gate_decision"] != "pass:stop_hook_active" || data["sentinel"] != SentinelDoneUnverified {
		t.Errorf("data = %v", data)
	}
}

func TestDoneGateWarnDefaultNeverBlocks(t *testing.T) {
	r := newGateRig(t, "", true) // no done_gate key → warn
	stubGh(t, `[]`, 0)
	r.dirty()
	stdout, data := r.stop(doneMsg, false)
	if stdout != "" {
		t.Errorf("warn must not block, got %q", stdout)
	}
	if data["sentinel"] != "done" || data["status"] != state.AgentIdle {
		t.Errorf("warn keeps the done sentinel: %v", data)
	}
	if data["gate"] != "warn" || data["gate_decision"] != "pass:warn" || data["gate_reason"] != "1 uncommitted file / no PR for "+r.branch {
		t.Errorf("warn must record the verdict: %v", data)
	}
}

func TestDoneGateOffRecordsNothing(t *testing.T) {
	r := newGateRig(t, "off", true)
	stubGh(t, `[]`, 0)
	r.dirty()
	stdout, data := r.stop(doneMsg, false)
	if stdout != "" || data["gate"] != "" || data["sentinel"] != "done" {
		t.Errorf("off must be today's behavior: %q %v", stdout, data)
	}
}

func TestDoneGateNoRemoteChecksOnlyDirty(t *testing.T) {
	r := newGateRig(t, "block", false)
	stubGh(t, `[]`, 0) // would say "no PR" if consulted
	if stdout, data := r.stop(doneMsg, false); stdout != "" || data["gate"] != "" {
		t.Errorf("clean no-remote worktree must pass: %q %v", stdout, data)
	}
	r.dirty()
	stdout, _ := r.stop(doneMsg, false)
	assertBlock(t, stdout, "1 uncommitted file")
}

func TestDoneGateUnreadableWorktreePasses(t *testing.T) {
	r := newGateRig(t, "block", true)
	stubGh(t, `[]`, 0)
	if err := os.RemoveAll(filepath.Join(r.wt, ".git")); err != nil {
		t.Fatal(err)
	}
	stdout, data := r.stop(doneMsg, false)
	if stdout != "" || data["gate"] != "" || data["sentinel"] != "done" {
		t.Errorf("a grove-side failure must let the stop through: %q %v", stdout, data)
	}
}

func TestDoneGateAtLayersWorkspaceOverGlobal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	gdir := filepath.Join(home, ".config", "grove")
	if err := os.MkdirAll(gdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gdir, "config.yaml"), []byte("repos:\n  r:\n    path: /x\n    done_gate: block\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := config.DoneGateAt("", "r"); got != config.DoneGateBlock {
		t.Errorf("global = %q, want block", got)
	}
	if got := config.DoneGateAt("", "other"); got != config.DoneGateWarn {
		t.Errorf("unknown repo = %q, want warn", got)
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".grove"), 0o755); err != nil {
		t.Fatal(err)
	}
	// repos is a wholesale key: the workspace block replaces the global one.
	if err := os.WriteFile(filepath.Join(root, ".grove", "config.yaml"), []byte("repos:\n  r:\n    path: /y\n    done_gate: off\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := config.DoneGateAt(root, "r"); got != config.DoneGateOff {
		t.Errorf("workspace = %q, want off", got)
	}
	if err := os.WriteFile(filepath.Join(root, ".grove", "config.yaml"), []byte("repos:\n  r:\n    done_gate: bogus\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := config.DoneGateAt(root, "r"); got != config.DoneGateWarn {
		t.Errorf("bogus = %q, want warn (tolerant)", got)
	}
	if got := config.DoneGateAt(filepath.Join(root, "missing"), "r"); got != config.DoneGateBlock {
		t.Errorf("missing workspace layer = %q, want the global block", got)
	}
}
