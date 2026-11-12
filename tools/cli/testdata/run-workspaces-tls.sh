#!/bin/sh
set -eu

: "${PROJECT_ONE_TEST_DSN_FILE:?Set the isolated PostgreSQL test DSN file}"

repo=$(CDPATH='' cd -- "$(dirname -- "$0")/../../.." && pwd -P)
scratch=$(mktemp -d "${TMPDIR:-/tmp}/ownsolid-workspaces-cli.XXXXXXXX")
trap 'rm -r "$scratch"' EXIT

go -C "$repo/tools/cli" build -trimpath -o "$scratch/ownsolid" ./cmd/ownsolid
python3 - "$repo" "$scratch/overlay.json" <<'PY'
import json
from pathlib import Path
import sys

repo, overlay = map(Path, sys.argv[1:])
root = repo / "services/control/internal/modules/identity/http"
sources = {
    "lane_d_workspace_cli_test.go": "workspaces_tls_test.go",
    "lane_d_workspace_cli_helpers_test.go": "workflow_tls_test.go",
}
overlay.write_text(json.dumps({"Replace": {
    str(root / target): str(repo / "tools/cli/testdata" / source)
    for target, source in sources.items()
}}), encoding="utf-8")
PY
export LANE_D_CLI_BINARY="$scratch/ownsolid"
go -C "$repo/services/control" test -race -overlay "$scratch/overlay.json" \
  ./internal/modules/identity/http -run '^TestLaneDWorkspaceReadsTLS$' -count=1 -v
