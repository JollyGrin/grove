package chatweb

// The phone UI, embedded so `gv chat serve` is still ONE binary with no
// install step — the same trick orchestrator/embed.go plays with the
// default CLAUDE.md.
//
// ui/ is three hand-written files and nothing else. There is NO JS
// toolchain in this repo: no npm, no lockfile, no node on the host, no
// bundler, no second release cadence. index.html and sw.js are written by
// hand in the spirit of site/index.html; marked.min.js is the one vendored
// third-party file — PINNED at v12.0.2, its SHA-256 recorded in grove-218's
// commit message, and NEVER auto-updated. Bumping it is a deliberate commit
// that re-records the SHA.

import "embed"

//go:embed ui
var uiFS embed.FS
