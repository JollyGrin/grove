package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// AC (grove-579): a slow backend report does not block a second stream's
// tick past the TTL. The first caller computes with the lock released;
// the second waits at most one TTL and serves the cache, and nobody
// starts a second report while the first is in flight.
func TestReportCacheSlowReportDoesNotBlockOtherStreams(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	c := &reportCache{ttl: 50 * time.Millisecond, report: func() ([]chatRecord, error) {
		calls.Add(1)
		<-release
		return nil, nil
	}}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = c.find("stream-1") // the slow one: parked until release
	}()
	// Let stream-1 take the inflight slot before stream-2 arrives.
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if calls.Load() == 0 {
		t.Fatal("stream-1 never started its report")
	}

	start := time.Now()
	_, _ = c.find("stream-2")
	took := time.Since(start)
	if took > 10*c.ttl {
		t.Errorf("stream-2 blocked %s behind stream-1's report, want about one TTL (%s)", took, c.ttl)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("%d reports in flight, want 1 — a waiter must not start its own", got)
	}

	close(release)
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Errorf("%d reports after release, want 1", got)
	}
}

// Within the TTL the second caller is a cache hit — no report at all.
func TestReportCacheHitWithinTTL(t *testing.T) {
	var calls atomic.Int32
	c := &reportCache{ttl: time.Second, report: func() ([]chatRecord, error) {
		calls.Add(1)
		return nil, nil
	}}
	_, _, _ = c.lookup("a", false)
	_, _, _ = c.lookup("b", false)
	if got := calls.Load(); got != 1 {
		t.Errorf("two lookups inside the TTL computed %d reports, want 1", got)
	}
	_, _, _ = c.lookup("c", true)
	if got := calls.Load(); got != 2 {
		t.Errorf("a forced lookup computed %d reports in all, want 2", got)
	}
}
