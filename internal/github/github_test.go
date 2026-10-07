package github

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
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

// stubGH403 puts a fake `gh` on PATH that models a GitHub App token
// (grove-450): any `pr list` that asks for `statusCheckRollup` fails with
// a 403, while a lean query succeeds with the given PR fixture. Every
// invocation's argv is appended to logPath, one line per call.
func stubGH403(t *testing.T, dir, logPath, fixture string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub script is a POSIX shell script")
	}
	script := "#!/bin/sh\n" +
		"echo \"$*\" >> " + logPath + "\n" +
		"case \"$*\" in\n" +
		"  *statusCheckRollup*) echo 'GraphQL: Resource not accessible by integration (repository.pullRequests.nodes.statusCheckRollup)' >&2; exit 1 ;;\n" +
		"  *) cat <<'EOF2'\n" + fixture + "\nEOF2\n ;;\n" +
		"esac\n"
	stubGH(t, dir, script)
}

func ghCalls(t *testing.T, logPath string) []string {
	t.Helper()
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read gh log: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// TestMergedIgnoresCIFields (grove-450): the merge gate never needed CI,
// so it must not request `statusCheckRollup` — with an App token that
// 403s on that field, a genuinely merged PR still reads merged instead of
// funneling the operator to --force.
func TestMergedIgnoresCIFields(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "gh.log")
	stubGH403(t, dir, logPath, `[{"number":7,"url":"https://x/7","state":"MERGED","mergedAt":"2026-10-07T00:00:00Z"}]`)

	merged, pr, err := Merged(dir, "some-branch")
	if err != nil {
		t.Fatalf("Merged: %v", err)
	}
	if !merged || pr == nil || pr.Number != 7 || pr.State != "MERGED" {
		t.Fatalf("merged = %v, pr = %+v, want merged PR #7", merged, pr)
	}
	calls := ghCalls(t, logPath)
	if len(calls) != 1 {
		t.Fatalf("gh calls = %q, want exactly one lean query", calls)
	}
	if !strings.Contains(calls[0], "--json number,url,state,mergedAt") || strings.Contains(calls[0], "statusCheckRollup") || strings.Contains(calls[0], "comments") {
		t.Errorf("merge gate query = %q, want --json number,url,state,mergedAt only", calls[0])
	}
}

// TestPRForBranchDegradesWithoutCI (grove-450): the display path keeps
// the full query, but when it fails it retries once without the CI and
// comments fields and renders CI as unknown — one 403 on a bundled field
// must not blank the whole PR column (or the merge gate behind it).
func TestPRForBranchDegradesWithoutCI(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "gh.log")
	stubGH403(t, dir, logPath, `[{"number":8,"url":"https://x/8","state":"OPEN","mergedAt":"","isDraft":false,"mergeable":"MERGEABLE","mergeStateStatus":"CLEAN"}]`)

	pr, err := PRForBranch(dir, "some-branch")
	if err != nil {
		t.Fatalf("PRForBranch: %v", err)
	}
	if pr == nil || pr.Number != 8 || pr.State != "OPEN" || pr.Mergeable != "MERGEABLE" {
		t.Fatalf("pr = %+v, want the lean retry's PR #8", pr)
	}
	if pr.CI != "unknown" {
		t.Errorf("CI = %q, want unknown (CI unreadable, not none)", pr.CI)
	}
	if pr.Checks != 0 || len(pr.Failing) != 0 {
		t.Errorf("Checks = %d, Failing = %v, want no check facts", pr.Checks, pr.Failing)
	}
	calls := ghCalls(t, logPath)
	if len(calls) != 2 {
		t.Fatalf("gh calls = %q, want the full query then one lean retry", calls)
	}
	if !strings.Contains(calls[0], "statusCheckRollup,comments") {
		t.Errorf("first call = %q, want the full query", calls[0])
	}
	if strings.Contains(calls[1], "statusCheckRollup") || strings.Contains(calls[1], "comments") {
		t.Errorf("retry = %q, want no CI or comments fields", calls[1])
	}
}

// TestPRForBranchNoRetryWhenLeanAlsoFails: a gh that is simply broken
// (offline, bad auth) still surfaces an error — the retry is for the
// bundled-field 403, not a way to turn every failure into "no PR".
func TestPRForBranchNoRetryWhenLeanAlsoFails(t *testing.T) {
	dir := t.TempDir()
	stubGH(t, dir, "#!/bin/sh\necho boom >&2\nexit 1\n")
	pr, err := PRForBranch(dir, "some-branch")
	if err == nil {
		t.Fatalf("PRForBranch = %+v, nil; want an error when both queries fail", pr)
	}
}
