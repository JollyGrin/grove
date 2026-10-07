package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/git"
	"github.com/JollyGrin/grove/internal/state"
)

// The Stop hook's evidence gate for `STATUS: DONE` (grove-441).
//
// Claude Code best practices, "Give Claude a way to verify its work": a
// Stop hook is the DETERMINISTIC gate — CLAUDE.md asking for evidence is
// advisory, a hook that checks the worktree is not. A DONE claimed while
// the worktree is dirty, the branch is unpushed, or no PR exists is
// answered with `{"decision":"block","reason":…}`: Claude Code shows the
// reason to the model, which continues the turn instead of stopping.
//
// Guardrails so the gate can never loop or wedge a session:
//   - `stop_hook_active: true` in the payload means Claude is already
//     continuing because of a Stop hook — never block that re-entry.
//   - At most maxGateBlocks consecutive blocks per session; the counter
//     lives in a side file under the fleet's state dir (tasks.json is a
//     derived view and events.jsonl is append-only) and resets on any
//     non-DONE stop or a verified DONE.
//   - QUESTION/BLOCKED are never gated.
//   - When grove itself cannot read the evidence (worktree gone, git
//     failing) the stop passes and the failure is logged; a `gh` failure
//     or timeout only drops the PR check, the git evidence still counts.
//   - Per-repo `done_gate: off|warn|block`, default warn: the verdict is
//     recorded and notified but never blocks, so the operator can watch
//     the gate for a release before enabling block.

// SentinelDoneUnverified is the additive agent_status sentinel value a
// DONE claim lands as when the gate is in block mode and the evidence is
// missing — whether the stop was actually blocked, passed on the cap, or
// passed on stop_hook_active. `gv ls` shows it as "done?"; `gv watch
// --until done` does not fire on it.
const SentinelDoneUnverified = "done_unverified"

// maxGateBlocks caps consecutive blocks per session (Stop input covers
// the cap; the hooks reference warns a blocking Stop hook must bound
// itself).
const maxGateBlocks = 2

// ghTimeout bounds the PR lookup — a hook runs inside Claude Code's own
// hook deadline, so a wedged gh must fail fast and fall back.
var ghTimeout = 5 * time.Second

// gateVerdict is what the stop path records and acts on.
type gateVerdict struct {
	Mode   string // config.DoneGateOff | Warn | Block
	Reason string // "" = evidence present (or gate off / unreadable)
	Block  bool   // emit the block JSON
	Pass   string // why an unverified DONE was not blocked: "warn", "capped", "stop_hook_active"
}

// Unverified reports whether the DONE claim lacked evidence.
func (v gateVerdict) Unverified() bool { return v.Reason != "" }

// doneGate evaluates a DONE claim for task. root is the owning
// workspace's root ("" for the legacy global fleet).
func doneGate(stateDir, root string, task *state.Task, p Payload) gateVerdict {
	mode := config.DoneGateAt(root, task.Repo)
	if mode == config.DoneGateOff {
		gateReset(stateDir, task.Ticket)
		return gateVerdict{Mode: mode}
	}
	wt := task.Worktree
	if wt == "" {
		wt = p.Cwd
	}
	reason, err := doneEvidence(wt, task.Branch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gv hook: done gate skipped (%s): %v\n", task.Ticket, err)
		gateReset(stateDir, task.Ticket)
		return gateVerdict{Mode: mode}
	}
	if reason == "" {
		gateReset(stateDir, task.Ticket)
		return gateVerdict{Mode: mode}
	}
	v := gateVerdict{Mode: mode, Reason: reason}
	switch {
	case mode != config.DoneGateBlock:
		v.Pass = "warn"
	case p.StopHookActive:
		v.Pass = "stop_hook_active"
	default:
		n := gateBlocks(stateDir, task.Ticket, p.SessionID)
		if n >= maxGateBlocks {
			v.Pass = "capped"
		} else {
			v.Block = true
			gateRecord(stateDir, task.Ticket, p.SessionID, n+1)
		}
	}
	return v
}

// blockJSON is the Stop hook's blocking decision, per the hooks
// reference: decision "block" + a reason shown to the model.
func blockJSON(reason string) []byte {
	out, _ := json.Marshal(map[string]string{
		"decision": "block",
		"reason":   "STATUS: DONE claimed but " + reason + ". Finish that, then restate your STATUS line.",
	})
	return append(out, '\n')
}

// doneEvidence checks the worktree for what a finished task leaves
// behind. It returns the missing pieces joined with " / " ("" when all
// present), or an error when git itself cannot answer — the caller then
// lets the stop through. Repos with no origin remote get only the dirty
// check (the degraded no-remote path: nothing to push to, no PR to open).
func doneEvidence(worktree, branch string) (string, error) {
	if fi, err := os.Stat(worktree); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("worktree %s: unreadable", worktree)
	}
	out, err := git.Run(worktree, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	var missing []string
	if n := porcelainCount(out); n > 0 {
		noun := "uncommitted files"
		if n == 1 {
			noun = "uncommitted file"
		}
		missing = append(missing, strconv.Itoa(n)+" "+noun)
	}
	if !git.HasRemote(worktree, "origin") {
		return strings.Join(missing, " / "), nil
	}
	if branch == "" {
		branch, _ = git.CurrentBranch(worktree)
	}
	if !git.HasUpstream(worktree) {
		missing = append(missing, "branch not pushed")
	} else {
		ahead, _, err := git.BranchStatus(worktree)
		if err != nil {
			return "", err
		}
		if ahead > 0 {
			missing = append(missing, fmt.Sprintf("%d unpushed commit(s)", ahead))
		}
	}
	hasPR, err := prExists(worktree, branch)
	switch {
	case err != nil:
		fmt.Fprintf(os.Stderr, "gv hook: done gate: PR check skipped: %v\n", err)
	case !hasPR:
		missing = append(missing, "no PR for "+branch)
	}
	return strings.Join(missing, " / "), nil
}

// prExists asks gh whether an open or merged PR exists for branch,
// bounded by ghTimeout. Any failure is the caller's cue to skip the
// check, never to block.
func prExists(dir, branch string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "pr", "list", "--head", branch, "--state", "all", "--limit", "5", "--json", "number,state")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return false, fmt.Errorf("gh pr list timed out after %s", ghTimeout)
		}
		return false, fmt.Errorf("gh pr list: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var prs []struct {
		Number int    `json:"number"`
		State  string `json:"state"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &prs); err != nil {
		return false, fmt.Errorf("gh pr list: %w", err)
	}
	for _, pr := range prs {
		if pr.State != "CLOSED" {
			return true, nil
		}
	}
	return false, nil
}

func porcelainCount(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// --- consecutive-block counter ---
//
// <stateDir>/done-gate/<ticket> holds "<session_id> <n>". A different
// session id reads as zero (the cap is per session); a missing or
// malformed file reads as zero too.

func gatePath(stateDir, ticket string) string {
	return filepath.Join(stateDir, "done-gate", ticket)
}

func gateBlocks(stateDir, ticket, session string) int {
	raw, err := os.ReadFile(gatePath(stateDir, ticket))
	if err != nil {
		return 0
	}
	sid, n, ok := strings.Cut(strings.TrimSpace(string(raw)), " ")
	if !ok || sid != session {
		return 0
	}
	count, _ := strconv.Atoi(n)
	return count
}

func gateRecord(stateDir, ticket, session string, n int) {
	path := gatePath(stateDir, ticket)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(session+" "+strconv.Itoa(n)+"\n"), 0o644)
}

func gateReset(stateDir, ticket string) {
	_ = os.Remove(gatePath(stateDir, ticket))
}
