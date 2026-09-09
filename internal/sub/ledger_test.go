package sub

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestLedgerAppendRead(t *testing.T) {
	dir := t.TempDir()
	r := Record{
		Time: time.Now().UTC(), Lane: "z", Model: "m", Mode: "raw",
		InputChars: 100, InputTokens: 10, OutputTokens: 5, Turns: 1,
		Millis: 250, Exit: 0, PromptHead: "hello",
	}
	if err := Append(dir, r); err != nil {
		t.Fatal(err)
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].Lane != "z" || got[0].Mode != "raw" || got[0].V != 1 {
		t.Errorf("got[0] = %+v", got[0])
	}
}

func TestLedgerConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_ = Append(dir, Record{Time: time.Now().UTC(), Lane: "z", Mode: "raw", PromptHead: "p"})
		}(i)
	}
	wg.Wait()
	got, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 10 {
		t.Fatalf("len(got) = %d, want 10 (no interleaving)", len(got))
	}
}

func TestLedgerSkipsBadLine(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, Record{Time: time.Now().UTC(), Lane: "a", Mode: "raw"}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(Path(dir), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("not json\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := Append(dir, Record{Time: time.Now().UTC(), Lane: "b", Mode: "raw"}); err != nil {
		t.Fatal(err)
	}

	got, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2 (bad line skipped)", len(got))
	}
	if got[0].Lane != "a" || got[1].Lane != "b" {
		t.Errorf("got = %+v", got)
	}
}

func TestLedgerPath(t *testing.T) {
	if Path("/tmp/x") != filepath.Join("/tmp/x", "sub.jsonl") {
		t.Errorf("Path = %q", Path("/tmp/x"))
	}
}
