#!/usr/bin/env bash
set -euo pipefail
work=$(cd "$1" && pwd -P)
go=/root/lane-sci-tools/go/bin/go
export GOENV=off GOFLAGS=-buildvcs=false GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off CGO_ENABLED=0
export GOCACHE=/root/lane-sci-tools/go-cache GOMODCACHE=/root/lane-sci-tools/go-mod
test "$("$go" version)" = 'go version go1.27.3 linux/amd64'
python3 - "$work" <<'PY'
import json
import pathlib
import sys

work = pathlib.Path(sys.argv[1])
test = "audit_candidate_restore_matrix_test.go"
target = work / "repo/services/control/internal/platform/database" / test
(work / "matrix-overlay.json").write_text(json.dumps({"Replace": {str(target): str(work / test)}}))
PY
"$go" -C "$work/repo/services/control" test -p 1 -overlay "$work/matrix-overlay.json" -c -o "$work/database.test" ./internal/platform/database
"$go" -C "$work/repo/services/control" test -p 1 -c -o "$work/audit.test" ./internal/modules/audit
"$go" -C "$work/repo/tools/audit-verify" build -p 1 -o "$work/audit-verify" .
