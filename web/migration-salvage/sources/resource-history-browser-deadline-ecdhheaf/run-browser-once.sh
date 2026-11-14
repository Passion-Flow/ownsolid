#!/bin/sh

# OWNSOLID_OLD_MAC_EPOCH_CLOSED
printf '%s\n' 'Old Mac admission epoch is closed; await coordinated activation.' >&2
exit 78
set -eu
exec python3 -I /Users/passion./.local/state/ownsolid/web-scopes/resource-history-browser-deadline-ecdhheaf/mac-browser-once.py /Users/passion./.local/state/ownsolid/web-scopes/resource-history-browser-deadline-ecdhheaf
