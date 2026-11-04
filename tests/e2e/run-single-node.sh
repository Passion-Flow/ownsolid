#!/usr/bin/env bash
# Runs the single-node install inside a privileged Docker-in-Docker container started
# with --network none, so the installer, the Docker daemon and every workload have no
# route to anything. Needs Docker on a Linux host; the tested images and the binaries
# from `make e2e-binaries` come in through a read-only bind mount.
#
# Usage: run-single-node.sh <binaries-directory> <control-image> <postgres-image> <dind-image>
set -euo pipefail

binaries=${1:?binaries directory}
control_image=${2:?control image}
postgres_image=${3:?postgres image}
dind_image=${4:?docker-in-docker image}

work=$(mktemp -d /var/tmp/ownsolid-e2e.XXXXXX)
name=$(basename "$work" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9-\n' '-')

cleanup() {
	docker rm --force --volumes "$name" >/dev/null 2>&1 || true
	rm -rf -- "$work"
}
trap cleanup EXIT

cp "$binaries/ownsolid-install" "$binaries/e2e.test" "$binaries/compose.yaml" "$work/"
chmod 755 "$work" "$work/ownsolid-install" "$work/e2e.test"
echo "saving images"
docker save --output "$work/images.tar" "$control_image" "$postgres_image"

docker run --detach --name "$name" --privileged --network none \
	--env DOCKER_TLS_CERTDIR= --volume "$work:/bundle:ro" "$dind_image" >/dev/null

echo "waiting for the isolated Docker daemon"
for _ in $(seq 1 60); do
	if docker exec "$name" docker info >/dev/null 2>&1; then
		break
	fi
	sleep 1
done
docker exec "$name" docker info >/dev/null

docker exec \
	--env OWNSOLID_E2E_INSTALLER=/bundle/ownsolid-install \
	--env OWNSOLID_E2E_COMPOSE_FILE=/bundle/compose.yaml \
	--env OWNSOLID_E2E_IMAGE_ARCHIVE=/bundle/images.tar \
	--env OWNSOLID_E2E_CONTROL_IMAGE="$control_image" \
	--env OWNSOLID_E2E_POSTGRES_IMAGE="$postgres_image" \
	--env OWNSOLID_E2E_ROOT=/srv/ownsolid \
	"$name" /bundle/e2e.test -test.v -test.timeout=30m
