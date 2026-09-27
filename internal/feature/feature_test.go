package feature

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JollyGrin/grove/internal/state"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// fixture: a bare origin with main (two commits) and a clone as the repo
// root gv works in. No network.
func fixture(t *testing.T) (root, origin, stateDir string) {
	t.Helper()
	base := t.TempDir()
	origin = filepath.Join(base, "origin.git")
	root = filepath.Join(base, "repo")
	stateDir = filepath.Join(base, "state")
	os.MkdirAll(stateDir, 0o755)
	gitIn(t, base, "init", "-q", "--bare", "-b", "main", origin)
	gitIn(t, base, "clone", "-q", origin, root)
	gitIn(t, root, "config", "user.email", "t@t")
	gitIn(t, root, "config", "user.name", "t")
	gitIn(t, root, "checkout", "-q", "-b", "main")
	for _, msg := range []string{"one", "two"} {
		gitIn(t, root, "commit", "-q", "--allow-empty", "-m", msg)
	}
	gitIn(t, root, "push", "-q", "origin", "main")
	return root, origin, stateDir
}

func eventLines(t *testing.T, stateDir string) []string {
	b, err := os.ReadFile(filepath.Join(stateDir, "events.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestValidateSlug(t *testing.T) {
	for _, ok := range []string{"keys", "feature-trains", "a1", "v2-x"} {
		if err := ValidateSlug(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Keys", "a_b", "a b", "-a", "a-", "a--b", "a/b", "ü"} {
		if ValidateSlug(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestNewCreatesAndPushesAtOriginBase(t *testing.T) {
	root, origin, sd := fixture(t)
	mainSHA := gitIn(t, origin, "rev-parse", "main")

	res, err := New(sd, root, Spec{Slug: "keys", Repo: "grove"})
	if err != nil {
		t.Fatal(err)
	}
	if got := gitIn(t, origin, "rev-parse", "refs/heads/feature/keys"); got != mainSHA {
		t.Errorf("feature/keys on origin = %s, want origin/main %s", got, mainSHA)
	}
	if !res.Created || res.SHA != mainSHA {
		t.Errorf("result = %+v", res)
	}
	if out := gitIn(t, root, "branch", "--list", "feature/keys"); out != "" {
		t.Errorf("new created a local branch: %q", out)
	}
	fs, _ := state.LoadFeatures(sd)
	f := fs["keys"]
	if f == nil || f.Branch != "feature/keys" || f.Base != "main" || f.Label != "keys" || f.Repo != "grove" || f.Closed {
		t.Fatalf("folded feature = %+v", f)
	}
	if n := len(eventLines(t, sd)); n != 1 {
		t.Errorf("%d events, want 1", n)
	}
}

func TestNewRefusesExistingRemoteBranchWithoutAdopt(t *testing.T) {
	root, _, sd := fixture(t)
	gitIn(t, root, "push", "-q", "origin", "main:refs/heads/feature/keys")

	_, err := New(sd, root, Spec{Slug: "keys", Repo: "grove"})
	if err == nil || !strings.Contains(err.Error(), "--adopt") {
		t.Fatalf("err = %v, want a refusal naming --adopt", err)
	}
	if n := len(eventLines(t, sd)); n != 0 {
		t.Errorf("refusal appended %d events", n)
	}
}

func TestAdoptMissingBranchRefused(t *testing.T) {
	root, _, sd := fixture(t)
	if _, err := New(sd, root, Spec{Slug: "keys", Repo: "grove", Adopt: true}); err == nil {
		t.Fatal("adopt of a missing branch succeeded")
	}
	if n := len(eventLines(t, sd)); n != 0 {
		t.Errorf("refusal appended %d events", n)
	}
}

func TestAdoptExistingAppendsOneEventPushesNothing(t *testing.T) {
	root, origin, sd := fixture(t)
	gitIn(t, root, "commit", "-q", "--allow-empty", "-m", "train work")
	gitIn(t, root, "push", "-q", "origin", "HEAD:refs/heads/feature/gv-keys-secrets")
	before := gitIn(t, origin, "for-each-ref")

	res, err := New(sd, root, Spec{Slug: "keys", Repo: "grove", Branch: "feature/gv-keys-secrets", Label: "gv-keys", Adopt: true})
	if err != nil {
		t.Fatal(err)
	}
	if after := gitIn(t, origin, "for-each-ref"); after != before {
		t.Errorf("adopt changed origin refs:\n%s\n→\n%s", before, after)
	}
	if res.Created || res.SHA != gitIn(t, origin, "rev-parse", "feature/gv-keys-secrets") {
		t.Errorf("result = %+v", res)
	}
	lines := eventLines(t, sd)
	if len(lines) != 1 || !strings.Contains(lines[0], `"type":"feature_created"`) ||
		!strings.Contains(lines[0], `"ticket":""`) || !strings.Contains(lines[0], `"label":"gv-keys"`) {
		t.Fatalf("events = %v", lines)
	}
}

func TestNewRefusesOpenSlugAllowsAfterClose(t *testing.T) {
	root, _, sd := fixture(t)
	if _, err := New(sd, root, Spec{Slug: "keys", Repo: "grove"}); err != nil {
		t.Fatal(err)
	}
	_, err := New(sd, root, Spec{Slug: "keys", Repo: "grove", Adopt: true})
	if err == nil || !strings.Contains(err.Error(), "already open") {
		t.Fatalf("second new of an open slug: err = %v", err)
	}
	if _, err := Close(sd, "keys", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := New(sd, root, Spec{Slug: "keys", Repo: "grove", Adopt: true}); err != nil {
		t.Fatalf("re-create after close: %v", err)
	}
	fs, _ := state.LoadFeatures(sd)
	if fs["keys"].Closed {
		t.Error("re-created feature reads closed")
	}
}

func TestNewBadSlugRefusedBeforeGit(t *testing.T) {
	root, origin, sd := fixture(t)
	before := gitIn(t, origin, "for-each-ref")
	if _, err := New(sd, root, Spec{Slug: "Bad_Slug", Repo: "grove"}); err == nil {
		t.Fatal("bad slug accepted")
	}
	if gitIn(t, origin, "for-each-ref") != before || len(eventLines(t, sd)) != 0 {
		t.Error("bad slug touched origin or the log")
	}
}

func TestClose(t *testing.T) {
	root, _, sd := fixture(t)
	if _, err := Close(sd, "keys", ""); err == nil {
		t.Error("close of an unknown feature succeeded")
	}
	if _, err := New(sd, root, Spec{Slug: "keys", Repo: "grove"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Close(sd, "keys", "shipped"); err == nil {
		t.Error("bad reason accepted")
	}
	if _, err := Close(sd, "keys", state.FeatureAbandoned); err != nil {
		t.Fatal(err)
	}
	fs, _ := state.LoadFeatures(sd)
	if f := fs["keys"]; !f.Closed || f.ClosedReason != state.FeatureAbandoned {
		t.Errorf("closed feature = %+v", f)
	}
	if _, err := Close(sd, "keys", ""); err == nil {
		t.Error("double close succeeded")
	}
	// Close deletes nothing: the branch is still on origin.
	if out := gitIn(t, root, "ls-remote", "origin", "refs/heads/feature/keys"); out == "" {
		t.Error("close removed the remote branch")
	}
}

func TestRows(t *testing.T) {
	fs := map[string]*state.Feature{
		"b": {Slug: "b", CreatedAt: t1(2)},
		"a": {Slug: "a", CreatedAt: t1(1)},
		"c": {Slug: "c", CreatedAt: t1(0), Closed: true, ClosedReason: "merged", ClosedAt: t1(3)},
	}
	open := Rows(fs, false)
	if len(open) != 2 || open[0].Slug != "a" || open[1].Slug != "b" || open[0].Closed != nil {
		t.Fatalf("open rows = %+v", open)
	}
	all := Rows(fs, true)
	if len(all) != 3 || all[0].Slug != "c" || all[0].Closed == nil || all[0].Closed.Reason != "merged" {
		t.Fatalf("all rows = %+v", all)
	}
	if rows := Rows(nil, false); rows == nil {
		t.Error("empty rows must marshal as [], not null")
	}
}

func t1(h int) time.Time { return time.Date(2026, 9, 27, h, 0, 0, 0, time.UTC) }
