package feature

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JollyGrin/grove/internal/fleet"
	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/ledger"
	"github.com/JollyGrin/grove/internal/provider"
	"github.com/JollyGrin/grove/internal/serve"
	"github.com/JollyGrin/grove/internal/state"
)

// Car states (feature trains Decision 4). ready = the worker reported done
// or its PR is ready; landed = on a GitHub provider, a closed labelled
// issue whose ticket branch has a PR merged into the feature branch
// (grove-397); otherwise (and in union) task_done after a task_created on
// this feature.
const (
	CarQueued   = "queued"
	CarWorking  = "working"
	CarQuestion = "question"
	CarReady    = "ready"
	CarLanded   = "landed"
)

// Car is one ticket on a feature train (docs/plugins.md).
type Car struct {
	Ticket   string     `json:"ticket"`
	Number   int        `json:"number,omitempty"`
	Title    string     `json:"title"`
	State    string     `json:"state"`
	PR       int        `json:"pr,omitempty"`
	LandedAt *time.Time `json:"landed_at,omitempty"`
	After    []int      `json:"after,omitempty"`
	EstUSD   float64    `json:"est_usd"`
	// Host is the grove host running the car (grove-398): set only on a
	// car tracked by another host's `gv ls --json`, absent for local ones.
	Host string `json:"host,omitempty"`

	created time.Time // active-car order key
}

// FeaturePR is the feature branch → base PR, if one is open or was.
type FeaturePR struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	State  string `json:"state"` // OPEN | MERGED | CLOSED
}

// Status is one open feature's computed state — shared by `gv feature ls`
// and the cockpit (ticket 06). BehindBase and Mergeable are absent when
// the local refs cannot answer (never fetched, no repo dir).
type Status struct {
	Cars       []Car      `json:"cars"`
	Landed     int        `json:"landed"`
	Total      int        `json:"total"`
	BehindBase *int       `json:"behind_base,omitempty"`
	Mergeable  *bool      `json:"mergeable,omitempty"`
	PR         *FeaturePR `json:"pr,omitempty"`
	EstUSD     float64    `json:"est_usd"`
	// Serve is the feature's local serve (grove-380, Decision 7), filled
	// by the caller after Statuses — it needs tmux and the run.sh hash.
	Serve *serve.Status `json:"serve,omitempty"`
}

// StatusInput is everything Statuses reads, injected so it is
// table-testable without network. Nil funcs skip their field.
type StatusInput struct {
	Features map[string]*state.Feature
	Tasks    map[string]*state.Task // the folded view
	Events   []state.Event          // oldest first; landed cars come from here
	Ledger   []ledger.Row

	// Issues lists a feature's open backend issues — the one network call.
	// Results are filtered to the feature's label here, so a provider
	// without a label query may return its whole backlog.
	Issues func(f *state.Feature) ([]*provider.Task, error)
	// SkipQueued turns the Issues lookup off (the cockpit runs it only
	// where PR refresh already runs).
	SkipQueued bool

	// RepoDir maps a feature's repo name to a local checkout; Git runs
	// git there. Both local only — nothing here fetches.
	RepoDir func(repo string) string
	Git     func(dir string, args ...string) (string, error)

	// PR looks up the PR whose head is the feature branch.
	PR func(f *state.Feature) (*github.PR, error)

	// ClosedIssues and MergedPRs are the GitHub landed lookup (grove-397):
	// the feature's closed labelled issues, and the PRs merged into its
	// branch. They run with Issues (never under SkipQueued); nil or a
	// failure falls back to landed-from-events alone.
	ClosedIssues func(f *state.Feature) ([]*provider.Task, error)
	MergedPRs    func(f *state.Feature) ([]github.MergedPR, error)

	// Remote asks every configured host for its tracked tasks (grove-398)
	// — once per Statuses call, never under SkipQueued. A failed host
	// comes back as a Result.Err and becomes a warning; its cars fall
	// back to queued.
	Remote func() []fleet.Result
}

