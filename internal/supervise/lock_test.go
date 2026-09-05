package supervise

import (
	"os"
	"testing"
)

func TestLock_SecondCallerRefused(t *testing.T) {
	dir := t.TempDir()
	unlock, err := Lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	_, err = Lock(dir)
	if err == nil {
		t.Fatal("second Lock succeeded — single-emitter guard did not hold")
	}
	lockErr, ok := err.(*LockErr)
	if !ok {
		t.Fatalf("err = %v (%T), want *LockErr", err, err)
	}
	if lockErr.PID != os.Getpid() {
		t.Errorf("LockErr.PID = %d, want %d (this process, the holder)", lockErr.PID, os.Getpid())
	}
}

func TestLock_ReleasedOnUnlock(t *testing.T) {
	dir := t.TempDir()
	unlock, err := Lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	unlock()

	unlock2, err := Lock(dir)
	if err != nil {
		t.Fatalf("Lock after Unlock: %v", err)
	}
	unlock2()
}
