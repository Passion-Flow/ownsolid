#!/usr/bin/env bash
# Runs the offline packaging tests on a Linux amd64 host. They need a non-root user and
# a real mounted private filesystem, which macOS and `make test` do not provide, so they
# skip there. Builds the test binaries here and runs them over SSH as `nobody`.
#
# Usage: test-offline.sh <ssh-host>
set -euo pipefail

host=${1:?ssh host}
repository=$(cd "$(dirname "$0")/../.." && pwd -P)
binaries=$(mktemp -d)
remote=/var/tmp/ownsolid-test-offline
trap 'rm -rf -- "$binaries"' EXIT

# module package: the test binary runs from its package directory, because some tests
# read repository files by relative path.
packages=(
	tools/release/offline/internal/factory
	tools/release/offline/pkg/bundle
	deploy/offline/cmd/operator
	deploy/offline/internal/compose
	deploy/offline/internal/composeprofile
	deploy/offline/internal/configuration
	deploy/offline/internal/engine
	deploy/offline/internal/install
	deploy/offline/internal/prepare
	deploy/offline/internal/scope
	deploy/offline/internal/tlsidentity
	deploy/offline/pkg/maintenance
)

for package in "${packages[@]}"; do
	(cd "$repository/$package" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -o "$binaries/$(echo "$package" | tr / -).test" .)
done

ssh "$host" "rm -rf $remote/source $remote/bin && mkdir -p $remote/source $remote/bin $remote/tmp && { mountpoint -q $remote/tmp || mount -t tmpfs -o size=4g,mode=0700 tmpfs $remote/tmp; } && chown nobody $remote/tmp && chmod 755 $remote $remote/bin"
(cd "$repository" && git ls-files -z --cached --others --exclude-standard | COPYFILE_DISABLE=1 tar --no-xattrs --null -T - -cf -) | ssh "$host" "tar -xf - -C $remote/source 2>/dev/null; chmod -R a+rX $remote/source"
scp -q "$binaries"/*.test "$host:$remote/bin/"
ssh "$host" "chmod 755 $remote/bin/*.test"

failed=0
for package in "${packages[@]}"; do
	name=$(echo "$package" | tr / -)
	echo "=== $package"
	if ! ssh "$host" "set -o pipefail; cd $remote/source/$package && timeout 3600 setpriv --reuid=nobody --regid=nogroup --clear-groups env TMPDIR=$remote/tmp HOME=/nonexistent $remote/bin/$name.test -test.count=1 -test.timeout=55m 2>&1 | tail -60"; then
		failed=1
	fi
done
ssh "$host" "umount $remote/tmp; rm -rf $remote/source $remote/bin"
exit "$failed"
