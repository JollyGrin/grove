// gv feature land (feature trains Decision 6): a plan builder over the
// same tracked-task view Statuses (grove-375) already folds, plus a
// runner that finishes every merged car. Neither function ever mutates a
// task backend — closing an issue is the orchestrator's act, on the
// operator's order (Decision 8); BuildLandPlan only reads, and Land calls
// nothing but the finisher it is handed.
package feature

import (
	"errors"
	"fmt"
	"sort"

	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/state"
)

// Skip reasons for a feature's other cars (docs/plugins.md).
const (
	SkipWorking = "working" // no PR yet, and the car isn't signaling done
	SkipPROpen  = "PR open" // a PR exists but isn't MERGED
	SkipNoPR    = "no PR"   // the car looks finished but no PR was found
	SkipQueued  = "queued"  // an open backend issue, not yet grabbed
)

// LandRow is one ticket `gv feature land` will finish.
type LandRow struct {
	Ticket string `json:"ticket"`
	Number int    `json:"number,omitempty"`
	PR     int    `json:"pr"`
}

// SkipRow is one other car of the feature, left alone, and why.
type SkipRow struct {
	Ticket string `json:"ticket"`
	Number int    `json:"number,omitempty"`
	Reason string `json:"reason"`
}

// LandPlan is what `gv feature land` proposes: every car whose PR is
// actually MERGED lands; every other car is named with why it doesn't.
type LandPlan struct {
	Land    []LandRow `json:"land"`
	Skipped []SkipRow `json:"skipped"`
}

// PRLookup returns the PR for a task's branch — nil, nil when none exists
// yet. It is the same merge check finishTask itself uses (`gh pr list`
// under the hood, never git ancestry — squash-merges break ancestry).
type PRLookup func(t *state.Task) (*github.PR, error)

// LandInput is everything BuildLandPlan reads — table-testable, no
// network of its own (PR is the caller's one network call per ticket).
type LandInput struct {
	Tasks map[string]*state.Task
	Slug  string
	PR    PRLookup
}

// BuildLandPlan walks every tracked, not-done task on Slug and classifies
// it: a MERGED PR lands; an OPEN (or otherwise unmerged) PR is skipped
// "PR open"; no PR at all is skipped "working" (the ordinary in-progress
// case) unless the car is already signaling done (CarReady) with nothing
// to show for it, which is the odder "no PR". A task on another feature,
// or already landed (done), never appears. A PR-lookup error is
// collected and joined, never fatal — the ticket is simply left out of
// both lists that round.
func BuildLandPlan(in LandInput) (LandPlan, error) {
	plan := LandPlan{Land: []LandRow{}, Skipped: []SkipRow{}}
	var errs []error

	tickets := make([]string, 0, len(in.Tasks))
	for ticket, t := range in.Tasks {
		if t.Feature != in.Slug || t.Done {
			continue
		}
		tickets = append(tickets, ticket)
	}
	sort.Strings(tickets)

	for _, ticket := range tickets {
		t := in.Tasks[ticket]
		num := issueNumber(ticket)
		pr, err := in.PR(t)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: PR lookup: %w", ticket, err))
			continue
		}
		switch {
		case pr != nil && pr.State == "MERGED":
			plan.Land = append(plan.Land, LandRow{Ticket: ticket, Number: num, PR: pr.Number})
		case pr != nil:
			plan.Skipped = append(plan.Skipped, SkipRow{Ticket: ticket, Number: num, Reason: SkipPROpen})
		case carState(t) == CarReady:
			plan.Skipped = append(plan.Skipped, SkipRow{Ticket: ticket, Number: num, Reason: SkipNoPR})
		default:
			plan.Skipped = append(plan.Skipped, SkipRow{Ticket: ticket, Number: num, Reason: SkipWorking})
		}
	}
	return plan, errors.Join(errs...)
}

// Finisher runs whatever `gv done` runs to close out one landed ticket
// (finishTask) — never a backend mutation.
type Finisher func(ticket string) error

// LandFailure is one row Land could not finish.
type LandFailure struct {
	Ticket string `json:"ticket"`
	Error  string `json:"error"`
}

// LandResult is what running a plan actually did.
type LandResult struct {
	Landed []int         `json:"landed"` // issue numbers actually finished, in plan order
	Failed []LandFailure `json:"failed"`
}

// Land runs finish over every plan.Land row. A failing row is recorded
// and the rest still run — one bad worktree must never strand the rest
// of the train.
func Land(plan LandPlan, finish Finisher) LandResult {
	res := LandResult{Landed: []int{}, Failed: []LandFailure{}}
	for _, row := range plan.Land {
		if err := finish(row.Ticket); err != nil {
			res.Failed = append(res.Failed, LandFailure{Ticket: row.Ticket, Error: err.Error()})
			continue
		}
		res.Landed = append(res.Landed, row.Number)
	}
	return res
}
