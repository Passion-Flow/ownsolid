#!/bin/sh
set -eu
repo=$1
evidence=$2
sha=$3
case "$sha" in *[!0-9a-f]*|'') exit 64 ;; esac
[ "${#sha}" = 40 ]
cd "$repo"
[ "$(git rev-parse "$sha^{commit}")" = "$sha" ]
run="$evidence/joint-$sha"
if [ -f "$run/complete" ]; then
 echo "IAM_JOINT_ALREADY_COMPLETE $sha"
 exit 0
fi
mkdir "$run"
printf '%s\n' "$sha" > "$run/input-sha"
cleanup() {
 if [ -f "$run/gitea-prototype/container-name" ]; then
  provider=$(cat "$run/gitea-prototype/container-name")
  case "$provider" in iam119-gitea-*) docker rm -f "$provider" >/dev/null 2>&1 || true ;; esac
 fi
}
trap cleanup EXIT
trap 'exit 130' INT TERM
printf 'archive\n' > "$run/phase"
git archive --format=tar --output="$run/source.tar" "$sha"
shasum -a 256 "$run/source.tar" > "$run/source-sha256"
mkdir "$run/source"
tar -xf "$run/source.tar" -C "$run/source"
source="$run/source"
printf 'compile\n' > "$run/phase"
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOFLAGS='-p=2 -mod=readonly' go -C "$source/services/control" test -c -tags sso_provider_integration -o "$run/identity-http.test" ./internal/modules/identity/http
ln "$run/identity-http.test" "$run/identity-http-live.test"
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOFLAGS='-p=2 -mod=readonly' go -C "$source/services/control" test -c -o "$run/sso.test" ./internal/modules/sso
printf 'http\n' > "$run/phase"
selector=$(cat "$evidence/joint-http-selector")
if "$evidence/run-joint-offline-http.sh" "$source" "$run" "$selector" 3 > "$run/http.log" 2>&1; then
 echo 'http 0' >> "$run/results"
 echo IAM_JOINT_PASS_HTTP
else
 code=$?
 echo "http $code" >> "$run/results"
 tail -n 35 "$run/http.log"
 exit "$code"
fi
printf 'sso\n' > "$run/phase"
if "$evidence/run-joint-offline-sso.sh" "$source" "$run" . 1 > "$run/sso.log" 2>&1; then
 echo 'sso 0' >> "$run/results"
 echo IAM_JOINT_PASS_SSO
else
 code=$?
 echo "sso $code" >> "$run/results"
 tail -n 35 "$run/sso.log"
 exit "$code"
fi
printf 'gitea\n' > "$run/phase"
python3 "$evidence/provision-provider.py" "$run" > "$run/gitea-setup.log" 2>&1
if "$evidence/run-joint-live.sh" "$source" "$run" > "$run/gitea.log" 2>&1; then
 echo 'gitea 0' >> "$run/results"
 echo IAM_JOINT_PASS_GITEA
else
 code=$?
 echo "gitea $code" >> "$run/results"
 tail -n 35 "$run/gitea.log"
 exit "$code"
fi
printf 'identity-unit\n' > "$run/phase"
unit_tmp=$(mktemp -d /private/tmp/iam-unit.XXXXXXXX)
if TMPDIR="$unit_tmp" GOFLAGS='-p=2 -mod=readonly' go -C "$source/services/control" test ./internal/modules/identity -run '^TestAdminTOTP' -count=3 -v > "$run/identity-unit.log" 2>&1; then
 echo 'identity-unit 0' >> "$run/results"
 echo IAM_JOINT_PASS_IDENTITY_UNIT
else
 code=$?
 echo "identity-unit $code" >> "$run/results"
 tail -n 25 "$run/identity-unit.log"
 exit "$code"
fi
rmdir "$unit_tmp"
printf 'complete\n' > "$run/phase"
printf '%s\n' "$sha" > "$run/complete"
echo "IAM_JOINT_COMPLETE $sha"