// Statuses computes Status for every open feature, keyed by slug. Lookup
// failures leave their field empty and come back joined in err, which is
// never fatal: the map is always complete.
func Statuses(in StatusInput) (map[string]*Status, error) {
	usd := map[string]float64{}
	for t, r := range ledger.Latest(in.Ledger) {
		usd[t] = r.USD
	}
	landed := landedCars(in.Events)

	out := map[string]*Status{}
	var errs []error
	var remote []fleet.Row
	if !in.SkipQueued && in.Remote != nil && len(in.Features) > 0 {
		for _, res := range in.Remote() {
			if res.Err != nil {
				errs = append(errs, fmt.Errorf("host %s: %w", res.Host, res.Err))
				continue
			}
			remote = append(remote, res.Rows...)
		}
	}
	for slug, f := range in.Features {
		if f.Closed {
			continue
		}
		st := &Status{Cars: []Car{}}
		seen := map[string]bool{}

		cars := landed[slug]
		if !in.SkipQueued && in.ClosedIssues != nil && in.MergedPRs != nil {
			gh, err := githubLanded(in, f)
			if err != nil {
				errs = append(errs, fmt.Errorf("feature %s: landed lookup: %w", slug, err))
			} else {
				cars = unionLanded(cars, gh)
			}
		}
		for _, c := range cars {
			if c.PR == 0 {
				c.PR = prOf(in.Tasks[c.Ticket])
			}
			st.Cars = append(st.Cars, c)
			seen[c.Ticket] = true
		}
		var active []Car
		for _, t := range in.Tasks {
			if t.Feature != slug || t.Done || seen[t.Ticket] {
				continue
			}
			active = append(active, Car{Ticket: t.Ticket, Number: issueNumber(t.Ticket), Title: t.Title,
				State: carState(t), PR: prOf(t), created: t.Created})
			seen[t.Ticket] = true
		}
		// A car another host tracks (grove-398): its real state, tagged
		// with the host. Local wins a ticket both know — the local fold
		// is the fresh truth after a take-back — and hosts dedup in order.
		for _, r := range remote {
			if r.Task == nil || r.Feature != slug || r.Done || seen[r.Ticket] || tracked(in.Tasks[r.Ticket]) {
				continue
			}
			active = append(active, Car{Ticket: r.Ticket, Number: issueNumber(r.Ticket), Title: r.Title,
				State: carState(r.Task), PR: prOf(r.Task), Host: r.Host, created: r.Created})
			seen[r.Ticket] = true
		}
		sort.Slice(active, func(i, j int) bool {
			if !active[i].created.Equal(active[j].created) {
				return active[i].created.Before(active[j].created)
			}
			return active[i].Ticket < active[j].Ticket
		})
		st.Cars = append(st.Cars, active...)

		if !in.SkipQueued && in.Issues != nil {
			issues, err := in.Issues(f)
			if err != nil {
				errs = append(errs, fmt.Errorf("feature %s: queued lookup: %w", slug, err))
			}
			var queued []Car
			for _, is := range issues {
				if seen[is.ID] || tracked(in.Tasks[is.ID]) || !hasLabel(is.Labels, f.Label) {
					continue
				}
				seen[is.ID] = true
				queued = append(queued, Car{Ticket: is.ID, Number: issueNumber(is.ID), Title: is.Title,
					State: CarQueued, After: DependsOn(is.Description)})
			}
			sort.Slice(queued, func(i, j int) bool {
				if queued[i].Number != queued[j].Number {
					return queued[i].Number < queued[j].Number
				}
				return queued[i].Ticket < queued[j].Ticket
			})
			st.Cars = append(st.Cars, queued...)
		}

		for i := range st.Cars {
			st.Cars[i].EstUSD = usd[st.Cars[i].Ticket]
			st.EstUSD += st.Cars[i].EstUSD
			if st.Cars[i].State == CarLanded {
				st.Landed++
			}
		}
		st.Total = len(st.Cars)

		if in.Git != nil && in.RepoDir != nil {
			if dir := in.RepoDir(f.Repo); dir != "" {
				// merge-tree also exits 1 on a bad ref, so it only runs
				// once rev-list proved both refs resolve.
				if st.BehindBase = BehindBase(in.Git, dir, f.Branch, f.Base); st.BehindBase != nil {
					st.Mergeable = Mergeable(in.Git, dir, f.Branch, f.Base)
				}
			}
		}
		if in.PR != nil {
			pr, err := in.PR(f)
			if err != nil {
				errs = append(errs, fmt.Errorf("feature %s: PR lookup: %w", slug, err))
			} else if pr != nil {
				st.PR = &FeaturePR{Number: pr.Number, URL: pr.URL, State: pr.State}
			}
		}
		out[slug] = st
	}
	return out, errors.Join(errs...)
}

