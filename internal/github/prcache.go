package github

// grove-507: every `gv dash` polled PR status on its own, so N cockpits
// cost N× the GraphQL calls. CachedFetchAll shares one on-disk cache
// (<state>/pr-cache.json) between them: whoever finds an entry older than
// the TTL refreshes it while holding the flock, the others read. No
// goroutines, no in-memory cache — the cockpit's RAM stays reserved for
// workers.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// PRCacheTTL is how long a cached lookup is served without refetching. It
// sits above the 30s beat so cockpits whose beats drift don't each
// refresh: roughly one refresh per two beats across the whole machine.
const PRCacheTTL = 40 * time.Second

// cacheMaxAge prunes entries nobody has asked for in an hour.
const cacheMaxAge = time.Hour

// lockWait bounds how long a cockpit waits on another's in-flight refresh.
// Var so tests can shrink it.
var lockWait = 10 * time.Second

type cacheEntry struct {
	PR *PR       `json:"pr"` // nil = the lookup succeeded and found no PR
	At time.Time `json:"at"`
}

func cacheKey(repoDir, branch string) string { return repoDir + "|" + branch }

func readCache(path string) map[string]cacheEntry {
	c := map[string]cacheEntry{}
	if b, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(b, &c) != nil || c == nil {
			c = map[string]cacheEntry{} // corrupt → rebuild, never fail
		}
	}
	return c
}

func writeCache(path string, c map[string]cacheEntry, now time.Time) {
	for k, e := range c {
		if now.Sub(e.At) > cacheMaxAge {
			delete(c, k)
		}
	}
	b, err := json.Marshal(c)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, path)
	}
}

// lockFile takes the exclusive flock, polling up to lockWait; nil when it
// could not (the caller then serves whatever the cache holds, read-only).
func lockFile(path string) *os.File {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil
	}
	deadline := time.Now().Add(lockWait)
	for {
		if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
			return f
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// CachedFetchAll is FetchAll behind the shared cache. Same contract: a key
// is in prs (PR found), unknown (lookup failed — never cached, never
// served as "no PR"), or neither (no PR). Fresh entries are served without
// a gh call; force skips the freshness check (an operator's explicit
// refresh) but still writes the result back. A failed lookup leaves the
// previous entry on disk untouched. If the lock can't be had in time, old
// entries of any age are served and keys without one come back unknown.
func CachedFetchAll(stateDir string, lookups map[string][2]string, ttl time.Duration, force bool,
	fetch func(map[string][2]string) (map[string]*PR, map[string]error)) (map[string]*PR, map[string]error) {
	prs := map[string]*PR{}
	unknown := map[string]error{}
	if len(lookups) == 0 {
		return prs, unknown
	}
	path := filepath.Join(stateDir, "pr-cache.json")
	lock := lockFile(path + ".lock")
	cache := readCache(path)
	now := time.Now()

	if lock == nil {
		for key, rb := range lookups {
			if e, ok := cache[cacheKey(rb[0], rb[1])]; ok {
				if e.PR != nil {
					prs[key] = e.PR
				}
			} else {
				unknown[key] = errLockTimeout
			}
		}
		return prs, unknown
	}
	defer lock.Close() // closing releases the flock

	need := map[string][2]string{}
	for key, rb := range lookups {
		e, ok := cache[cacheKey(rb[0], rb[1])]
		if ok && !force && now.Sub(e.At) < ttl {
			if e.PR != nil {
				prs[key] = e.PR
			}
			continue
		}
		need[key] = rb
	}
	if len(need) == 0 {
		return prs, unknown
	}
	got, bad := fetch(need)
	for key, rb := range need {
		switch {
		case bad[key] != nil:
			unknown[key] = bad[key]
		case got[key] != nil:
			prs[key] = got[key]
			cache[cacheKey(rb[0], rb[1])] = cacheEntry{PR: got[key], At: now}
		default:
			cache[cacheKey(rb[0], rb[1])] = cacheEntry{At: now}
		}
	}
	writeCache(path, cache, now)
	return prs, unknown
}

type lockTimeoutErr struct{}

func (lockTimeoutErr) Error() string { return "pr cache busy" }

var errLockTimeout error = lockTimeoutErr{}
