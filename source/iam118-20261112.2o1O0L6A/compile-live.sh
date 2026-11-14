#!/bin/sh
set -eu
root=$1
evidence=$2
cd "$root"
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOFLAGS='-p=2 -mod=readonly' go -C services/control test -c -tags sso_provider_integration -o "$evidence/identity-http-live.test" ./internal/modules/identity/http

cp "$evidence/identity-http-live.test" "$evidence/identity-http.test"
