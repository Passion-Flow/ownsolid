#!/bin/bash
set -euo pipefail
source /root/lane-rel-tools/env
export GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local GOWORK=off GOMAXPROCS=2 GOFLAGS='-p=1 -mod=readonly' CGO_ENABLED=1
python3 - <<'PY'
from pathlib import Path
import os
m=int(next(s.split()[1] for s in Path('/proc/meminfo').read_text().splitlines() if s.startswith('MemFree:')))//1024
v=os.statvfs('/root')
if m<2048 or v.f_bavail*v.f_frsize<15*1024**3:raise SystemExit(75)
PY
exec flock --nonblock --conflict-exit-code 75 /run/lock/ownsolid-heavy.lock flock --nonblock --conflict-exit-code 75 /root/lane-sci-tools/heavy-test.lock flock --nonblock --conflict-exit-code 75 /root/lane-rel-tools/heavy-test.lock bash -c '
set -euo pipefail
python3 - <<"PY"
from pathlib import Path
import os
m=int(next(s.split()[1] for s in Path("/proc/meminfo").read_text().splitlines() if s.startswith("MemFree:")))//1024
v=os.statvfs("/root")
if m<2048 or v.f_bavail*v.f_frsize<15*1024**3:raise SystemExit(75)
PY
cd /root/lane-rel-tools/model-history-prototype/wire/go
gofmt -w history_wire_test.go
timeout --signal=TERM --kill-after=5s 120s go test -race -count=1 ./...
'
