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

// batchGH is a stub gh for the grove-541 batched fetch. It appends every
// invocation's arguments to $GH_LOG, answers `--state open` with the
// repo's open PRs (two branches, newest first, one duplicate head), and
// `--head <branch>` with a merged PR for "merged-branch" or nothing. The
// repo is told apart by the working directory's basename so one stub can
// serve several repos.
const batchGH = `#!/bin/sh
echo "$(basename "$PWD") $*" >> "$GH_LOG"
case "$*" in
  *"--state open"*)
    cat <<'JSON'
[{"number": 10, "url": "https://github.com/o/r/pull/10", "state": "OPEN", "headRefName": "open-a", "isDraft": false, "mergeable": "MERGEABLE", "mergeStateStatus": "CLEAN",
  "statusCheckRollup": [{"name": "build", "status": "COMPLETED", "conclusion": "SUCCESS"}]},
 {"number": 9, "url": "https://github.com/o/r/pull/9", "state": "OPEN", "headRefName": "open-a", "isDraft": true, "mergeable": "UNKNOWN", "mergeStateStatus": "UNKNOWN"},
 {"number": 11, "url": "https://github.com/o/r/pull/11", "state": "OPEN", "headRefName": "open-b", "isDraft": true, "mergeable": "CONFLICTING", "mergeStateStatus": "DIRTY",
  "statusCheckRollup": [{"name": "lint", "status": "COMPLETED", "conclusion": "FAILURE"}]}]
JSON
    ;;
  *"--head merged-branch"*)
    cat <<'JSON'
[{"number": 5, "url": "https://github.com/o/r/pull/5", "state": "MERGED", "mergedAt": "2026-10-01T00:00:00Z", "isDraft": false, "mergeable": "UNKNOWN", "mergeStateStatus": "UNKNOWN"}]
JSON
    ;;
  *) echo '[]' ;;
esac
`

// ghLog reads the stub's invocation log, one line per call.
func ghLog(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func countContaining(lines []string, sub string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, sub) {
			n++
		}
	}
	return n
}

// TestFetchAllOneListPerRepo (grove-541): N tracked branches in one repo
// cost ONE `--state open` list, matched to branches locally, plus a
// `--head` fallback only for the branches the list did not cover — the
// merged one (which the fallback must still report MERGED) and the one
// with no PR at all. The newest open PR wins when a branch has two.
func TestFetchAllOneListPerRepo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub script is a POSIX shell script")
	}
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo1")
	os.Mkdir(repo, 0o755)
	log := filepath.Join(dir, "gh.log")
	t.Setenv("GH_LOG", log)
	stubGH(t, dir, batchGH)

	lookups := map[string][2]string{
		"t-a":      {repo, "open-a"},
		"t-b":      {repo, "open-b"},
		"t-merged": {repo, "merged-branch"},
		"t-none":   {repo, "no-pr-branch"},
	}
	prs, unknown := FetchAll(lookups)
	if len(unknown) != 0 {
		t.Fatalf("unknown = %v, want none", unknown)
	}
	if pr := prs["t-a"]; pr == nil || pr.Number != 10 || pr.CI != "pass" || pr.Draft {
		t.Errorf("t-a = %+v, want #10 OPEN CI pass (newest open PR for the branch)", pr)
	}
	if pr := prs["t-b"]; pr == nil || pr.Number != 11 || pr.CI != "fail" || pr.Mergeable != "CONFLICTING" {
		t.Errorf("t-b = %+v, want #11 CONFLICTING CI fail", pr)
	}
	if pr := prs["t-merged"]; pr == nil || pr.Number != 5 || pr.State != "MERGED" {
		t.Errorf("t-merged = %+v, want #5 MERGED via the per-branch fallback", pr)
	}
	if _, ok := prs["t-none"]; ok {
		t.Errorf("t-none in prs = %+v, want absent (no PR)", prs["t-none"])
	}

	lines := ghLog(t, log)
	if n := countContaining(lines, "--state open"); n != 1 {
		t.Errorf("open-PR list calls = %d, want 1:\n%s", n, strings.Join(lines, "\n"))
	}
	if n := countContaining(lines, "--head"); n != 2 {
		t.Errorf("--head fallback calls = %d, want 2 (merged-branch, no-pr-branch only):\n%s", n, strings.Join(lines, "\n"))
	}
	for _, b := range []string{"open-a", "open-b"} {
		if countContaining(lines, "--head "+b) != 0 {
			t.Errorf("per-branch call issued for %s, which the list already covered", b)
		}
	}
}