// landedCars replays the log: a ticket belongs to the feature named by its
// latest task_created/task_adopted, and is landed when a task_done follows
// that membership. Untrack and sweep never erase it; a re-grab does.
// Result per slug is in landing order.
func landedCars(events []state.Event) map[string][]Car {
	type rec struct {
		feature, title string
		doneAt         time.Time
	}
	recs := map[string]*rec{}
	for _, ev := range events {
		switch ev.Type {
		case state.EvTaskCreated, state.EvTaskAdopted:
			r := recs[ev.Ticket]
			if r == nil {
				r = &rec{}
				recs[ev.Ticket] = r
			}
			if ev.Type == state.EvTaskCreated {
				r.feature, r.title = ev.Data["feature"], ev.Data["title"]
			} else if v, ok := ev.Data["feature"]; ok {
				r.feature = v
			}
			if t := ev.Data["title"]; t != "" {
				r.title = t
			}
			r.doneAt = time.Time{}
		case state.EvTaskDone:
			if r := recs[ev.Ticket]; r != nil && r.doneAt.IsZero() {
				r.doneAt = ev.Time
			}
		}
	}
	out := map[string][]Car{}
	for ticket, r := range recs {
		if r.feature == "" || r.doneAt.IsZero() {
			continue
		}
		at := r.doneAt
		out[r.feature] = append(out[r.feature], Car{Ticket: ticket, Number: issueNumber(ticket), Title: r.title,
			State: CarLanded, LandedAt: &at})
	}
	for _, cars := range out {
		sortLanded(cars)
	}
	return out
}

// githubLanded asks GitHub which of f's cars landed: a closed issue with
// the feature's label whose ticket branch (`<ticket>` or `<ticket>-…`,
// grove's branch naming) has a PR merged into f.Branch. A closed issue
// with no such PR was dropped, not landed. A ticket still tracked on this
// feature stays an active car — it still owes `gv done`; one tracked
// with no feature (grabbed before the feature was registered, grove-361)
// or on another lands here, or it would show nowhere.
func githubLanded(in StatusInput, f *state.Feature) ([]Car, error) {
	issues, err := in.ClosedIssues(f)
	if err != nil {
		return nil, err
	}
	prs, err := in.MergedPRs(f)
	if err != nil {
		return nil, err
	}
	var out []Car
	for _, is := range issues {
		if t := in.Tasks[is.ID]; (tracked(t) && t.Feature == f.Slug) || !hasLabel(is.Labels, f.Label) {
			continue
		}
		var hit *github.MergedPR
		for i := range prs {
			p := &prs[i]
			if p.HeadRefName != is.ID && !strings.HasPrefix(p.HeadRefName, is.ID+"-") {
				continue
			}
			if hit == nil || p.MergedAt.After(hit.MergedAt) {
				hit = p
			}
		}
		if hit == nil {
			continue
		}
		at := hit.MergedAt
		out = append(out, Car{Ticket: is.ID, Number: issueNumber(is.ID), Title: is.Title,
			State: CarLanded, PR: hit.Number, LandedAt: &at})
	}
	return out, nil
}

