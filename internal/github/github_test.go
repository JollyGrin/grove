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

// ghStubCIForbidden is a `gh` whose full query (the one carrying
// statusCheckRollup) fails the way a GitHub App token does — HTTP 403 on
// the checks field — while any query without that field answers with the
// fixture. Models the grove-old-145 cascade.
func ghStubCIForbidden(fixture string) string {
	return "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"  *statusCheckRollup*) echo 'GraphQL: Resource not accessible by integration (statusCheckRollup)' >&2; exit 1;;\n" +
		"esac\n" +
		"cat <<'EOF'\n" + fixture + "\nEOF\n"
}

const mergedFixture = `[{
	"number": 7,
	"url": "https://github.com/o/r/pull/7",
	"state": "MERGED",
	"mergedAt": "2026-10-07T10:00:00Z",
	"isDraft": false,
	"mergeable": "UNKNOWN",
	"mergeStateStatus": "UNKNOWN"
}]`

// TestMergedIgnoresCIField (grove-old-145): the merge gate never looks at
// CI, so a token that cannot read statusCheckRollup must still get a
// MERGED verdict — and the gate's query must not even ask for the field.
func TestMergedIgnoresCIField(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub script is a POSIX shell script")
	}
	dir := t.TempDir()
	stubGH(t, dir, ghStubCIForbidden(mergedFixture))

	merged, pr, err := Merged(dir, "some-branch")
	if err != nil {
		t.Fatalf("Merged: %v", err)
	}
	if !merged {
		t.Fatal("merged = false, want true despite the CI field being unreadable")
	}
	if pr == nil || pr.Number != 7 || pr.State != "MERGED" {
		t.Fatalf("pr = %+v, want #7 MERGED", pr)
	}
	if pr.CI != "unknown" {
		t.Errorf("CI = %q, want unknown (the gate never asks for CI)", pr.CI)
	}
}

// TestMergedDistinguishesNoPRFromGHFailure: nil/nil is "no PR exists";
// a gh failure is an error — finishTask relies on telling them apart.
func TestMergedDistinguishesNoPRFromGHFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub script is a POSIX shell script")
	}
	dir := t.TempDir()
	stubGH(t, dir, "#!/bin/sh\necho '[]'\n")
	merged, pr, err := Merged(dir, "some-branch")
	if err != nil || merged || pr != nil {
		t.Fatalf("empty list: got (%v, %v, %v), want (false, nil, nil)", merged, pr, err)
	}

	stubGH(t, dir, "#!/bin/sh\necho 'HTTP 403' >&2\nexit 1\n")
	merged, pr, err = Merged(dir, "some-branch")
	if err == nil {
		t.Fatal("gh failure: want an error, got nil (would read as 'no PR')")
	}
	if merged || pr != nil {
		t.Fatalf("gh failure: got (%v, %v), want (false, nil)", merged, pr)
	}
}

// TestPRForBranchDegradesWithoutCI (grove-old-145): when the full query
// 403s on statusCheckRollup, the display path retries without the CI +
// comments fields and still yields the PR, with CI "unknown" (rendered as
// "?") rather than failing the whole fetch and blanking the PR column.
func TestPRForBranchDegradesWithoutCI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub script is a POSIX shell script")
	}
	dir := t.TempDir()
	stubGH(t, dir, ghStubCIForbidden(mergedFixture))

	pr, err := PRForBranch(dir, "some-branch")
	if err != nil {
		t.Fatalf("PRForBranch: %v", err)
	}
	if pr == nil {
		t.Fatal("expected a PR from the degraded retry, got nil")
	}
	if pr.Number != 7 || pr.State != "MERGED" || pr.Mergeable != "UNKNOWN" {
		t.Errorf("pr = %+v, want #7 MERGED with the degraded facts decoded", pr)
	}
	if pr.CI != "unknown" {
		t.Errorf("CI = %q, want unknown", pr.CI)
	}
	if pr.Checks != 0 || len(pr.Failing) != 0 {
		t.Errorf("Checks/Failing = %d/%v, want none on the degraded path", pr.Checks, pr.Failing)
	}

	// Both queries failing is still an error — degradation is one retry,
	// not a license to invent a PR.
	stubGH(t, dir, "#!/bin/sh\necho 'HTTP 403' >&2\nexit 1\n")
	if _, err := PRForBranch(dir, "some-branch"); err == nil {
		t.Fatal("both queries failing: want an error, got nil")
	}
}

// TestPRForBranchFullQueryStaysFull: a healthy token must not be degraded
// — the one-shot retry only runs after the full query errors.
func TestPRForBranchFullQueryStaysFull(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub script is a POSIX shell script")
	}
	dir := t.TempDir()
	fixture := `[{"number": 1, "url": "u", "state": "OPEN",
		"statusCheckRollup": [{"name": "build", "status": "COMPLETED", "conclusion": "SUCCESS"}],
		"comments": [{"body": "preview: https://my-app-abc.vercel.app/ ok"}]}]`
	stubGH(t, dir, "#!/bin/sh\ncase \"$*\" in *statusCheckRollup*) ;; *) echo 'unexpected degraded query' >&2; exit 1;; esac\ncat <<'EOF'\n"+fixture+"\nEOF\n")

	pr, err := PRForBranch(dir, "some-branch")
	if err != nil {
		t.Fatalf("PRForBranch: %v", err)
	}
	if pr.CI != "pass" {
		t.Errorf("CI = %q, want pass", pr.CI)
	}
	if pr.PreviewURL != "https://my-app-abc.vercel.app/" {
		t.Errorf("PreviewURL = %q, want the bot-comment link", pr.PreviewURL)
	}
}
