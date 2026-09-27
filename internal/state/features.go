// Feature trains (grove-372): a long-lived feature branch is runtime state,
// so it lives in events.jsonl as two workspace-scoped (empty ticket) record
// types and folds into a Features view keyed by slug. New file, same
// package: state.go's task fold stays untouched, so a stream with no
// feature events folds — and writes tasks.json — exactly as before.
package state

import (
	"bufio"
	"encoding/json"
	"os"
	"time"
)

// Feature event types (docs/plugins.md, workspace-scoped).
const (
	// EvFeatureCreated — data {slug, repo, branch, base, label}.
	EvFeatureCreated = "feature_created"
	// EvFeatureClosed — data {slug, reason}; reason is merged|abandoned.
	EvFeatureClosed = "feature_closed"
)

// feature_closed reasons.
const (
	FeatureMerged    = "merged"
	FeatureAbandoned = "abandoned"
)

// Feature is one folded feature. A slug may be re-created after it closed;
// the map then holds the newest incarnation.
type Feature struct {
	Slug         string    `json:"slug"`
	Repo         string    `json:"repo"`
	Branch       string    `json:"branch"`
	Base         string    `json:"base"`
	Label        string    `json:"label"`
	CreatedAt    time.Time `json:"created_at"`
	Closed       bool      `json:"closed"`
	ClosedReason string    `json:"closed_reason,omitempty"`
	ClosedAt     time.Time `json:"closed_at,omitzero"`
}

// foldFeature applies one event to the features view; every other type is
// a no-op. A create for a slug that is still open is ignored (first wins —
// `gv feature new` refuses it, so one only lands via a race or hand-edit);
// a close for an unknown or already-closed slug is ignored too.
func foldFeature(features map[string]*Feature, ev Event) {
	d := ev.Data
	switch ev.Type {
	case EvFeatureCreated:
		slug := d["slug"]
		if slug == "" {
			return
		}
		if f := features[slug]; f != nil && !f.Closed {
			return
		}
		features[slug] = &Feature{
			Slug: slug, Repo: d["repo"], Branch: d["branch"],
			Base: d["base"], Label: d["label"], CreatedAt: ev.Time,
		}
	case EvFeatureClosed:
		f := features[d["slug"]]
		if f == nil || f.Closed {
			return
		}
		f.Closed, f.ClosedReason, f.ClosedAt = true, d["reason"], ev.Time
	}
}

// LoadFeatures folds events.jsonl into the features view — the CLI's cold
// read, next to Load. Read-only: tasks.json is not touched. Malformed
// complete lines are skipped, as in Load.
func LoadFeatures(stateDir string) (map[string]*Feature, error) {
	features := map[string]*Feature{}
	f, err := os.Open(eventsPath(stateDir))
	if err != nil {
		if os.IsNotExist(err) {
			return features, nil
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
				foldFeature(features, ev)
			}
		}
		if readErr != nil {
			break
		}
	}
	return features, nil
}

// OpenFeature returns the open feature with this slug, or nil.
func OpenFeature(features map[string]*Feature, slug string) *Feature {
	if f := features[slug]; f != nil && !f.Closed {
		return f
	}
	return nil
}

func copyFeatures(src map[string]*Feature) map[string]*Feature {
	out := make(map[string]*Feature, len(src))
	for k, f := range src {
		c := *f
		out[k] = &c
	}
	return out
}
