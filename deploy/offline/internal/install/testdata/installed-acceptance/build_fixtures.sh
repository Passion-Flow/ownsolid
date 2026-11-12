#!/usr/bin/env bash
# Compile the fixture overlays without creating files in the candidate checkout.
set -euo pipefail
test "$#" -eq 2 || { echo 'Usage: build_fixtures.sh SOURCE_ROOT EMPTY_STAGE' >&2; exit 2; }
source_root=$(cd "$1" && pwd -P)
stage=$2
fixtures=$(cd "$(dirname "$0")" && pwd -P)
test -d "$stage"
test -z "$(ls -A "$stage")"
test -z "$(git -C "$source_root" status --porcelain)"
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off
export GOOS=linux GOARCH=amd64 CGO_ENABLED=0
umask 077
python3 - "$source_root" "$stage" "$fixtures" <<'PY'
import json,sys
from pathlib import Path
source,stage,fixtures=map(Path,sys.argv[1:])
assert stage.resolve()==stage and stage.parent.exists()
for name,target in [
 ('history_prepare_test.go','deploy/offline/internal/install/temporary_combined_prepare_test.go'),
 ('installed_acceptance_test.go','tests/e2e/temporary_combined_installed_test.go'),
 ('installed_scope_test.go','services/control/cmd/server/temporary_combined_scope_test.go')]:
 replacements={str(source/target):str(fixtures/name)}
 if name=='installed_acceptance_test.go':
  replacements[str(source/'tests/e2e/egress_test.go')]=str(fixtures/'strict_egress_test.go')
 (stage/(name+'.overlay.json')).write_text(json.dumps({'Replace':replacements})+'\n')
PY
go -C "$source_root/deploy/offline" test -overlay "$stage/history_prepare_test.go.overlay.json" \
  -c -trimpath -o "$stage/history-prepare.test" ./internal/install
go -C "$source_root/tests/e2e" test -overlay "$stage/installed_acceptance_test.go.overlay.json" \
  -c -trimpath -o "$stage/installed.test" .
go -C "$source_root/services/control" test -overlay "$stage/installed_scope_test.go.overlay.json" \
  -c -trimpath -o "$stage/scope.test" ./cmd/server
git -C "$source_root" rev-parse HEAD > "$stage/source-sha"
test -z "$(git -C "$source_root" status --porcelain)"
echo 'Fixture binaries compiled; installation admission and Linux execution remain separate'
