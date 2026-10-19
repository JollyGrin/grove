package github

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

// stubGH puts a fake `gh` on PATH that always prints script's stdout (or
// runs it verbatim, for a non-echo script) — the TestGHTimesOut pattern.
func stubGH(t *testing.T, dir, script string) {
	t.Helper()
	stub := filepath.Join(dir, "gh")
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub gh: %v", err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath)
}

// TestGHTimesOut verifies a wedged gh (stalled network, offline) is bounded
// by ghTimeout instead of hanging the caller forever — grove-164.
func TestGHTimesOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub script is a POSIX shell script")
	}

	dir := t.TempDir()
	stub := filepath.Join(dir, "gh")
	// `exec sleep`: a wedged gh is ONE hung process. A plain `sleep` child
	// would inherit the stdout/stderr pipes, survive the deadline's kill
	// of its parent sh, and hold Wait open past the bound — modeling the
	// orphan-descendant hole, not the wedge this test is about.
	script := "#!/bin/sh\nexec sleep 5\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub gh: %v", err)
	}

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath)

	oldTimeout := ghTimeout
	ghTimeout = 100 * time.Millisecond
	defer func() { ghTimeout = oldTimeout }()

	start := time.Now()
	_, err := gh(dir, "pr", "list")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from timed-out gh, got nil")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("gh() took %v to return after a wedged call, want well under the deadline bound", elapsed)
	}
}

// TestPRForBranchFacts (grove-251): PRForBranch requests and decodes the
// draft/mergeable/mergeStateStatus facts the supervisor's transition engine
// needs, and TIMED_OUT/CANCELLED/ACTION_REQUIRED count as CI failures (not
// neither-pass-nor-fail) — a PR whose only failing check was cancelled must
// read CI "fail", not "pass".
func TestPRForBranchFacts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub script is a POSIX shell script")
	}

	dir := t.TempDir()
	fixture := `[{
		"number": 42,
		"url": "https://github.com/o/r/pull/42",
		"state": "OPEN",
		"mergedAt": "",
		"isDraft": true,
		"mergeable": "CONFLICTING",
		"mergeStateStatus": "DIRTY",
		"comments": [],
		"statusCheckRollup": [
			{"name": "build", "status": "COMPLETED", "conclusion": "SUCCESS"},
			{"name": "zebra", "status": "COMPLETED", "conclusion": "FAILURE"},
			{"name": "lint", "status": "COMPLETED", "conclusion": "CANCELLED"},
			{"name": "apple", "status": "COMPLETED", "conclusion": "ACTION_REQUIRED"},
			{"context": "vercel", "state": "SUCCESS"}
		]
	}]`
	stubGH(t, dir, "#!/bin/sh\ncat <<'EOF'\n"+fixture+"\nEOF\n")

	pr, err := PRForBranch(dir, "some-branch")
	if err != nil {
		t.Fatalf("PRForBranch: %v", err)
	}
	if pr == nil {
		t.Fatal("expected a PR, got nil")
	}
	if !pr.Draft {
		t.Error("Draft = false, want true")
	}
	if pr.Mergeable != "CONFLICTING" {
		t.Errorf("Mergeable = %q, want CONFLICTING", pr.Mergeable)
	}
	if pr.MergeState != "DIRTY" {
		t.Errorf("MergeState = %q, want DIRTY", pr.MergeState)
	}
	if want := []string{"apple", "lint", "zebra"}; !reflect.DeepEqual(pr.Failing, want) {
		t.Errorf("Failing = %v, want %v (sorted)", pr.Failing, want)
	}
	if pr.Checks != 5 {
		t.Errorf("Checks = %d, want 5", pr.Checks)
	}
	if pr.CI != "fail" {
		t.Errorf("CI = %q, want fail (a CANCELLED check must count as a failure)", pr.CI)
	}
}

// TestFetchAllUnknownOnError: a `gh` that exits nonzero must land its key in
// unknown, never silently drop it — "lookup failed" and "no PR" must stay
// distinguishable so the transition engine never emits from a failed lookup.
func TestFetchAllUnknownOnError(t *testing.T) {
	dir := t.TempDir()
	stubGH(t, dir, "#!/bin/sh\necho boom >&2\nexit 1\n")

	lookups := map[string][2]string{"grove-1": {dir, "some-branch"}}
	prs, unknown := FetchAll(lookups)
	if len(prs) != 0 {
		t.Errorf("prs = %v, want empty", prs)
	}
	if _, ok := unknown["grove-1"]; !ok {
		t.Fatal("expected grove-1 in unknown, got none")
	}
}

