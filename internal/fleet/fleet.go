// Package fleet folds the remote grove hosts into one view (grove-178, part
// 3 of the remote-overflow train). Read-only throughout: it runs
// `gv ls --json --no-pr` on each configured host over ssh, decodes the
// host's own envelope, and merges the rows with the local fleet plus the
// forwarding tombstones gv handoff leaves behind (state.Task.HandedOffTo).
//
// Merge rules (the spec, verbatim):
//   - local live rows get host "local"; every remote row gets host "<name>"
//   - a tombstone whose task shows up in any reachable host's result is
//     replaced by that live remote row
//   - a tombstone whose named host answered but does not list the task is
//     stale — rendered `→ grove-host?` (LiveStale)
//   - a host that fails or times out yields ONE warning line and the merge
//     carries on local-only for that host; never a non-zero exit
package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/cost"
	"github.com/JollyGrin/grove/internal/github"
	"github.com/JollyGrin/grove/internal/remote"
	"github.com/JollyGrin/grove/internal/state"
)

// Row is one `gv ls --json` row: the task plus its live/PR/cost columns.
// Host lives HERE, not on state.Task: it is merge-output display data, and
// keeping it off the persisted type means a Task reaching tasks.json can
// never round-trip a host tag by construction. Workspace (grove-191) is the
// same kind of display data: the owning registered workspace's label on the
// global layer's aggregated rows — omitted for global-layer tasks and
// inside workspaces, so pre-existing payloads are unchanged.
type Row struct {
	*state.Task
	Host      string     `json:"host,omitempty"`
	Workspace string     `json:"workspace,omitempty"`
	Live      string     `json:"live"`
	PR        *github.PR `json:"pr,omitempty"`
	// PRKnown is only present when a PR lookup was actually attempted for
	// this row: false means the lookup errored or timed out (github.FetchAll
	// dropped it into its unknown map, grove-251) — nil/absent means no
	// lookup was attempted at all (--no-pr, or no repo config for this
	// task). A caller must never read a nil PR as "no PR" without checking
	// this first.
	PRKnown *bool        `json:"pr_known,omitempty"`
	Cost    *cost.Totals `json:"cost,omitempty"`
}

// Live values for tombstone rows — additive vocabulary next to the tmux
// states ("gone", "working", …). "handed-off" is the value grove-177's
// `gv ls --json` already publishes for tombstone rows (docs/plugins.md).
const (
	LocalHost     = "local"
	LiveElsewhere = "handed-off"  // handed off; the named host has not been asked (or was unreachable)
	LiveStale     = "handed-off?" // handed off, but the named host answered without the task
	Timeout       = 5 * time.Second
)

// Result is one host's `gv ls --json --no-pr` outcome.
type Result struct {
	Host string
	Rows []Row
	Err  error
}

// Runner executes `gv ls --json --no-pr` on h and returns its stdout —
// a seam so tests never touch ssh.
type Runner func(ctx context.Context, h *config.Host) ([]byte, error)

// SSHRunner is the real Runner: the grove-176 passthrough argv, bounded by
// ctx. Only stdout is returned; ssh's own stderr is folded into the error
// so the warning line names the cause.
func SSHRunner(ctx context.Context, h *config.Host) ([]byte, error) {
	argv := remote.Argv(h, "ls", []string{"--json", "--no-pr", "--no-cost"})
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		// A run that completed keeps its rows even if it finished at the
		// deadline's edge — only a FAILED run reads the ctx to decide
		// whether the cause was the timeout.
		return stdout.Bytes(), nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("timed out after %s", Timeout)
	}
	if msg := strings.TrimSpace(stderr.String()); msg != "" {
		return nil, fmt.Errorf("%s", lastLine(msg))
	}
	return nil, err
}

