package supervise

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// LockErr is returned by Lock when another process already holds the
// workspace's supervise lock.
type LockErr struct{ PID int }

func (e *LockErr) Error() string {
	return fmt.Sprintf("already supervised (pid %d)", e.PID)
}

// Lock takes a non-blocking exclusive flock on <stateDir>/supervise.lock —
// the single-emitter guard a headless `gv supervise` loop shares with part
// 4's cockpit driver, so the two can never double-emit into the same
// events.jsonl. A second caller gets *LockErr naming the pid that holds it.
// The lock dies with the process (no stale-lock cleanup needed — flock is
// released the instant the holder exits, cleanly or not); Unlock releases
// it explicitly on a clean shutdown.
func Lock(stateDir string) (unlock func(), err error) {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(stateDir, "supervise.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		pid := readLockPID(f)
		f.Close()
		return nil, &LockErr{PID: pid}
	}
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0)
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

func readLockPID(f *os.File) int {
	buf := make([]byte, 32)
	n, _ := f.ReadAt(buf, 0)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(buf[:n])))
	return pid
}
