#!/bin/bash
set -euo pipefail
root="$1"
inputs="$2"
python3 "$root/complete-doc-only-publish.py" "$root" "$root/publication-doc-sync-t_svekfo"
python3 "$inputs/prepare.py" "$inputs"
bash "$inputs/build-inputs.sh" "$inputs"
