// Package orchestrator carries the default orchestrator CLAUDE.md, embedded
// so the single binary can install it into ~/.config/grove/orchestrator/
// on first `ovs ui`. Edit the installed copy to customize; this file is only
// the seed.
package orchestrator

import _ "embed"

//go:embed CLAUDE.md
var ClaudeMd string
