package chat

// grove-222: a chat's Claude session id is known BY CONSTRUCTION, never
// inferred from transcript recency.
//
// grove-215 paired a live pane with a transcript by mtime order — newest
// pane takes the newest `.jsonl` — and shipped a resolver that stamped two
// live chats with each other's ids. The assumption is false: a transcript's
// mtime is its LAST WRITE, so an older pane that is still working outranks
// a younger one that went idle. Stability is not correctness, and the stamp
// is durable, so a wrong pairing is sticky: `gv chat tail` reads the wrong
// conversation and `gv chat send` pastes into the wrong agent.
//
// The fix is to stop guessing. Two sources of truth, in this order:
//
//  1. MINTED AT SPAWN — grove makes the UUID (NewSessionID), hands it to
//     `claude --session-id`, and stamps the pane before the agent has even
//     booted. Every chat grove spawns is known this way.
//  2. THE RUNNING PROCESS — the id the pane's claude was LAUNCHED on, read
//     out of its argv (PaneSessionID). Ground truth for a pane grove did
//     not spawn, and the self-heal for a pane wearing a wrong stamp from
//     before this fix.
//
// Neither can follow a conversation that is replaced inside a living pane
// (a `/clear` starts a fresh one, and nothing in the argv or the stamp
// changes with it) — `gv chat restamp` is the escape hatch for that, and
// for any pane whose identity was mis-stamped and cannot be re-derived.
//
// Note what is NOT here: the open-fd probe (`/proc/<pid>/fd`) the ticket
// floated as a fallback. Verified 2026-08-31 on a machine running four
// claude sessions — NONE of them held its transcript open; Claude Code
// appends and closes, so an fd scan finds nothing to correlate.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// NewSessionID mints the UUID grove hands `claude --session-id`. v4, which
// is what Claude Code validates the flag against ("must be a valid UUID").
func NewSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("minting a chat session id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}

// Proc is one row of `ps -Ao pid,ppid,args`: the process tree in the only
// terms this package needs. Parsed by the caller (ParseProcs) so the whole
// resolution is testable without a machine's real process table.
type Proc struct {
	PID  int
	PPID int
	Args string
}

var procLineRe = regexp.MustCompile(`^\s*(\d+)\s+(\d+)\s+(.*)$`)

// ParseProcs parses `ps -Ao pid,ppid,args` output. A header line, a blank
// line, or anything that is not two integers followed by an argv is
// skipped — ps formats differ across platforms and a surprise line must
// cost a row, never the scan.
func ParseProcs(out string) []Proc {
	var procs []Proc
	for _, line := range strings.Split(out, "\n") {
		m := procLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		pid, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		procs = append(procs, Proc{PID: pid, PPID: ppid, Args: strings.TrimSpace(m[3])})
	}
	return procs
}

// sessionFlags are the two ways a launch names its conversation:
// `--session-id <uuid>` (grove mints it) and `--resume <id>` (grove-217
// revives one). Either is the id the process is actually speaking into.
var sessionFlags = []string{"--session-id", "--resume"}

// LaunchedSessionID reads the session id out of a process's argv, "" when
// it names none.
//
// The value must pass ValidSessionID, which is what keeps prose out: a
// WORKER's argv carries its whole ticket text, and a ticket that quotes
// `claude --session-id <uuid>` must never be read as an id (`<uuid>` is not
// one). Two DIFFERENT ids in one argv is refused rather than resolved to
// the first — an argv that ambiguous is not something to guess from, and
// guessing is the bug this file exists to end.
func LaunchedSessionID(args string) string {
	found := ""
	fields := strings.Fields(args)
	for i, f := range fields {
		val := ""
		for _, flag := range sessionFlags {
			if f == flag && i+1 < len(fields) {
				val = fields[i+1]
			} else if v, ok := strings.CutPrefix(f, flag+"="); ok {
				val = v
			}
		}
		if val == "" || !ValidSessionID(val) {
			continue
		}
		if found != "" && found != val {
			return ""
		}
		found = val
	}
	return found
}

// PaneSessionID answers "which conversation is running in this pane?" from
// the process table: the id the pane's agent was launched on, found by
// walking the pane process's descendants (the pane pid is a SHELL — the
// launch is typed into it, so claude is a child, not the pane process).
//
// "" means "nothing to say", never a guess: no descendant names an id, or
// two of them name different ones (a pane running two agents is not a
// question this can answer).
func PaneSessionID(procs []Proc, panePID int) string {
	if panePID <= 0 || len(procs) == 0 {
		return ""
	}
	children := map[int][]Proc{}
	self := map[int]Proc{}
	for _, p := range procs {
		children[p.PPID] = append(children[p.PPID], p)
		self[p.PID] = p
	}
	found := ""
	seen := map[int]bool{panePID: true}
	queue := []int{panePID}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if p, ok := self[pid]; ok {
			if id := LaunchedSessionID(p.Args); id != "" {
				if found != "" && found != id {
					return ""
				}
				found = id
			}
		}
		for _, c := range children[pid] {
			// A process whose ppid is its own pid (or a cycle from a
			// reused pid between ps rows) must not spin the walk.
			if seen[c.PID] {
				continue
			}
			seen[c.PID] = true
			queue = append(queue, c.PID)
		}
	}
	return found
}
