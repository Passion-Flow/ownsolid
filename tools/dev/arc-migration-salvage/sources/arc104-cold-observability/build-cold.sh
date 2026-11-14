#!/bin/bash
# Rebuild the test-only Linux cold package without changing the lane checkout.
set -euo pipefail
umask 077
dir=$(cd -- "$(dirname -- "$0")" && pwd -P)
repo=$(cd -- "$1" && pwd -P)
kit=$dir/cold-kit
expected=$(cat "$kit/e2e-source")
git -C "$repo" merge-base --is-ancestor "$expected" HEAD
build=$(mktemp -d "$dir/build.XXXXXX")
mkdir -m 0700 "$build/source" "$build/e2e-source" "$build/replacements"
git -C "$repo" archive "$(cat "$kit/control-source")" | tar xf - -C "$build/source"
python3 "$dir/build-control.py" "$build/source" "$build/replacements"
GOPROXY=off GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go -C "$build/source/services/control" build \
    -overlay "$build/replacements/control-overlay.json" -o "$kit/diagnostic-control" ./cmd/server
git -C "$repo" archive "$expected" | tar xf - -C "$build/e2e-source"
python3 - "$kit" "$build/e2e-source" "$build" <<'PY'
import json,sys
from pathlib import Path
kit,repo,build=map(Path,sys.argv[1:])
pairs=[("arc104_registry_test.go","managed_test.go"),("egress_test.go","egress_test.go"),
       ("single_node_install_test.go","single_node_install_test.go")]
(build/"e2e-overlay.json").write_text(json.dumps({"Replace":{
    str(repo/"tests/e2e"/name):str(kit/source) for name,source in pairs}}))
PY
GOPROXY=off GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go -C "$build/e2e-source/tests/e2e" test \
    -overlay "$build/e2e-overlay.json" -c -o "$kit/managed.test"
(cd "$kit"; shasum -a 256 *.sh *.py *.go managed.test diagnostic-control control-source e2e-source > SHA256SUMS)
