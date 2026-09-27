#!/bin/sh
# grove's serve contract (docs/plans/2026-09-27-feature-trains-design.md
# §Decision 7): grove is a CLI, so "serving" a train is a throwaway build
# of its tip — the path is what the operator is handed. Never go install
# (it stamps the binary `dev` and breaks the next `gv update`).
# gv exports GROVE_WORKTREE (the serve worktree at the feature tip) and
# GROVE_FEATURE (the slug); GROVE_PORT/GROVE_BRANCH are unused here.
set -eu
: "${GROVE_WORKTREE:?run by gv serve}" "${GROVE_FEATURE:?run by gv serve}"
out="/tmp/gv-$GROVE_FEATURE"
cd "$GROVE_WORKTREE"
go build -o "$out" ./cmd/gv
echo "GROVE_READY $out"