// unionLanded merges event-landed and GitHub-landed cars: GitHub wins a
// ticket both know; the result is in landing order.
func unionLanded(events, gh []Car) []Car {
	out := append([]Car(nil), gh...)
	have := map[string]bool{}
	for _, c := range gh {
		have[c.Ticket] = true
	}
	for _, c := range events {
		if !have[c.Ticket] {
			out = append(out, c)
		}
	}
	sortLanded(out)
	return out
}

func sortLanded(cars []Car) {
	sort.Slice(cars, func(i, j int) bool {
		if !cars[i].LandedAt.Equal(*cars[j].LandedAt) {
			return cars[i].LandedAt.Before(*cars[j].LandedAt)
		}
		return cars[i].Ticket < cars[j].Ticket
	})
}

// carState maps one active task's shape to a car state. A ready PR wins;
// then anything waiting on the operator; a worker mid-turn is working even
// if its last sentinel was done (it was answered and is re-working).
func carState(t *state.Task) string {
	if d := t.Delivery; d != nil && (d.State == state.DeliveryReady || d.State == state.DeliveryMerged) {
		return CarReady
	}
	if l := t.Liveness; l != nil && l.State == state.LivenessWaiting {
		return CarQuestion
	}
	switch t.Agent {
	case state.AgentWaiting, state.AgentBlocked:
		return CarQuestion
	case state.AgentWorking, state.AgentSetup:
		return CarWorking
	}
	switch t.Sentinel {
	case "question", "blocked":
		return CarQuestion
	case "done":
		return CarReady
	}
	return CarWorking
}

// tracked: an active task, on any feature or none — a ticket grabbed
// with --feature none still carries the label but is not queued here.
func tracked(t *state.Task) bool { return t != nil && !t.Done }

func prOf(t *state.Task) int {
	if t != nil && t.Delivery != nil {
		return t.Delivery.PR
	}
	return 0
}

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if strings.EqualFold(l, want) {
			return true
		}
	}
	return false
}

// issueNumber is the trailing number of a ticket id (grove-375 → 375,
// task-002 → 2); 0 when there is none.
func issueNumber(ticket string) int {
	i := len(ticket)
	for i > 0 && ticket[i-1] >= '0' && ticket[i-1] <= '9' {
		i--
	}
	n, _ := strconv.Atoi(ticket[i:])
	return n
}

var (
	dependsRe = regexp.MustCompile(`(?i)depends on\b(.*)`)
	issueRe   = regexp.MustCompile(`#(\d+)`)
)

// DependsOn reads `depends on #N` lines from an issue body — any case,
// several numbers per line and several lines — in order, deduplicated.
// Display only in v1.
func DependsOn(body string) []int {
	var out []int
	seen := map[int]bool{}
	for _, line := range strings.Split(body, "\n") {
		m := dependsRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		for _, n := range issueRe.FindAllStringSubmatch(m[1], -1) {
			v, err := strconv.Atoi(n[1])
			if err == nil && !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	return out
}

// BehindBase counts origin/<base> commits the feature branch lacks, on the
// last fetched refs. nil when either ref is missing.
func BehindBase(gitRun func(string, ...string) (string, error), dir, branch, base string) *int {
	out, err := gitRun(dir, "rev-list", "--count", "origin/"+branch+"..origin/"+base)
	if err != nil {
		return nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return nil
	}
	return &n
}

// Mergeable dry-runs merging the feature branch into its base with
// `git merge-tree --write-tree` (writes objects only, touches no ref or
// worktree): exit 0 is clean, exit 1 conflicts. nil when git cannot
// answer (a git older than 2.38). Exit 1 also means a bad ref, so callers
// check both refs resolve first (Statuses does, via BehindBase).
func Mergeable(gitRun func(string, ...string) (string, error), dir, branch, base string) *bool {
	_, err := gitRun(dir, "merge-tree", "--write-tree", "origin/"+base, "origin/"+branch)
	ok := err == nil
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 1 {
			return nil
		}
	}
	return &ok
}
