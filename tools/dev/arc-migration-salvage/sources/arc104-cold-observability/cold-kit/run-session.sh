#!/bin/bash
set -uo pipefail
umask 077
cd -- "$(dirname -- "$0")"
mkdir -m 0700 session-lock || exit 75
timeout --kill-after=20s 40m bash resume.sh
rc=$?
printf '%s\n' "$rc" > session.exit
exit "$rc"
