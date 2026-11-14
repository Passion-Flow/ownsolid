#!/bin/bash
set -euo pipefail
root="$1"
consumer="/Users/passion./.local/state/ownsolid/web-scopes/aud108-explicit-retry-s0ycu2hy"
bash "$root/build-inputs.sh" "$root"
bash "$consumer/run-full-and-publish.sh" "$consumer"
