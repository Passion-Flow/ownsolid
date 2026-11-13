#!/bin/bash
set -euo pipefail
source /root/lane-rel-tools/env
export GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local GOMAXPROCS=2 GOFLAGS=-p=1 CGO_ENABLED=1
python3 - <<'PY'
from pathlib import Path
import os
mem=int(next(x.split()[1] for x in Path('/proc/meminfo').read_text().splitlines() if x.startswith('MemFree:')))//1024
disk=os.statvfs('/root')
if mem<2048 or disk.f_bavail*disk.f_frsize<15360*1048576:raise SystemExit(75)
PY
exec flock --nonblock --conflict-exit-code 75 /run/lock/ownsolid-heavy.lock flock --nonblock --conflict-exit-code 75 /root/lane-sci-tools/heavy-test.lock flock --nonblock --conflict-exit-code 75 /root/lane-rel-tools/heavy-test.lock bash -c '
set -euo pipefail
python3 - <<"PY"
from pathlib import Path
import os
mem=int(next(x.split()[1] for x in Path("/proc/meminfo").read_text().splitlines() if x.startswith("MemFree:")))//1024
disk=os.statvfs("/root")
if mem<2048 or disk.f_bavail*disk.f_frsize<15360*1048576:raise SystemExit(75)
PY
cd /root/lane-rel-workspace-operations
gofmt -w tools/cli/workspace_operation_reads.go tools/cli/workspace_operation_reads_test.go tools/cli/workspaces.go tools/cli/cli_test.go
timeout --signal=TERM --kill-after=5s 180s unshare --net -- bash -c "set -e; ip link set dev lo up; go test -race ./tools/cli/...; go vet ./tools/cli/..."
python3 tools/dev/roadmap_progress.py
make check-layout check-roadmap
git diff --check
'
