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
	"sort"
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

	// grove-251: PR facts the supervisor's transition engine needs to
	// decide pr_ready / pr_ci_failed / pr_conflicting without a second
	// `gh` round-trip.
	Draft      bool     `json:"draft"`
	Mergeable  string   `json:"mergeable"`         // gh's MERGEABLE | CONFLICTING | UNKNOWN
	MergeState string   `json:"merge_state"`       // gh's mergeStateStatus, passed through verbatim
	Failing    []string `json:"failing,omitempty"` // names of failing checks, sorted
	Checks     int      `json:"checks"`            // total rollup entries
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
		"--json", "number,url,state,mergedAt,isDraft,mergeable,mergeStateStatus,statusCheckRollup,comments")
	if err != nil {
		return nil, err
	}
	var prs []struct {
		Number     int    `json:"number"`
		URL        string `json:"url"`
		State      string `json:"state"`
		MergedAt   string `json:"mergedAt"`
		IsDraft    bool   `json:"isDraft"`
		Mergeable  string `json:"mergeable"`
		MergeState string `json:"mergeStateStatus"`
		Comments   []struct {
			Body string `json:"body"`
		} `json:"comments"`
		StatusCheckRollup []struct {
			Name       string `json:"name"`    // CheckRun
			Context    string `json:"context"` // StatusContext
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
	pr := &PR{
		Number:     p.Number,
		URL:        p.URL,
		State:      p.State,
		MergedAt:   p.MergedAt,
		CI:         "none",
		Draft:      p.IsDraft,
		Mergeable:  p.Mergeable,
		MergeState: p.MergeState,
		Checks:     len(p.StatusCheckRollup),
	}

	var pass, fail, pending int
	var failing []string
	for _, c := range p.StatusCheckRollup {
		// CheckRun: status/conclusion. StatusContext: state.
		switch {
		case c.Conclusion == "SUCCESS" || c.State == "SUCCESS":
			pass++
		case c.Conclusion == "FAILURE" || c.Conclusion == "ERROR" || c.Conclusion == "TIMED_OUT" ||
			c.Conclusion == "CANCELLED" || c.Conclusion == "ACTION_REQUIRED" ||
			c.State == "FAILURE" || c.State == "ERROR":
			fail++
			name := c.Name
			if name == "" {
				name = c.Context
			}
			if name != "" {
				failing = append(failing, name)
			}
		case c.Status == "IN_PROGRESS" || c.Status == "QUEUED" || c.State == "PENDING":
			pending++
		}
		if c.TargetURL != "" && strings.Contains(c.TargetURL, ".vercel.app") {
			pr.PreviewURL = c.TargetURL
		}
	}
	sort.Strings(failing)
	pr.Failing = failing
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

// FetchAll fans PR lookups out concurrently (ls refresh path). unknown
// carries a key whenever its lookup errored or never returned before the
// timeout — the transition engine (part 2) must never emit a transition
// from a failed lookup, so "lookup failed" has to stay distinguishable
// from "no PR" (a key in neither map). A key present in prs is never also
// in unknown.
func FetchAll(lookups map[string][2]string) (prs map[string]*PR, unknown map[string]error) {
	type res struct {
		key string
		pr  *PR
		err error
	}
	ch := make(chan res, len(lookups))
	for key, rb := range lookups {
		go func(key, repoDir, branch string) {
			pr, err := PRForBranch(repoDir, branch)
			ch <- res{key, pr, err}
		}(key, rb[0], rb[1])
	}
	prs = map[string]*PR{}
	unknown = map[string]error{}
	timeout := time.After(6 * time.Second)
	pending := map[string]bool{}
	for key := range lookups {
		pending[key] = true
	}
	for range lookups {
		select {
		case r := <-ch:
			delete(pending, r.key)
			switch {
			case r.err != nil:
				unknown[r.key] = r.err
			case r.pr != nil:
				prs[r.key] = r.pr
			}
		case <-timeout:
			for key := range pending {
				unknown[key] = fmt.Errorf("timed out")
			}
			return prs, unknown
		}
	}
	return prs, unknown
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