// TestFetchAllUnknownOnTimeout: a wedged `gh` (bounded by ghTimeout) must
// also land its key in unknown rather than being dropped as "no PR".
func TestFetchAllUnknownOnTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub script is a POSIX shell script")
	}

	dir := t.TempDir()
	stubGH(t, dir, "#!/bin/sh\nexec sleep 5\n")

	oldTimeout := ghTimeout
	ghTimeout = 100 * time.Millisecond
	defer func() { ghTimeout = oldTimeout }()

	lookups := map[string][2]string{"grove-1": {dir, "some-branch"}}
	prs, unknown := FetchAll(lookups)
	if len(prs) != 0 {
		t.Errorf("prs = %v, want empty", prs)
	}
	if _, ok := unknown["grove-1"]; !ok {
		t.Fatal("expected grove-1 in unknown, got none")
	}
}

// ciDenyingGH is a stub gh that models a GitHub App token (grove-451): any
// query that asks for statusCheckRollup gets a 403, every other query
// returns one merged PR.
const ciDenyingGH = `#!/bin/sh
case "$*" in
  *statusCheckRollup*) echo 'GraphQL: Resource not accessible by integration (statusCheckRollup)' >&2; exit 1 ;;
esac
cat <<'JSON'
[{"number": 7, "url": "https://github.com/o/r/pull/7", "state": "MERGED", "mergedAt": "2026-10-01T00:00:00Z", "isDraft": false, "mergeable": "UNKNOWN", "mergeStateStatus": "UNKNOWN"}]
JSON
`

// TestMergedDespiteCIFieldFailure (grove-451): the merge gate never looks
// at CI, so a token that cannot read statusCheckRollup must still produce
// a merged verdict — previously every `gv done` on a merged ticket failed
// and forced --force.
func TestMergedDespiteCIFieldFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub script is a POSIX shell script")
	}
	dir := t.TempDir()
	stubGH(t, dir, ciDenyingGH)

	merged, pr, err := Merged(dir, "some-branch")
	if err != nil {
		t.Fatalf("Merged: %v", err)
	}
	if !merged {
		t.Fatal("merged = false, want true")
	}
	if pr == nil || pr.Number != 7 || pr.State != "MERGED" {
		t.Fatalf("pr = %+v, want #7 MERGED", pr)
	}
	if pr.CI != "unknown" {
		t.Errorf("CI = %q, want unknown (gate query asks for no CI fields)", pr.CI)
	}
}

// TestPRForBranchDegradesCIOn403 (grove-451): the display path retries
// without the CI fields and returns the PR with CI "unknown" instead of
// failing the whole fetch — ls/TUI keep the PR number, blank the CI column.
func TestPRForBranchDegradesCIOn403(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub script is a POSIX shell script")
	}
	dir := t.TempDir()
	stubGH(t, dir, ciDenyingGH)

	pr, err := PRForBranch(dir, "some-branch")
	if err != nil {
		t.Fatalf("PRForBranch: %v", err)
	}
	if pr == nil {
		t.Fatal("expected a PR, got nil")
	}
	if pr.Number != 7 || pr.State != "MERGED" {
		t.Errorf("pr = %+v, want #7 MERGED", pr)
	}
	if pr.CI != "unknown" {
		t.Errorf("CI = %q, want unknown", pr.CI)
	}
	if pr.Checks != 0 || len(pr.Failing) != 0 {
		t.Errorf("Checks/Failing = %d/%v, want none on the degraded path", pr.Checks, pr.Failing)
	}
}

// TestPRForBranchErrorsWhenRetryFails: when gh fails even without the CI
// fields (offline, bad auth) the error still surfaces — degradation is
// only for the CI fields, never a blanket swallow.
func TestPRForBranchErrorsWhenRetryFails(t *testing.T) {
	dir := t.TempDir()
	stubGH(t, dir, "#!/bin/sh\necho boom >&2\nexit 1\n")

	pr, err := PRForBranch(dir, "some-branch")
	if err == nil {
		t.Fatalf("expected an error, got pr=%+v", pr)
	}
}
