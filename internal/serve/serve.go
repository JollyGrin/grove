// Package serve holds the decisions behind `gv serve` (grove-380, feature
// trains Decision 7): the <workspace>/.grove/run.sh contract, the sha256
// trust gate, per-feature port allocation, the GROVE_READY handshake, the
// serve worktree gv creates and removes, and the serve status a feature
// row carries. cmd/gv/serve.go is the glue; tmux lives in internal/tmux.
package serve

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/worktree"
)

// FirstPort is where allocation starts; each feature gets its own port so
// trains serve side by side.
const FirstPort = 4100

// ReadyMarker is the token run.sh prints, followed by a URL or a path,
// once the thing it runs is up.
const ReadyMarker = "GROVE_READY"

// exitedMarker is written into the log by gv's window wrapper when run.sh
// returns, so a script that dies before GROVE_READY fails fast instead of
// waiting out the timeout.
const exitedMarker = "GROVE_SERVE_EXITED"

// Window is the tmux window name for a feature's serve. Matched EXACTLY
// (tmux.WindowIDExact), never by prefix: "▶ keys" must not hit "▶ keys-v2".
func Window(slug string) string { return "▶ " + slug }

// ScriptPath is the contract's location: committed with the workspace.
func ScriptPath(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, ".grove", "run.sh")
}

// Dir is where gv keeps per-feature serve files under the state dir.
func Dir(stateDir string) string { return filepath.Join(stateDir, "serve") }

// LogPath is the teed output of a feature's run.sh.
func LogPath(stateDir, slug string) string { return filepath.Join(Dir(stateDir), slug+".log") }

// SnapshotPath is the copy of the trusted bytes gv actually runs — so an
// edit between the hash check and the exec can never run unreviewed code.
func SnapshotPath(stateDir, slug string) string {
	return filepath.Join(Dir(stateDir), slug+".run.sh")
}

// WorktreePath is the serve worktree: <parent>/.worktrees/<repo>/<slug>-serve.
func WorktreePath(repoRoot, slug string) string {
	return worktree.DefaultPath(repoRoot, slug+"-serve")
}

// SHA is the hex sha256 the trust gate compares.
func SHA(script []byte) string {
	sum := sha256.Sum256(script)
	return hex.EncodeToString(sum[:])
}

// Trusted: the script runs only if its sha matches the LATEST
// run_script_trusted event. No event → untrusted; an edit → untrusted.
func Trusted(l *state.ServeLedger, sha string) bool {
	return sha != "" && l.TrustedSHA == sha
}

// Port returns the feature's recorded port, or the lowest port from
// FirstPort up that no feature has recorded.
func Port(l *state.ServeLedger, slug string) int {
	if s := l.Served[slug]; s != nil {
		return s.Port
	}
	taken := map[int]bool{}
	for _, s := range l.Served {
		taken[s.Port] = true
	}
	p := FirstPort
	for taken[p] {
		p++
	}
	return p
}

// ParseReady reads one output line: "GROVE_READY <url-or-path>" (leading
// and trailing space and a CR tolerated) yields the value. The marker must
// open the line, so a script echoing its own source never false-fires.
func ParseReady(line string) (string, bool) {
	line = strings.TrimSpace(strings.TrimRight(line, "\r"))
	rest, ok := strings.CutPrefix(line, ReadyMarker)
	if !ok || rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
		return "", false
	}
	v := strings.TrimSpace(rest)
	return v, v != ""
}

// Scan reads the log so far: the first READY value, or whether run.sh
// already exited (and with which status) without printing one.
func Scan(log []byte) (ready string, exited bool, code string) {
	sc := bufio.NewScanner(bytes.NewReader(log))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if v, ok := ParseReady(line); ok {
			return v, false, ""
		}
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), exitedMarker); ok {
			return "", true, strings.TrimSpace(rest)
		}
	}
	return "", false, ""
}

// Env is what run.sh sees, besides the operator's environment.
func Env(wt, branch, slug string, port int) []string {
	return []string{
		"GROVE_WORKTREE=" + wt,
		"GROVE_BRANCH=" + branch,
		"GROVE_PORT=" + fmt.Sprint(port),
		"GROVE_FEATURE=" + slug,
	}
}

