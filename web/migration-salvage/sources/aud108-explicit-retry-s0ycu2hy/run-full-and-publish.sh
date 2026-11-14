#!/bin/bash
set -euo pipefail
root="$1"
run="$(mktemp -d "$root/full-XXXXXX")"
printf '%s\n' "$run" > "$root/full-current-path"
cp "$root/workers.mk" "$run/workers.mk"
bash "$root/full-gate.sh" "$run"
python3 "$root/finish-full.py" "$root" "$run"