// TestFetchAllOneListPerDistinctRepo: lookups spanning two repo paths (two
// workspaces, or one workspace with two repos) cost one list each, and
// two tasks sharing a repo path share its list.
func TestFetchAllOneListPerDistinctRepo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub script is a POSIX shell script")
	}
	dir := t.TempDir()
	r1, r2 := filepath.Join(dir, "repo1"), filepath.Join(dir, "repo2")
	os.Mkdir(r1, 0o755)
	os.Mkdir(r2, 0o755)
	log := filepath.Join(dir, "gh.log")
	t.Setenv("GH_LOG", log)
	stubGH(t, dir, batchGH)

	prs, unknown := FetchAll(map[string][2]string{
		"ws1-a": {r1, "open-a"},
		"ws1-b": {r1, "open-b"},
		"ws2-a": {r2, "open-a"},
	})
	if len(unknown) != 0 || len(prs) != 3 {
		t.Fatalf("prs=%v unknown=%v, want 3 PRs and no unknown", prs, unknown)
	}
	lines := ghLog(t, log)
	if n := countContaining(lines, "--state open"); n != 2 {
		t.Errorf("open-PR list calls = %d, want 2 (one per repo):\n%s", n, strings.Join(lines, "\n"))
	}
	if countContaining(lines, "repo1 ") != 1 || countContaining(lines, "repo2 ") != 1 {
		t.Errorf("want exactly one call in each repo dir:\n%s", strings.Join(lines, "\n"))
	}
}

// TestFetchAllListFailureMarksRepoUnknown: when the repo's list fails
// (both with and without CI fields) every key of that repo is unknown —
// never "no PR" — and no per-branch fallback is attempted for it. The
// other repo is unaffected.
func TestFetchAllListFailureMarksRepoUnknown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub script is a POSIX shell script")
	}
	dir := t.TempDir()
	bad, good := filepath.Join(dir, "bad"), filepath.Join(dir, "good")
	os.Mkdir(bad, 0o755)
	os.Mkdir(good, 0o755)
	log := filepath.Join(dir, "gh.log")
	t.Setenv("GH_LOG", log)
	stubGH(t, dir, `#!/bin/sh
echo "$(basename "$PWD") $*" >> "$GH_LOG"
case "$(basename "$PWD")" in
  bad) echo 'boom' >&2; exit 1 ;;
esac
`+strings.TrimPrefix(batchGH, "#!/bin/sh\necho \"$(basename \"$PWD\") $*\" >> \"$GH_LOG\"\n"))

	prs, unknown := FetchAll(map[string][2]string{
		"bad-a":    {bad, "open-a"},
		"bad-none": {bad, "no-pr-branch"},
		"good-a":   {good, "open-a"},
	})
	for _, k := range []string{"bad-a", "bad-none"} {
		if _, ok := unknown[k]; !ok {
			t.Errorf("%s not in unknown (%v) — a failed list must never read as no PR", k, unknown)
		}
		if _, ok := prs[k]; ok {
			t.Errorf("%s in prs, want absent", k)
		}
	}
	if pr := prs["good-a"]; pr == nil || pr.Number != 10 {
		t.Errorf("good-a = %+v, want #10", pr)
	}
	if _, ok := unknown["good-a"]; ok {
		t.Errorf("good-a unknown, want resolved")
	}
	lines := ghLog(t, log)
	if n := countContaining(lines, "bad --head"); n != 0 {
		t.Errorf("fallback calls in the failed repo = %d, want 0:\n%s", n, strings.Join(lines, "\n"))
	}
}

// TestFetchAllListDegradesCIOn403 (grove-451 carried to the batch): a
// token that cannot read statusCheckRollup gets the open PRs from the
// retried fact-only list with CI "unknown", instead of the whole repo
// landing in unknown.
func TestFetchAllListDegradesCIOn403(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub script is a POSIX shell script")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "gh.log")
	t.Setenv("GH_LOG", log)
	stubGH(t, dir, `#!/bin/sh
echo "$*" >> "$GH_LOG"
case "$*" in
  *statusCheckRollup*) echo 'GraphQL: Resource not accessible by integration (statusCheckRollup)' >&2; exit 1 ;;
  *"--state open"*)
    echo '[{"number": 10, "url": "u", "state": "OPEN", "headRefName": "open-a", "isDraft": false, "mergeable": "MERGEABLE", "mergeStateStatus": "CLEAN"}]' ;;
  *) echo '[]' ;;
esac
`)
	prs, unknown := FetchAll(map[string][2]string{"t-a": {dir, "open-a"}})
	if len(unknown) != 0 {
		t.Fatalf("unknown = %v, want none", unknown)
	}
	pr := prs["t-a"]
	if pr == nil || pr.Number != 10 {
		t.Fatalf("t-a = %+v, want #10", pr)
	}
	if pr.CI != "unknown" {
		t.Errorf("CI = %q, want unknown on the degraded list", pr.CI)
	}
	lines := ghLog(t, log)
	if n := countContaining(lines, "--state open"); n != 2 {
		t.Errorf("list calls = %d, want 2 (full, then fact-only retry):\n%s", n, strings.Join(lines, "\n"))
	}
}
