package provider

import (
	"errors"
	"strings"
	"testing"
)

func ghStub(t *testing.T, wantDir string, responses map[string]string) *GitHub {
	t.Helper()
	g := NewGitHub(wantDir, "unbrewed-p2p")
	g.run = func(dir string, args ...string) ([]byte, error) {
		if dir != wantDir {
			t.Errorf("gh ran in %s, want %s", dir, wantDir)
		}
		key := strings.Join(args[:2], " ")
		resp, ok := responses[key]
		if !ok {
			return nil, errors.New("gh: unexpected call " + key)
		}
		return []byte(resp), nil
	}
	return g
}

func TestGitHubParseID(t *testing.T) {
	g := NewGitHub("/r", "unbrewed-p2p")
	for raw, want := range map[string]string{
		"7":              "unbrewed-p2p-7",
		"#7":             "unbrewed-p2p-7",
		" #42 ":          "unbrewed-p2p-42",
		"unbrewed-p2p-7": "unbrewed-p2p-7",
		"UNBREWED-P2P-7": "unbrewed-p2p-7",
		"https://github.com/JollyGrin/unbrewed-p2p/issues/7":         "unbrewed-p2p-7",
		"https://github.com/JollyGrin/unbrewed-p2p/issues/7#issue-1": "unbrewed-p2p-7",
	} {
		got, err := g.ParseID(raw)
		if err != nil || got != want {
			t.Errorf("ParseID(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, bad := range []string{"", "seven", "other-repo-7", "unbrewed-p2p-x", "https://github.com/x/y/pull/7"} {
		if got, err := g.ParseID(bad); err == nil {
			t.Errorf("ParseID(%q) = %q, want error", bad, got)
		}
	}
}

func TestGitHubGet(t *testing.T) {
	g := ghStub(t, "/repos/p2p", map[string]string{
		"issue view": `{"number":7,"title":"Bot: humanlike delay","body":"Details here","url":"https://github.com/JollyGrin/unbrewed-p2p/issues/7","state":"OPEN","labels":[{"name":"bug"},{"name":"bot"}],"comments":[{"author":{"login":"jollygrin"},"body":"repro attached"},{"author":{},"body":"anon note"}]}`,
	})
	task, err := g.Get("unbrewed-p2p-7")
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "unbrewed-p2p-7" || task.Title != "Bot: humanlike delay" || task.Status != "open" {
		t.Errorf("task = %+v", task)
	}
	if len(task.Labels) != 2 || task.Labels[0] != "bug" {
		t.Errorf("labels = %v", task.Labels)
	}
	if len(task.Comments) != 2 || task.Comments[0].Author != "jollygrin" || task.Comments[1].Author != "unknown" {
		t.Errorf("comments = %+v", task.Comments)
	}
	if _, err := g.Get("other-7"); err == nil {
		t.Error("foreign id must error")
	}
}

func TestGitHubListAndCap(t *testing.T) {
	g := ghStub(t, "/r", map[string]string{
		"issue list": `[{"number":10,"title":"B","labels":[]},{"number":7,"title":"A","labels":[{"name":"bug"}]}]`,
	})
	tasks, err := g.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].ID != "unbrewed-p2p-7" || tasks[1].ID != "unbrewed-p2p-10" {
		t.Errorf("order/ids wrong: %v %v", tasks[0].ID, tasks[1].ID)
	}
	if tasks[0].Status != "open" {
		t.Errorf("list status = %q", tasks[0].Status)
	}
	if g.ListCapped() {
		t.Error("2 issues must not read as capped")
	}

	// A full page sets the cap flag.
	var sb strings.Builder
	sb.WriteString("[")
	for i := 0; i < 200; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"number":` + itoa(i+1) + `,"title":"t","labels":[]}`)
	}
	sb.WriteString("]")
	g2 := ghStub(t, "/r", map[string]string{"issue list": sb.String()})
	if _, err := g2.List(); err != nil {
		t.Fatal(err)
	}
	if !g2.ListCapped() {
		t.Error("full page must set the cap flag")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestGitHubErrorsSurfaceGhStderr(t *testing.T) {
	g := NewGitHub("/r", "p2p")
	g.run = func(string, ...string) ([]byte, error) {
		return nil, errors.New("gh issue view: exit status 1\nGraphQL: Could not resolve")
	}
	if _, err := g.Get("p2p-9"); err == nil || !strings.Contains(err.Error(), "Could not resolve") {
		t.Errorf("gh stderr must surface, got %v", err)
	}
}

func TestGitHubVerbsNeverClose(t *testing.T) {
	v := NewGitHub("/r", "p2p").Verbs()
	if !strings.Contains(v.Start, "in-progress") || !strings.Contains(v.Review, "Closes #") {
		t.Errorf("verbs missing label/close-link mechanics: %+v", v)
	}
	for _, banned := range []string{"gh issue close", "gh issue delete"} {
		if strings.Contains(v.Start+v.Review, banned) {
			t.Errorf("verbs must never close/delete issues: %q", banned)
		}
	}
	if !strings.Contains(v.Review, "NEVER close") {
		t.Error("review verb must forbid closing (humans finish)")
	}
}

// Canonical github ids resolve through IDCandidates to themselves — the
// unanchored linear regex also emits a spurious P2P-7 candidate, and the
// tracked-state membership check is what arbitrates (review S-1 pin).
func TestIDCandidatesCanonicalGithubID(t *testing.T) {
	got := IDCandidates("unbrewed-p2p-7")
	found := false
	for _, c := range got {
		if c == "unbrewed-p2p-7" {
			found = true
		}
	}
	if !found {
		t.Errorf("canonical github id missing from candidates: %v", got)
	}
}
