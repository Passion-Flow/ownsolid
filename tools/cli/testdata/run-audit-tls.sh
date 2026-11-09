#!/bin/sh
set -eu

: "${PROJECT_ONE_TEST_DSN:?Set the isolated PostgreSQL test DSN}"

repo=$(CDPATH='' cd -- "$(dirname -- "$0")/../../.." && pwd -P)
scratch=$(mktemp -d "${TMPDIR:-/tmp}/ownsolid-admin-audit-cli.XXXXXXXX")
trap 'rm -r "$scratch"' EXIT

go -C "$repo/tools/cli" build -trimpath -o "$scratch/ownsolid" ./cmd/ownsolid
python3 - "$repo" "$scratch/overlay.json" <<'PY'
import json
from pathlib import Path
import sys

repo, overlay = map(Path, sys.argv[1:])
target = repo / "services/control/internal/modules/identity/http/lane_d_audit_cli_test.go"
source = repo / "tools/cli/testdata/audit_tls_test.go"
overlay.write_text(json.dumps({"Replace": {str(target): str(source)}}), encoding="utf-8")
PY
export LANE_D_CLI_BINARY="$scratch/ownsolid"
go -C "$repo/services/control" test -overlay "$scratch/overlay.json" \
  ./internal/modules/identity/http -run '^(TestLaneDAdminAuditTLS|TestAdminAuditHTTPAssistantAttribution)$' -count=1 -v
