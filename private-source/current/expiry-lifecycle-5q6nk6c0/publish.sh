#!/bin/bash
exit 75 # LANE_M_OLD_GATE_EPOCH_CLOSED
set -euo pipefail
proof="$(cd "$(dirname "$0")" && pwd -P)"
python3 "$proof/publish.py" "$1" > "$proof/publish.log" 2>&1
cat "$proof/publish.log"
