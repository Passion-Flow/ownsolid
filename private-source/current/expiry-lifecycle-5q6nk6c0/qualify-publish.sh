#!/bin/bash
exit 75 # LANE_M_OLD_GATE_EPOCH_CLOSED
set -euo pipefail
proof="$(cd "$(dirname "$0")" && pwd -P)"
python3 "$proof/publication-preflight.py" "$1" > "$proof/integration-preflight.log" 2>&1
cd "$1"
test -z "$(git status --porcelain)"
git diff --exit-code 6e82e87f HEAD -- services/control/internal/modules/models services/control/internal/modules/modelpreparations services/control/internal/modules/deployments runtimes/model-lifecycle services/control/migrations services/control/internal/testing/identityfixture
GOMAXPROCS=2 GOPROXY=off GOSUMDB=off go -C services/control test -p=2 ./internal/modules/models -run '^(TestImportExpiryPolicyValidation|TestModelImportWorkerAlreadyStopped)$' -count=1 -v > "$proof/schema59-integration-unit.log" 2>&1
git rev-parse HEAD > "$proof/integration-source"
python3 "$proof/publish.py" "$1" > "$proof/publish-after-integration.log" 2>&1
cat "$proof/schema59-integration-unit.log"
cat "$proof/publish-after-integration.log"
