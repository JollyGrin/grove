package serve

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/state"
)

func ledger(evs ...state.Event) *state.ServeLedger {
	l := state.NewServeLedger()
	for _, ev := range evs {
		l.FoldServe(ev)
	}
	return l
}

func trusted(sha string) state.Event {
	return state.Event{Type: state.EvRunScriptTrusted, Data: map[string]string{"sha256": sha}}
}

func served(slug, port, tip string) state.Event {
	return state.Event{Type: state.EvFeatureServed, Data: map[string]string{
		"slug": slug, "port": port, "tip": tip, "window": Window(slug), "url": "http://localhost:" + port,
	}}
}

func TestTrust(t *testing.T) {
	script := []byte("#!/bin/sh\necho GROVE_READY http://localhost:$GROVE_PORT\n")
	sha := SHA(script)
	if Trusted(ledger(), sha) {
		t.Fatal("no run_script_trusted event must read untrusted")
	}
	if !Trusted(ledger(trusted(sha)), sha) {
		t.Fatal("matching sha must read trusted")
	}
	edited := SHA(append(script, []byte("rm -rf ~\n")...))
	if Trusted(ledger(trusted(sha)), edited) {
		t.Fatal("an edited script must read untrusted again")
	}
	// Only the LATEST trust counts: re-trusting the edit, then reverting,
	// needs a fresh trust of the original.
	if Trusted(ledger(trusted(sha), trusted(edited)), sha) {
		t.Fatal("an older trusted sha must not stay trusted")
	}
	if Trusted(ledger(), "") {
		t.Fatal("empty sha must never be trusted")
	}
}

func TestPort(t *testing.T) {
	l := ledger()
	if p := Port(l, "a"); p != 4100 {
		t.Fatalf("first serve: got %d, want 4100", p)
	}
	l.FoldServe(served("a", "4100", "t1"))
	if p := Port(l, "b"); p != 4101 {
		t.Fatalf("second feature: got %d, want 4101", p)
	}
	l.FoldServe(served("a", "4100", "t2"))
	l.FoldServe(state.Event{Type: state.EvFeatureServeStopped, Data: map[string]string{"slug": "a"}})
	if p := Port(l, "a"); p != 4100 {
		t.Fatalf("re-serve (even after stop): got %d, want reuse 4100", p)
	}
	// A recorded port higher up is skipped, the gap below it is used.
	l2 := ledger(served("x", "4100", "t"), served("y", "4102", "t"))
	if p := Port(l2, "z"); p != 4101 {
		t.Fatalf("gap: got %d, want 4101", p)
	}
	l2.FoldServe(served("z", "4101", "t"))
	if p := Port(l2, "w"); p != 4103 {
		t.Fatalf("skip recorded 4102: got %d, want 4103", p)
	}
}

func TestParseReady(t *testing.T) {
	for _, tc := range []struct {
		line, want string
		ok         bool
	}{
		{"GROVE_READY http://localhost:4100", "http://localhost:4100", true},
		{"GROVE_READY /tmp/gv-keys", "/tmp/gv-keys", true},
		{"  GROVE_READY   http://127.0.0.1:4101/app  \r", "http://127.0.0.1:4101/app", true},
		{"GROVE_READY\t/tmp/x", "/tmp/x", true},
		{"GROVE_READY", "", false},
		{"GROVE_READY   ", "", false},
		{"GROVE_READYhttp://x", "", false},
		{`echo "GROVE_READY http://x"`, "", false},
		{"ready on http://localhost", "", false},
	} {
		got, ok := ParseReady(tc.line)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ParseReady(%q) = %q,%v; want %q,%v", tc.line, got, ok, tc.want, tc.ok)
		}
	}
}

func TestScan(t *testing.T) {
	if v, ex, _ := Scan([]byte("building…\nGROVE_READY /tmp/gv-x\nGROVE_SERVE_EXITED 0\n")); v != "/tmp/gv-x" || ex {
		t.Fatalf("ready then exit (CLI build): got %q exited=%v", v, ex)
	}
	if v, ex, code := Scan([]byte("boom\nGROVE_SERVE_EXITED 2\n")); v != "" || !ex || code != "2" {
		t.Fatalf("exit without ready: got %q %v %q", v, ex, code)
	}
	if v, ex, _ := Scan([]byte("still starting\n")); v != "" || ex {
		t.Fatalf("pending: got %q %v", v, ex)
	}
}

func TestDerive(t *testing.T) {
	sv := &state.Served{Port: 4100, Tip: "aaa", URL: "http://localhost:4100"}
	for _, tc := range []struct {
		name   string
		in     StatusInput
		want   string
		behind bool
	}{
		{"none: no script, never served", StatusInput{}, StateNone, false},
		{"none: trusted script, never served", StatusInput{ScriptSHA: "s", TrustedSHA: "s"}, StateNone, false},
		{"untrusted: script never trusted", StatusInput{ScriptSHA: "s"}, StateUntrusted, false},
		{"untrusted: script edited since", StatusInput{ScriptSHA: "s2", TrustedSHA: "s", Served: sv, BranchTip: "aaa"}, StateUntrusted, false},
		{"stopped: served, window gone", StatusInput{ScriptSHA: "s", TrustedSHA: "s", Served: sv, BranchTip: "aaa"}, StateStopped, false},
		{"running: window live, at tip", StatusInput{ScriptSHA: "s", TrustedSHA: "s", Served: sv, WindowLive: true, BranchTip: "aaa"}, StateRunning, false},
		{"running: behind the branch", StatusInput{ScriptSHA: "s", TrustedSHA: "s", Served: sv, WindowLive: true, BranchTip: "bbb"}, StateRunning, true},
		{"running beats untrusted (liveness is the window)", StatusInput{ScriptSHA: "s2", TrustedSHA: "s", Served: sv, WindowLive: true}, StateRunning, false},
	} {
		st := Derive(tc.in)
		if st.State != tc.want || st.Behind != tc.behind {
			t.Errorf("%s: got %+v, want state %s behind %v", tc.name, st, tc.want, tc.behind)
		}
		if tc.in.Served != nil && (st.Port != 4100 || st.URL == "" || st.Tip != "aaa") {
			t.Errorf("%s: served fields not carried: %+v", tc.name, st)
		}
	}
}

