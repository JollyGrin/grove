package github

// grove-507: the cockpit's PR beat is GraphQL (`gh pr list`), shared by
// every open `gv dash` on the machine, so the beat watches the hourly
// GraphQL budget and stretches itself as it drains. The read goes through
// `gh api rate_limit` — REST, which does not count against the GraphQL
// quota it reports on.

import (
	"encoding/json"
	"sync"
	"time"
)

// Quota is the GraphQL slice of `gh api rate_limit`.
type Quota struct {
	Remaining int
	Limit     int
	Reset     time.Time
}

// quotaTTL caches the rate-limit read so it is not its own poll: the beat
// is at most every 30s and several cockpits share the REST budget too.
const quotaTTL = time.Minute

var (
	quotaMu   sync.Mutex
	quotaAt   time.Time
	quotaLast Quota
	quotaOK   bool
	// readQuotaRaw is the gh call; a var so tests can stub it.
	readQuotaRaw = func() ([]byte, error) { return gh("", "api", "rate_limit") }
)

// ReadQuota returns the GraphQL quota, cached for quotaTTL. ok is false
// when gh could not say (offline, old gh, a stub) — callers treat that as
// "no information", i.e. poll at the normal cadence, never as "empty".
func ReadQuota() (q Quota, ok bool) {
	quotaMu.Lock()
	defer quotaMu.Unlock()
	if !quotaAt.IsZero() && time.Since(quotaAt) < quotaTTL {
		return quotaLast, quotaOK
	}
	quotaAt = time.Now()
	quotaLast, quotaOK = Quota{}, false
	out, err := readQuotaRaw()
	if err != nil {
		return quotaLast, false
	}
	var resp struct {
		Resources struct {
			GraphQL struct {
				Limit     int   `json:"limit"`
				Remaining int   `json:"remaining"`
				Reset     int64 `json:"reset"`
			} `json:"graphql"`
		} `json:"resources"`
	}
	if json.Unmarshal(out, &resp) != nil || resp.Resources.GraphQL.Limit <= 0 {
		return quotaLast, false
	}
	g := resp.Resources.GraphQL
	quotaLast = Quota{Remaining: g.Remaining, Limit: g.Limit, Reset: time.Unix(g.Reset, 0)}
	quotaOK = true
	return quotaLast, true
}

// Beat intervals by remaining-quota tier.
const (
	BeatNormal = 30 * time.Second
	beatMid    = time.Minute
	beatLow    = 2 * time.Minute
	beatCrit   = 5 * time.Minute
)

// BeatPlan maps the remaining quota to the next PR-beat interval. skip
// means don't poll this beat at all (below 5%); low means the cockpit
// should say so (below 25%). An unreadable quota (ok false) plans a
// normal beat.
func BeatPlan(q Quota, ok bool) (interval time.Duration, skip, low bool) {
	if !ok || q.Limit <= 0 {
		return BeatNormal, false, false
	}
	pct := q.Remaining * 100 / q.Limit
	switch {
	case pct < 5:
		return beatCrit, true, true
	case pct < 10:
		return beatCrit, false, true
	case pct < 25:
		return beatLow, false, true
	case pct < 50:
		return beatMid, false, false
	}
	return BeatNormal, false, false
}
