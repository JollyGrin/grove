// Package seeds is the archive of every orchestrator seed grove has ever
// shipped, keyed by SeedStamp — the base a migration diffs an edited brain
// against (grove-513, brain-overlay design §Migration step 1).
//
// The archive is generated, not hand-written: `go generate ./orchestrator/seeds`
// runs gen.go, which walks `git log -- orchestrator/CLAUDE.md`, adds the
// working-tree seed, stamps each distinct body with the same function the
// brains carry and writes them all into one deterministic tar.gz. Embedding
// it means a brain's base is found with no git at runtime — on groveremote,
// in a workspace with no repository, and for an OSS user who installed a
// release binary.
//
// Re-run the generator whenever orchestrator/CLAUDE.md changes;
// TestArchiveHoldsCurrentSeed fails until you do.
package seeds

//go:generate go run gen.go

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	_ "embed"
	"io"
	"sort"
	"strings"
	"sync"
)

// ArchiveFile is the embedded archive's basename: one `<stamp>.md` entry
// per distinct seed body, stamp lines stripped, sorted by stamp.
const ArchiveFile = "archive.tar.gz"

//go:embed archive.tar.gz
var archive []byte

var (
	once    sync.Once
	byStamp map[string]string
	loadErr error
)

func load() {
	byStamp = map[string]string{}
	zr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		loadErr = err
		return
	}
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			loadErr = err
			return
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			loadErr = err
			return
		}
		byStamp[strings.TrimSuffix(hdr.Name, ".md")] = string(body)
	}
}

// Lookup returns the seed body archived under stamp, and whether one is.
// The body is unstamped — exactly the bytes SeedStamp hashes.
func Lookup(stamp string) (string, bool) {
	once.Do(load)
	body, ok := byStamp[stamp]
	return body, ok
}

// Stamps lists every archived stamp, sorted.
func Stamps() []string {
	once.Do(load)
	out := make([]string, 0, len(byStamp))
	for s := range byStamp {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Err reports a corrupt archive — only ever a build defect, since the
// generator wrote it and the test read it back.
func Err() error {
	once.Do(load)
	return loadErr
}
