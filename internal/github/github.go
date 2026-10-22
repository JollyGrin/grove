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
	CI         string `json:"ci"`      // pass | fail | pending | none | unknown (CI fields unreadable, grove-451)
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

// Field sets for `gh pr list --json`. grove-451: a token that cannot read
// CI (a GitHub App token gets 403 on statusCheckRollup) must not break the
// merge gate, so the gate asks only for mergeFields and the display path
// degrades to factFields when the CI fields fail.
const (
	mergeFields = "number,url,state,mergedAt"
	factFields  = mergeFields + ",isDraft,mergeable,mergeStateStatus"
	ciFields    = "statusCheckRollup,comments"
)

// ghPR is the wire shape of one `gh pr list --json` row; fields a query
// did not request decode to their zero values.
type ghPR struct {
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

// listHead runs `gh pr list --head branch` for the given field set and
// decodes the first row; nil when the branch has no PR.
func listHead(repoDir, branch, fields string) (*ghPR, error) {
	out, err := gh(repoDir, "pr", "list", "--head", branch, "--state", "all", "--limit", "1",
		"--json", fields)
	if err != nil {
		return nil, err
	}
	var prs []ghPR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, err
	}
	if len(prs) == 0 {
		return nil, nil
	}
	return &prs[0], nil
}

// PRForBranch returns the PR for a head branch, or nil if none exists.
// Comments ride along in the same call because the real *.vercel.app
// preview links live in the Vercel bot comment — status-check targetUrls
// are vercel.com build pages (field-tested on PR #936, twice).
//
// When the full query fails (grove-451: an App token gets 403 on
// statusCheckRollup) it retries once without the CI fields and returns
// the PR with CI "unknown" rather than failing the whole fetch — the
// ls/TUI CI column renders blank, the PR column still shows the number.
func PRForBranch(repoDir, branch string) (*PR, error) {
	p, err := listHead(repoDir, branch, factFields+","+ciFields)
	if err != nil {
		p2, err2 := listHead(repoDir, branch, factFields)
		if err2 != nil {
			return nil, err
		}
		if p2 == nil {
			return nil, nil
		}
		pr := fromGH(p2)
		pr.CI = "unknown"
		return pr, nil
	}
	if p == nil {
		return nil, nil
	}
	return fromGH(p), nil
}

// fromGH derives the CI verdict, failing-check names and preview link
// from a decoded gh row.
func fromGH(p *ghPR) *PR {
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
	return pr
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
// check under squash-merge (git ancestry lies; see LEARNINGS.md). It asks
// gh for the merge fields only (grove-451): the gate never looks at CI,
// so a token that cannot read statusCheckRollup must not fail it. The
// returned PR carries CI "unknown".
func Merged(repoDir, branch string) (bool, *PR, error) {
	p, err := listHead(repoDir, branch, mergeFields)
	if err != nil {
		return false, nil, err
	}
	if p == nil {
		return false, nil, nil
	}
	pr := fromGH(p)
	pr.CI = "unknown"
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

// MergedPR is one PR merged into a branch — the feature-status landed
// lookup (grove-397) reads head branches to tie each PR to its ticket.
type MergedPR struct {
	Number      int       `json:"number"`
	HeadRefName string    `json:"headRefName"`
	MergedAt    time.Time `json:"mergedAt"`
}

// MergedInto lists the PRs merged into base, newest first as gh returns
// them. One gh call.
func MergedInto(repoDir, base string) ([]MergedPR, error) {
	out, err := gh(repoDir, "pr", "list", "--state", "merged", "--base", base, "--limit", "200",
		"--json", "number,headRefName,mergedAt")
	if err != nil {
		return nil, err
	}
	var prs []MergedPR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("parse gh pr list: %w", err)
	}
	return prs, nil
}
