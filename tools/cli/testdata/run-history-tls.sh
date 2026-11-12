#!/bin/sh
set -eu

: "${PROJECT_ONE_TEST_DSN_FILE:?Set the isolated PostgreSQL test DSN file}"
: "${OWNSOLID_TEST_VICTORIA_METRICS:?Set the verified native VictoriaMetrics executable}"
: "${OWNSOLID_TEST_VMALERT:?Set the verified native vmalert executable}"

repo=$(CDPATH='' cd -- "$(dirname -- "$0")/../../.." && pwd -P)
scratch=$(mktemp -d "${TMPDIR:-/tmp}/ownsolid-history-cli.XXXXXXXX")
trap 'rm -r "$scratch"' EXIT

go -C "$repo/tools/cli" build -trimpath -o "$scratch/ownsolid" ./cmd/ownsolid
python3 - "$repo" "$scratch/overlay.json" <<'PY'
import json
from pathlib import Path
import sys

repo, overlay = map(Path, sys.argv[1:])
target = repo / "services/control/internal/modules/monitoring/lane_d_history_cli_test.go"
source = repo / "tools/cli/testdata/monitoring_history_workflow_test.go"
overlay.write_text(json.dumps({"Replace": {str(target): str(source)}}), encoding="utf-8")
PY
export LANE_D_CLI_BINARY="$scratch/ownsolid"
go -C "$repo/services/control" test -overlay "$scratch/overlay.json" \
  -tags repository_integration,monitoring_integration ./internal/modules/monitoring \
  -run '^TestLaneDHistoryTLS$' -count=1 -v -timeout=3m

