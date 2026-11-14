#!/bin/sh
set -eu
root=$1
evidence=$2
cd "$root"
export GOFLAGS='-p=2 -mod=readonly'
export TMPDIR=/private/tmp/
go -C services/control test -count=1 ./internal/modules/sso ./internal/modules/sso/http > "$evidence/final-unit.log" 2>&1
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go -C services/control test -c -tags sso_provider_integration -o "$evidence/identity-http-live.test" ./internal/modules/identity/http > "$evidence/final-compile.log" 2>&1
cp "$evidence/identity-http-live.test" "$evidence/identity-http.test"
echo IAM_TARGETED_BUILD_PASSED