func lastLine(s string) string {
	lines := strings.Split(s, "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// Fetch asks every named host in parallel (per-host timeout, never longer
// than Timeout in total) and returns results in the given host order.
func Fetch(ctx context.Context, cfg *config.Config, hosts []string, run Runner) []Result {
	if run == nil {
		run = SSHRunner
	}
	out := make([]Result, len(hosts))
	var wg sync.WaitGroup
	for i, name := range hosts {
		out[i].Host = name
		h, err := cfg.Host(name)
		if err != nil {
			out[i].Err = err
			continue
		}
		wg.Add(1)
		go func(i int, h *config.Host) {
			defer wg.Done()
			hctx, cancel := context.WithTimeout(ctx, Timeout)
			defer cancel()
			raw, err := run(hctx, h)
			if err != nil {
				out[i].Err = err
				return
			}
			out[i].Rows, out[i].Err = Decode(out[i].Host, raw)
		}(i, h)
	}
	wg.Wait()
	return out
}

// Decode parses a remote host's `gv ls --json` envelope and stamps every
// row with host. Tombstone rows the remote itself carries (a task handed
// on from there) are dropped — the view shows where a task IS, and the
// remote's own forwarding pointer is that host's business.
func Decode(host string, raw []byte) ([]Row, error) {
	var env struct {
		Tasks []Row `json:"tasks"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("bad gv ls --json output: %v", err)
	}
	rows := make([]Row, 0, len(env.Tasks))
	for _, r := range env.Tasks {
		if r.Task == nil || r.HandedOffTo != "" {
			continue
		}
		r.Host = host
		rows = append(rows, r)
	}
	return rows, nil
}

// IsRemote reports whether a merged row came from another host.
func IsRemote(r Row) bool {
	return r.Host != "" && r.Host != LocalHost
}

// Tombstone builds the "elsewhere" row for a handed-off task.
func Tombstone(t *state.Task) Row {
	return Row{Task: t, Live: LiveElsewhere}
}

// Merge folds local live rows, local tombstones and the remote results into
// one list: local live, then each reachable host's rows in host order, then
// the tombstones that no host replaced. Warnings carry one line per failed
// host. Local rows are stamped LocalHost on the ROW (never on the Task —
// the persisted type stays host-free); tombstones keep Host "" + their
// HandedOffTo pointer (the row's `live` says handed-off / handed-off?).
// Live rows dedup by ticket, local first: after a `--from` take-back the
// local worker is the fresh truth, and a stale remote answer still listing
// the ticket must not shadow it with a second row.
func Merge(local []Row, tombstones []*state.Task, results []Result) (rows []Row, warnings []string) {
	rows = make([]Row, 0, len(local)+len(tombstones))
	seen := map[string]bool{} // tickets already live on the board (local wins, then host order)
	for _, r := range local {
		if r.Task != nil && r.Host == "" {
			r.Host = LocalHost
		}
		if r.Task != nil {
			seen[r.Ticket] = true
		}
		rows = append(rows, r)
	}
	answered := map[string]bool{}
	for _, res := range results {
		if res.Err != nil {
			warnings = append(warnings, fmt.Sprintf("host %s: %v", res.Host, res.Err))
			continue
		}
		answered[res.Host] = true
		for _, r := range res.Rows {
			if r.Task == nil || seen[r.Ticket] {
				continue
			}
			seen[r.Ticket] = true
			rows = append(rows, r)
		}
	}
	for _, t := range tombstones {
		if seen[t.Ticket] {
			continue // replaced by a live row (remote, or re-adopted locally)
		}
		row := Tombstone(t)
		if answered[t.HandedOffTo] {
			row.Live = LiveStale
		}
		rows = append(rows, row)
	}
	return rows, warnings
}

// Elsewhere renders the tombstone line in grove-177's ls format — the
// take-it-back hint must be the handoff mirror, not a plain adopt (`gv
// adopt` here would start a second live worker on the branch the remote
// still runs). A `?` on the arrow host marks a stale tombstone (the named
// host answered without the task); the hint commands keep the clean host
// name. ago is the caller's age formatter so gv ls and the cockpit print
// the same unit; Updated is the handoff time (the fold's event stamp).
func Elsewhere(r Row, ago func(time.Time) string) string {
	host := r.HandedOffTo
	if host == "" {
		return r.Ticket
	}
	disp := host
	if r.Live == LiveStale {
		disp += "?"
	}
	return fmt.Sprintf("%-11s %-11s → %s (handed off %s; take back: gv handoff %s --from %s · drop row: gv untrack %s)",
		r.Ticket, r.Repo, disp, ago(r.Updated), r.Ticket, host, r.Ticket)
}
