// Serve (grove-380, feature trains Decision 7): running a feature's tip
// locally from the workspace's reviewed .grove/run.sh. Three
// workspace-scoped (empty ticket) record types fold into a ServeLedger —
// the trusted script sha, each feature's port and its last serve. New
// file, same package: the task fold is untouched.
package state

import (
	"bufio"
	"encoding/json"
	"os"
	"strconv"
	"time"
)

// Serve event types (docs/plugins.md, workspace-scoped).
const (
	// EvFeatureServed — data {slug, port, tip, window, url}: gv serve
	// started run.sh and saw its GROVE_READY line (url is that value).
	EvFeatureServed = "feature_served"
	// EvFeatureServeStopped — data {slug}: gv serve stop killed the window.
	EvFeatureServeStopped = "feature_serve_stopped"
	// EvRunScriptTrusted — data {sha256}: the operator reviewed run.sh at
	// this hash and trusted it. Only the latest one counts.
	EvRunScriptTrusted = "run_script_trusted"
)

// Served is a feature's latest serve.
type Served struct {
	Port    int
	Tip     string
	Window  string
	URL     string
	At      time.Time
	Stopped bool // a feature_serve_stopped followed the latest feature_served
}

// ServeLedger is the folded serve view.
type ServeLedger struct {
	TrustedSHA string             // latest run_script_trusted sha256
	Served     map[string]*Served // by feature slug
}

// NewServeLedger returns an empty ledger.
func NewServeLedger() *ServeLedger { return &ServeLedger{Served: map[string]*Served{}} }

// FoldServe applies one event; every other type is a no-op. A served
// record with no slug or an unparsable port is ignored; a stop for a slug
// never served is ignored.
func (l *ServeLedger) FoldServe(ev Event) {
	d := ev.Data
	switch ev.Type {
	case EvRunScriptTrusted:
		if d["sha256"] != "" {
			l.TrustedSHA = d["sha256"]
		}
	case EvFeatureServed:
		port, err := strconv.Atoi(d["port"])
		if d["slug"] == "" || err != nil || port <= 0 {
			return
		}
		l.Served[d["slug"]] = &Served{Port: port, Tip: d["tip"], Window: d["window"], URL: d["url"], At: ev.Time}
	case EvFeatureServeStopped:
		if s := l.Served[d["slug"]]; s != nil {
			s.Stopped = true
		}
	}
}

// LoadServe folds events.jsonl into the serve ledger — the CLI's cold read.
// Read-only. Malformed complete lines are skipped, as in Load.
func LoadServe(stateDir string) (*ServeLedger, error) {
	l := NewServeLedger()
	f, err := os.Open(eventsPath(stateDir))
	if err != nil {
		if os.IsNotExist(err) {
			return l, nil
		}
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		line, readErr := r.ReadBytes('\n')
		if len(line) > 0 {
			var ev Event
			if json.Unmarshal(line, &ev) == nil {
				l.FoldServe(ev)
			}
		}
		if readErr != nil {
			break
		}
	}
	return l, nil
}
