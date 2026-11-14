#!/bin/bash
set -euo pipefail
proof="$(cd "$(dirname "$0")" && pwd -P)"
python3 "$proof/prepare-final-source.py" "$1" > "$proof/prepare-final-source.log" 2>&1
cat "$proof/prepare-final-source.log"
