// Package github shells out to gh for PR + preview state (the `delivery`
// dimension). Polled lazily on ls/TUI refresh — never a daemon.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// ghTimeout bounds every gh invocation so a wedged gh (offline, stalled
// network) fails fast instead of hanging callers forever — see grove-164.
// Var (not const) so tests can shrink it instead of waiting out the real
// default.
var ghTimeout = 15 * time.Second

type PR struct {
	Number     int    `json:"number"`
	URL        string `json:"url"`
	State      string `json:"state"` // OPEN | MERGED | CLOSED
	MergedAt   string `json:"mergedAt,omitempty"`
	CI         string `json:"ci"`      // pass | fail | pending | none
	PreviewURL string `json:"preview"` // best-effort Vercel link
}

func gh(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gh %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// PRForBranch returns the PR for a head branch, or nil if none exists.
// Comments ride along in the same call because the real *.vercel.app
// preview links live in the Vercel bot comment — status-check targetUrls
// are vercel.com build pages (field-tested on PR #936, twice).
func PRForBranch(repoDir, branch string) (*PR, error) {
	out, err := gh(repoDir, "pr", "list", "--head", branch, "--state", "all", "--limit", "1",
		"--json", "number,url,state,mergedAt,statusCheckRollup,comments")
	if err != nil {
		return nil, err
	}
	var prs []struct {
		Number   int    `json:"number"`
		URL      string `json:"url"`
		State    string `json:"state"`
		MergedAt string `json:"mergedAt"`
		Comments []struct {
			Body string `json:"body"`
		} `json:"comments"`
		StatusCheckRollup []struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			State      string `json:"state"` // StatusContext variant (Vercel uses these)
			TargetURL  string `json:"targetUrl"`
		} `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, err
	}
	if len(prs) == 0 {
		return nil, nil
	}
	p := prs[0]
	pr := &PR{Number: p.Number, URL: p.URL, State: p.State, MergedAt: p.MergedAt, CI: "none"}

	var pass, fail, pending int
	for _, c := range p.StatusCheckRollup {
		// CheckRun: status/conclusion. StatusContext: state.
		switch {
		case c.Conclusion == "SUCCESS" || c.State == "SUCCESS":
			pass++
		case c.Conclusion == "FAILURE" || c.State == "FAILURE" || c.State == "ERROR":
			fail++
		case c.Status == "IN_PROGRESS" || c.Status == "QUEUED" || c.State == "PENDING":
			pending++
		}
		if c.TargetURL != "" && strings.Contains(c.TargetURL, ".vercel.app") {
			pr.PreviewURL = c.TargetURL
		}
	}
	if pr.PreviewURL == "" {
		for _, c := range p.Comments {
			if m := vercelURLRe.FindString(c.Body); m != "" {
				pr.PreviewURL = m
				break
			}
		}
	}
	switch {
	case fail > 0:
		pr.CI = "fail"
	case pending > 0:
		pr.CI = "pending"
	case pass > 0:
		pr.CI = "pass"
	}
	return pr, nil
}

var vercelURLRe = regexp.MustCompile(`https://[a-zA-Z0-9.-]+\.vercel\.app[^\s)\]"]*`)

// PreviewURL digs the Vercel preview link out of PR comments when the status
// check didn't carry one (bot-comment style integration).
func PreviewURL(repoDir string, number int) string {
	out, err := gh(repoDir, "pr", "view", fmt.Sprint(number), "--json", "comments")
	if err != nil {
		return ""
	}
	if m := vercelURLRe.Find(out); m != nil {
		return string(m)
	}
	return ""
}

// Merged reports whether the branch's PR is merged — the only safe merge
// check under squash-merge (git ancestry lies; see LEARNINGS.md).
func Merged(repoDir, branch string) (bool, *PR, error) {
	pr, err := PRForBranch(repoDir, branch)
	if err != nil {
		return false, nil, err
	}
	if pr == nil {
		return false, nil, nil
	}
	return pr.State == "MERGED", pr, nil
}

// FetchAll fans PR lookups out concurrently (ls refresh path).
func FetchAll(lookups map[string][2]string) map[string]*PR {
	type res struct {
		key string
		pr  *PR
	}
	ch := make(chan res, len(lookups))
	for key, rb := range lookups {
		go func(key, repoDir, branch string) {
			pr, _ := PRForBranch(repoDir, branch)
			ch <- res{key, pr}
		}(key, rb[0], rb[1])
	}
	out := map[string]*PR{}
	timeout := time.After(6 * time.Second)
	for range lookups {
		select {
		case r := <-ch:
			if r.pr != nil {
				out[r.key] = r.pr
			}
		case <-timeout:
			return out
		}
	}
	return out
}

// OpenPRBody returns number, url, and body of the open PR whose head is
// branch (drafts included); number 0 when none. `gv handoff` reads it to
// check the worker wrote its handoff before the task leaves this host.
func OpenPRBody(repoDir, branch string) (number int, url, body string, err error) {
	out, err := gh(repoDir, "pr", "list", "--head", branch, "--state", "open", "--limit", "1",
		"--json", "number,url,body")
	if err != nil {
		return 0, "", "", err
	}
	var prs []struct {
		Number int    `json:"number"`
		URL    string `json:"url"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal(out, &prs); err != nil {
		return 0, "", "", err
	}
	if len(prs) == 0 {
		return 0, "", "", nil
	}
	return prs[0].Number, prs[0].URL, prs[0].Body, nil
}