// Command is the window's program, as argv (tmux execs it directly, no
// user shell in between): run the snapshot, tee everything into the log,
// stamp the exit, then keep the window open on a shell for inspection.
func Command(snapshot, logPath, slug string) []string {
	inner := fmt.Sprintf(`{ %s; echo "%s $?"; } 2>&1 | tee -a %s; `+
		`echo; echo "run.sh exited — window left for inspection; gv serve stop %s closes it"; `+
		`exec "${SHELL:-/bin/sh}"`,
		shq(snapshot), exitedMarker, shq(logPath), slug)
	return []string{"/bin/sh", "-c", inner}
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Status is a feature row's `serve` field (docs/plugins.md).
type Status struct {
	State  string `json:"state"` // none | untrusted | stopped | running
	Port   int    `json:"port,omitempty"`
	URL    string `json:"url,omitempty"`
	Tip    string `json:"tip,omitempty"`
	Behind bool   `json:"behind"` // served tip ≠ branch tip
}

// Serve states.
const (
	StateNone      = "none"
	StateUntrusted = "untrusted"
	StateStopped   = "stopped"
	StateRunning   = "running"
)

// StatusInput is everything Derive needs, gathered once per refresh.
type StatusInput struct {
	ScriptSHA  string        // "" when run.sh is absent
	TrustedSHA string        // latest run_script_trusted
	Served     *state.Served // latest feature_served, nil if never
	WindowLive bool          // the exact ▶ <slug> window exists
	BranchTip  string        // origin/<branch> as last fetched; "" unknown
}

// Derive: a live window is running (liveness is the window, whatever the
// events say); otherwise a present script that isn't trusted is
// untrusted; otherwise a feature served before is stopped; else none.
func Derive(in StatusInput) Status {
	st := Status{State: StateNone}
	switch {
	case in.WindowLive:
		st.State = StateRunning
	case in.ScriptSHA != "" && in.ScriptSHA != in.TrustedSHA:
		st.State = StateUntrusted
	case in.Served != nil:
		st.State = StateStopped
	}
	if s := in.Served; s != nil {
		st.Port, st.URL, st.Tip = s.Port, s.URL, s.Tip
		st.Behind = in.BranchTip != "" && s.Tip != "" && s.Tip != in.BranchTip
	}
	return st
}

// ReadScript returns run.sh's bytes and sha, or os.ErrNotExist.
func ReadScript(workspaceRoot string) ([]byte, string, error) {
	b, err := os.ReadFile(ScriptPath(workspaceRoot))
	if err != nil {
		return nil, "", err
	}
	return b, SHA(b), nil
}

// WriteSnapshot stores the trusted bytes as the executable gv runs, and
// truncates the log so the wait never matches a previous run's READY.
func WriteSnapshot(stateDir, slug string, script []byte) error {
	if err := os.MkdirAll(Dir(stateDir), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(LogPath(stateDir, slug), nil, 0o644); err != nil {
		return err
	}
	p := SnapshotPath(stateDir, slug)
	if err := os.WriteFile(p, script, 0o700); err != nil {
		return err
	}
	return os.Chmod(p, 0o700) // WriteFile keeps an existing file's mode
}

// WaitReady polls the log until run.sh prints GROVE_READY, exits, the
// window disappears, or timeout passes. alive may be nil.
func WaitReady(logPath string, timeout, poll time.Duration, alive func() bool) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		b, _ := os.ReadFile(logPath)
		ready, exited, code := Scan(b)
		if ready != "" {
			return ready, nil
		}
		if exited {
			return "", fmt.Errorf("run.sh exited (status %s) without printing %s — log: %s", code, ReadyMarker, logPath)
		}
		if alive != nil && !alive() {
			return "", fmt.Errorf("serve window vanished before %s — log: %s", ReadyMarker, logPath)
		}
		if !time.Now().Before(deadline) {
			return "", fmt.Errorf("no %s line within %s — window left for inspection; log: %s", ReadyMarker, timeout, logPath)
		}
		time.Sleep(poll)
	}
}

// --- the serve worktree -------------------------------------------------

// wtEntry is one `git worktree list --porcelain` record.
type wtEntry struct {
	path     string
	detached bool
}

func parsePorcelain(out string) []wtEntry {
	var es []wtEntry
	for _, block := range strings.Split(out, "\n\n") {
		var e wtEntry
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if p, ok := strings.CutPrefix(line, "worktree "); ok {
				e.path = p
			}
			if line == "detached" {
				e.detached = true
			}
		}
		if e.path != "" {
			es = append(es, e)
		}
	}
	return es
}

// findWorktree reports whether path is one of the repo's worktrees and
// whether it is detached. Paths compare after symlink resolution.
func findWorktree(porcelain, path string) (found, detached bool) {
	want := realpath(path)
	for _, e := range parsePorcelain(porcelain) {
		if realpath(e.path) == want {
			return true, e.detached
		}
	}
	return false, false
}

func realpath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// PrepareWorktree fetches the feature branch and puts the serve worktree
// at its new tip, detached: created on first serve, moved on each later
// one. A path that exists but is not this repo's detached worktree is
// refused — gv only drives the checkout it created.
func PrepareWorktree(repoRoot, branch, path string) (tip string, err error) {
	if _, err := git(repoRoot, "fetch", "origin", branch); err != nil {
		return "", err
	}
	tip, err = git(repoRoot, "rev-parse", "--verify", "--quiet", "origin/"+branch+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("origin/%s not found after fetch: %w", branch, err)
	}
	porcelain, err := git(repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	found, detached := findWorktree(porcelain, path)
	switch {
	case !found:
		if _, statErr := os.Stat(path); statErr == nil {
			return "", fmt.Errorf("%s exists but is not a worktree of %s — move it aside; gv serves only from a checkout it created", path, repoRoot)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		_, err = git(repoRoot, "worktree", "add", "--detach", path, tip)
	case !detached:
		return "", fmt.Errorf("%s is on a branch — not a serve worktree gv created; leaving it alone", path)
	default:
		_, err = git(path, "checkout", "--quiet", "--detach", tip)
	}
	return tip, err
}

// Removable reports whether path is the repo's detached serve worktree —
// the only shape gv creates, so the only one `gv feature close` removes.
func Removable(repoRoot, path string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		return false, nil
	}
	porcelain, err := git(repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return false, err
	}
	found, detached := findWorktree(porcelain, path)
	return found && detached, nil
}

// RemoveWorktree force-removes the serve worktree (build output and
// installed deps are untracked, which a plain remove refuses). Callers
// check Removable first.
func RemoveWorktree(repoRoot, path string) error {
	_, err := git(repoRoot, "worktree", "remove", "--force", path)
	return err
}

// BranchTip is origin/<branch> as last fetched, "" when unknown. Local
// only — the status refresh never touches the network.
func BranchTip(repoRoot, branch string) string {
	tip, err := git(repoRoot, "rev-parse", "--verify", "--quiet", "origin/"+branch+"^{commit}")
	if err != nil {
		return ""
	}
	return tip
}

// Confirmed: only an explicit y/yes trusts; anything else, empty included,
// is no.
func Confirmed(answer string) bool {
	a := strings.ToLower(strings.TrimSpace(answer))
	return a == "y" || a == "yes"
}
