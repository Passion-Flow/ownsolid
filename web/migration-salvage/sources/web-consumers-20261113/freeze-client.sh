#!/bin/bash

# OWNSOLID_OLD_MAC_EPOCH_CLOSED
printf '%s\n' 'Old Mac admission epoch is closed; await coordinated activation.' >&2
exit 78
set -euo pipefail
root="$1"
frozen="$2"
cd "$frozen/web"
pnpm install --offline --frozen-lockfile --ignore-scripts > "$root/frozen-dependencies.log" 2>&1
python3 "$root/installed-be62/bind.py" "$frozen" be62f58e846dcc654dafee65cba6cc7480890570
bash "$root/installed-be62/checks.sh" "$root/installed-be62"
