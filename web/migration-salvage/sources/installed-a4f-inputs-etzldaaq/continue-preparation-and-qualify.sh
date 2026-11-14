#!/bin/bash
set -euo pipefail
umask 077
root="$1"
if test -e "$root/input-manifest.json"; then exec bash /Users/passion./.local/state/ownsolid/web-scopes/aud108-explicit-retry-s0ycu2hy/run-full-and-publish.sh /Users/passion./.local/state/ownsolid/web-scopes/aud108-explicit-retry-s0ycu2hy; fi
bash "$root/installed-a4f/checks.sh" "$root/installed-a4f" > "$root/checks-corrected.log" 2>&1
python3 "$root/materialize.py" "$root" "/Users/passion./Documents/Passion's WorkSpace/Development/lane-web-acceptance-a4f1120d"
bash /Users/passion./.local/state/ownsolid/web-scopes/aud108-explicit-retry-s0ycu2hy/run-full-and-publish.sh /Users/passion./.local/state/ownsolid/web-scopes/aud108-explicit-retry-s0ycu2hy
