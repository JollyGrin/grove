package github

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestBeatPlanTiers(t *testing.T) {
	cases := []struct {
		remaining int
		interval  time.Duration
		skip, low bool
	}{
		{5000, 30 * time.Second, false, false},
		{2501, 30 * time.Second, false, false},
		{2000, time.Minute, false, false},
		{1000, 2 * time.Minute, false, true},
		{400, 5 * time.Minute, false, true},
		{249, 5 * time.Minute, true, true},
		{0, 5 * time.Minute, true, true},
	}
	for _, c := range cases {
		iv, skip, low := BeatPlan(Quota{Remaining: c.remaining, Limit: 5000}, true)
		if iv != c.interval || skip != c.skip || low != c.low {
			t.Errorf("remaining %d: got (%v,%v,%v), want (%v,%v,%v)", c.remaining, iv, skip, low, c.interval, c.skip, c.low)
		}
	}
	// No information is never "empty": a normal beat.
	if iv, skip, low := BeatPlan(Quota{}, false); iv != BeatNormal || skip || low {
		t.Errorf("unreadable quota planned (%v,%v,%v), want a normal beat", iv, skip, low)
	}
}

func TestReadQuotaParsesAndCaches(t *testing.T) {
	calls := 0
	old := readQuotaRaw
	readQuotaRaw = func() ([]byte, error) {
		calls++
		return []byte(`{"resources":{"graphql":{"limit":5000,"remaining":1234,"reset":1760000000}}}`), nil
	}
	quotaAt = time.Time{}
	t.Cleanup(func() { readQuotaRaw = old; quotaAt = time.Time{} })
	for i := 0; i < 3; i++ {
		q, ok := ReadQuota()
		if !ok || q.Remaining != 1234 || q.Limit != 5000 {
			t.Fatalf("ReadQuota = %+v, %v", q, ok)
		}
	}
	if calls != 1 {
		t.Errorf("rate_limit read %d times in the TTL, want 1", calls)
	}
}

func TestReadQuotaErrorIsNoInformation(t *testing.T) {
	old := readQuotaRaw
	readQuotaRaw = func() ([]byte, error) { return nil, errors.New("offline") }
	quotaAt = time.Time{}
	t.Cleanup(func() { readQuotaRaw = old; quotaAt = time.Time{} })
	if _, ok := ReadQuota(); ok {
		t.Error("failed read reported ok")
	}
}

type fakeFetch struct {
	calls int
	prs   map[string]*PR
	errs  map[string]error
}

func (f *fakeFetch) fetch(l map[string][2]string) (map[string]*PR, map[string]error) {
	f.calls++
	prs, errs := map[string]*PR{}, map[string]error{}
	for k := range l {
		if e := f.errs[k]; e != nil {
			errs[k] = e
		} else if p := f.prs[k]; p != nil {
			prs[k] = p
		}
	}
	return prs, errs
}

var oneLookup = map[string][2]string{"T-1": {"/repo", "b1"}}

func TestCacheServesWithinTTLAndRefetchesAfter(t *testing.T) {
	dir := t.TempDir()
	f := &fakeFetch{prs: map[string]*PR{"T-1": {Number: 7, State: "OPEN"}}}
	for i := 0; i < 3; i++ {
		prs, unk := CachedFetchAll(dir, oneLookup, time.Hour, false, f.fetch)
		if prs["T-1"] == nil || prs["T-1"].Number != 7 || len(unk) != 0 {
			t.Fatalf("pass %d: prs=%v unknown=%v", i, prs, unk)
		}
	}
	if f.calls != 1 {
		t.Errorf("fetched %d times within the TTL, want 1", f.calls)
	}
	CachedFetchAll(dir, oneLookup, 0, false, f.fetch) // TTL elapsed
	if f.calls != 2 {
		t.Errorf("expired entry did not refetch (calls=%d)", f.calls)
	}
	CachedFetchAll(dir, oneLookup, time.Hour, true, f.fetch) // forced
	if f.calls != 3 {
		t.Errorf("force did not bypass a fresh entry (calls=%d)", f.calls)
	}
}

// "No PR" is cached like a PR (it is a successful answer)…
func TestCacheRemembersNoPR(t *testing.T) {
	dir := t.TempDir()
	f := &fakeFetch{}
	for i := 0; i < 2; i++ {
		prs, unk := CachedFetchAll(dir, oneLookup, time.Hour, false, f.fetch)
		if len(prs) != 0 || len(unk) != 0 {
			t.Fatalf("no-PR lookup returned prs=%v unknown=%v", prs, unk)
		}
	}
	if f.calls != 1 {
		t.Errorf("no-PR answer refetched (calls=%d)", f.calls)
	}
}

// …but a FAILED lookup is never cached and never turns into "no PR", and
// it leaves the previous good entry intact (stale-on-error).
func TestCacheFailedLookupStaysUnknownAndKeepsOldEntry(t *testing.T) {
	dir := t.TempDir()
	f := &fakeFetch{prs: map[string]*PR{"T-1": {Number: 7, State: "OPEN"}}}
	CachedFetchAll(dir, oneLookup, time.Hour, false, f.fetch)

	f.errs = map[string]error{"T-1": errors.New("gh down")}
	prs, unk := CachedFetchAll(dir, oneLookup, 0, false, f.fetch)
	if unk["T-1"] == nil || prs["T-1"] != nil {
		t.Fatalf("failed lookup: prs=%v unknown=%v, want unknown only", prs, unk)
	}
	// The old entry survived on disk: a lock-timeout read sees it.
	c := readCache(filepath.Join(dir, "pr-cache.json"))
	if e, ok := c[cacheKey("/repo", "b1")]; !ok || e.PR == nil || e.PR.Number != 7 {
		t.Errorf("failed lookup clobbered the cached entry: %+v", e)
	}

	// A brand-new key that fails is never written as "no PR".
	other := map[string][2]string{"T-2": {"/repo", "b2"}}
	f.errs = map[string]error{"T-2": errors.New("gh down")}
	CachedFetchAll(dir, other, 0, false, f.fetch)
	c = readCache(filepath.Join(dir, "pr-cache.json"))
	if _, ok := c[cacheKey("/repo", "b2")]; ok {
		t.Error("failed lookup was cached")
	}
}

func TestCacheLockContentionServesStale(t *testing.T) {
	dir := t.TempDir()
	f := &fakeFetch{prs: map[string]*PR{"T-1": {Number: 7, State: "OPEN"}}}
	CachedFetchAll(dir, oneLookup, time.Hour, false, f.fetch)

	// Another cockpit holds the flock (mid-refresh).
	held, err := os.OpenFile(filepath.Join(dir, "pr-cache.json.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	old := lockWait
	lockWait = 100 * time.Millisecond
	t.Cleanup(func() { lockWait = old })

	before := f.calls
	// Expired entry + contended lock → stale PR served, no gh call.
	prs, unk := CachedFetchAll(dir, oneLookup, 0, false, f.fetch)
	if prs["T-1"] == nil || len(unk) != 0 || f.calls != before {
		t.Errorf("contended: prs=%v unknown=%v calls=%d→%d", prs, unk, before, f.calls)
	}
	// A key the cache has never seen is unknown, not "no PR".
	_, unk = CachedFetchAll(dir, map[string][2]string{"T-9": {"/repo", "zz"}}, 0, false, f.fetch)
	if unk["T-9"] == nil {
		t.Error("uncached key under contention must be unknown")
	}
}
