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
// The host tag rides on Task.Host (json "host").
type Row struct {
	*state.Task
	Live string       `json:"live"`
	PR   *github.PR   `json:"pr,omitempty"`
	Cost *cost.Totals `json:"cost,omitempty"`
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
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("timed out after %s", Timeout)
	}
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%s", lastLine(msg))
		}
		return nil, err
	}
	return stdout.Bytes(), nil
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
func IsRemote(t *state.Task) bool {
	return t != nil && t.Host != "" && t.Host != LocalHost
}

// Tombstone builds the "elsewhere" row for a handed-off task.
func Tombstone(t *state.Task) Row {
	return Row{Task: t, Live: LiveElsewhere}
}

// Merge folds local live rows, local tombstones and the remote results into
// one list: local live, then each reachable host's rows in host order, then
// the tombstones that no host replaced. Warnings carry one line per failed
// host. Local rows are stamped LocalHost; tombstones keep Host "" + their
// HandedOffTo pointer (the row's `live` says handed-off / handed-off?).
func Merge(local []Row, tombstones []*state.Task, results []Result) (rows []Row, warnings []string) {
	rows = make([]Row, 0, len(local)+len(tombstones))
	for _, r := range local {
		if r.Task != nil && r.Host == "" {
			// Stamp a copy: the caller's tasks (the cockpit's folded map,
			// which is what tasks.json is written from) must not pick up
			// the output-only host tag.
			cp := *r.Task
			cp.Host = LocalHost
			r.Task = &cp
		}
		rows = append(rows, r)
	}
	seen := map[string]bool{} // ticket live on some reachable host
	answered := map[string]bool{}
	for _, res := range results {
		if res.Err != nil {
			warnings = append(warnings, fmt.Sprintf("host %s: %v", res.Host, res.Err))
			continue
		}
		answered[res.Host] = true
		for _, r := range res.Rows {
			seen[r.Ticket] = true
			rows = append(rows, r)
		}
	}
	for _, t := range tombstones {
		if seen[t.Ticket] {
			continue // replaced by the live remote row
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