func TestConfirmed(t *testing.T) {
	for in, want := range map[string]bool{"y\n": true, "Y": true, "yes\n": true, "": false, "\n": false, "n": false, "yep": false} {
		if Confirmed(in) != want {
			t.Errorf("Confirmed(%q) != %v", in, want)
		}
	}
}

func TestWaitReady(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "x.log")
	os.WriteFile(log, []byte("GROVE_READY http://localhost:4100\n"), 0o644)
	if v, err := WaitReady(log, time.Second, time.Millisecond, nil); err != nil || v != "http://localhost:4100" {
		t.Fatalf("got %q %v", v, err)
	}
	os.WriteFile(log, []byte("nope\n"), 0o644)
	_, err := WaitReady(log, 20*time.Millisecond, time.Millisecond, nil)
	if err == nil || !strings.Contains(err.Error(), log) {
		t.Fatalf("timeout must name the log: %v", err)
	}
	_, err = WaitReady(log, time.Second, time.Millisecond, func() bool { return false })
	if err == nil || !strings.Contains(err.Error(), "vanished") {
		t.Fatalf("dead window must fail fast: %v", err)
	}
}

// TestCommandRunsSnapshot runs the window program under a plain sh (no
// tmux) and checks the log gets output + the exit stamp.
func TestCommandRunsSnapshot(t *testing.T) {
	dir := t.TempDir()
	snap := filepath.Join(dir, "it's.run.sh")
	log := filepath.Join(dir, "o.log")
	os.WriteFile(snap, []byte("echo GROVE_READY /tmp/gv-$GROVE_FEATURE\nexit 3\n"), 0o700)
	argv := Command(snap, log, "keys")
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "GROVE_FEATURE=keys", "SHELL=/usr/bin/true")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	b, _ := os.ReadFile(log)
	if v, _, _ := Scan(b); v != "/tmp/gv-keys" {
		t.Fatalf("log: %q", b)
	}
	if !strings.Contains(string(b), "GROVE_SERVE_EXITED 3") {
		t.Fatalf("exit stamp missing: %q", b)
	}
}

func TestFindWorktree(t *testing.T) {
	out := "worktree /r\nHEAD aaa\nbranch refs/heads/main\n\nworktree /w/x-serve\nHEAD bbb\ndetached\n\nworktree /w/y-serve\nHEAD ccc\nbranch refs/heads/y\n"
	if f, d := findWorktree(out, "/w/x-serve"); !f || !d {
		t.Fatal("detached serve worktree not found")
	}
	if f, d := findWorktree(out, "/w/y-serve"); !f || d {
		t.Fatal("branch worktree must read not-detached")
	}
	if f, _ := findWorktree(out, "/w/z-serve"); f {
		t.Fatal("absent path found")
	}
}

// TestWorktreeLifecycle: create at the tip, move to a new tip, removable
// only while detached.
func TestWorktreeLifecycle(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	origin, repo := filepath.Join(dir, "origin.git"), filepath.Join(dir, "repo")
	sh := func(d string, args ...string) string {
		t.Helper()
		out, err := git(d, args...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	sh(dir, "init", "-q", "--bare", "-b", "main", origin)
	sh(dir, "clone", "-q", origin, repo)
	sh(repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "one")
	sh(repo, "push", "-q", "origin", "HEAD:refs/heads/feature/f")
	wt := WorktreePath(repo, "f")
	tip, err := PrepareWorktree(repo, "feature/f", wt)
	if err != nil {
		t.Fatal(err)
	}
	if got := sh(wt, "rev-parse", "HEAD"); got != tip {
		t.Fatalf("worktree at %s, want %s", got, tip)
	}
	sh(repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "two")
	sh(repo, "push", "-q", "origin", "HEAD:refs/heads/feature/f")
	tip2, err := PrepareWorktree(repo, "feature/f", wt)
	if err != nil || tip2 == tip {
		t.Fatalf("move: %v (%s → %s)", err, tip, tip2)
	}
	if got := sh(wt, "rev-parse", "HEAD"); got != tip2 {
		t.Fatalf("not moved: %s", got)
	}
	if BranchTip(repo, "feature/f") != tip2 {
		t.Fatal("BranchTip")
	}
	os.WriteFile(filepath.Join(wt, "node_modules"), []byte("x"), 0o644) // untracked build junk
	if ok, err := Removable(repo, wt); !ok || err != nil {
		t.Fatalf("removable: %v %v", ok, err)
	}
	if err := RemoveWorktree(repo, wt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("worktree still there")
	}
	// A same-named checkout on a branch is not gv's: never removed, never driven.
	sh(repo, "worktree", "add", "-q", "-b", "mine", wt)
	if ok, _ := Removable(repo, wt); ok {
		t.Fatal("a branch worktree at the serve path must not be removable")
	}
	if _, err := PrepareWorktree(repo, "feature/f", wt); err == nil {
		t.Fatal("PrepareWorktree must refuse a branch worktree")
	}
}
