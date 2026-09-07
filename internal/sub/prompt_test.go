package sub

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAssembleNumbersLines(t *testing.T) {
	got, err := Assemble("summarise", []Input{{Path: "foo.txt", Content: []byte("a\nb\nc")}}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	want := "summarise" +
		"\n<file path=\"foo.txt\" lines=\"3\">\n" +
		"    1\ta\n" +
		"    2\tb\n" +
		"    3\tc\n" +
		"</file>\n"
	if got != want {
		t.Errorf("Assemble() =\n%q\nwant\n%q", got, want)
	}
}

func TestAssembleTooLarge(t *testing.T) {
	_, err := Assemble("p", []Input{{Path: "f", Content: []byte("0123456789")}}, 5)
	var tooLarge ErrTooLarge
	if !errors.As(err, &tooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if tooLarge.Have != 10 || tooLarge.Max != 5 {
		t.Errorf("ErrTooLarge = %+v, want {10 5}", tooLarge)
	}
}

func TestCollectSkipsBinaryAndVendored(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel string, content []byte) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("a.go", []byte("package a\n"))
	mustWrite("node_modules/dep/index.js", []byte("module.exports = {}\n"))
	mustWrite(".git/HEAD", []byte("ref: refs/heads/main\n"))
	mustWrite("bin.dat", []byte{0x00, 0x01, 0x02})
	mustWrite("big.go", make([]byte, maxFileSize+1))
	mustWrite("readme.md", []byte("# hi\n"))
	mustWrite("unknown.xyz", []byte("nope\n"))

	inputs, err := Collect([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, in := range inputs {
		got[filepath.Base(in.Path)] = true
	}
	want := map[string]bool{"a.go": true, "readme.md": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want exactly %v", got, want)
	}
	for k := range want {
		if !got[k] {
			t.Errorf("missing expected file %s in %v", k, got)
		}
	}
}

func TestCollectStableOrder(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"z.go", "a.go", "m.go"} {
		if err := os.WriteFile(filepath.Join(root, rel), []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		inputs, err := Collect([]string{root})
		if err != nil {
			t.Fatal(err)
		}
		if len(inputs) != 3 {
			t.Fatalf("len(inputs) = %d, want 3", len(inputs))
		}
		if filepath.Base(inputs[0].Path) != "a.go" || filepath.Base(inputs[1].Path) != "m.go" || filepath.Base(inputs[2].Path) != "z.go" {
			t.Fatalf("order = %v, want a.go, m.go, z.go", inputs)
		}
	}
}
