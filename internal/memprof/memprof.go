// Package memprof is the heap-profile seam for grove's long-lived verbs
// (dash, chat serve, supervise, watch). It answers one question the
// 2026-10-24 memory audit could not: what does a cockpit hold after days
// up? Until it existed every memory ticket was a guess.
//
// The seam is a signal, not a listener: on SIGUSR2 the process writes a
// heap profile and a goroutine dump under <state>/profiles/ and goes
// back to what it was doing. One signal.Notify goroutine, no port, no
// poll, no cache — within the cockpit RAM rule. SIGUSR1 is taken (the
// chat rail's wake, cmd/gv/chat_rail.go), which is why this is USR2.
//
// Reading one:
//
//	kill -USR2 <pid>
//	go tool pprof -top ~/go/bin/gv <state>/profiles/<verb>-<pid>-<ts>.pprof
//	less <state>/profiles/<verb>-<pid>-<ts>.goroutines
//
// The files are operator-local diagnostics, not a plugin contract
// (docs/plugins.md): their names and shapes may change any release.
package memprof

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"syscall"
	"time"
)

// Signal is the trigger. Exported so a test can raise it on itself.
const Signal = syscall.SIGUSR2

// Dir is where profiles land under a state dir.
func Dir(stateDir string) string { return filepath.Join(stateDir, "profiles") }

// Write runs a GC so the heap profile reflects the live set rather than
// the last cycle's, then writes <dir>/<verb>-<pid>-<ts>.pprof (heap, the
// pprof binary format `go tool pprof` reads) and the same stem with a
// .goroutines suffix (the text dump `runtime/pprof` labels debug=2: one
// stack per goroutine with its state and how long it has waited — the
// shape a leak hunt needs). dir is created on demand. Returns both paths.
func Write(dir, verb string, now time.Time) (heap, goroutines string, err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	stem := filepath.Join(dir, fmt.Sprintf("%s-%d-%s", verb, os.Getpid(), now.UTC().Format("20060102T150405Z")))
	heap, goroutines = stem+".pprof", stem+".goroutines"

	runtime.GC()
	f, err := os.Create(heap)
	if err != nil {
		return "", "", err
	}
	if err := pprof.WriteHeapProfile(f); err != nil {
		f.Close()
		return "", "", err
	}
	if err := f.Close(); err != nil {
		return "", "", err
	}

	g, err := os.Create(goroutines)
	if err != nil {
		return "", "", err
	}
	if err := pprof.Lookup("goroutine").WriteTo(g, 2); err != nil {
		g.Close()
		return "", "", err
	}
	if err := g.Close(); err != nil {
		return "", "", err
	}
	return heap, goroutines, nil
}

// Install arms the seam for this process: every SIGUSR2 writes one
// profile pair into Dir(stateDir), tagged with verb. report, if non-nil,
// is called after each attempt with the paths (or the error) — the
// line-oriented verbs print it, the dash passes nil because its stderr
// is the TUI's terminal. The returned stop undoes the Notify and ends
// the goroutine; callers that live until exit may ignore it.
//
// Exactly one goroutine, parked on the channel. The channel is buffered
// by one so a signal that lands mid-write is not lost; two that land
// mid-write coalesce, which is the right answer for a diagnostic.
func Install(stateDir, verb string, report func(heap, goroutines string, err error)) (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, Signal)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ch:
				heap, goroutines, err := Write(Dir(stateDir), verb, time.Now())
				if report != nil {
					report(heap, goroutines, err)
				}
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}

// Report is the line-oriented verbs' report: one stderr line per
// profile, so an operator at `gv supervise` sees where the files went.
func Report(heap, goroutines string, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "gv: profile failed: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "gv: wrote %s and %s\n", heap, goroutines)
}
