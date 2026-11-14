#!/bin/bash
set -euo pipefail
git -c http.proxy=http://127.0.0.1:1082 -c http.version=HTTP/1.1 -c http.lowSpeedLimit=100 -c http.lowSpeedTime=15 fetch origin
git rebase origin/main
