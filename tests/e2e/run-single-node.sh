#!/usr/bin/env bash
# Runs the single-node install inside a privileged Docker-in-Docker container started
# with --network none, so the installer, the Docker daemon and every workload have no
# route to anything. The container's default route is a TUN interface held by
# egress-recorder: every packet that tries to leave is written to a log and refused at
# once, and the test fails if anything but its own probes was recorded. Needs Docker on
# a Linux host; the tested images and the binaries from `make e2e-binaries` come in
# through a read-only bind mount.
#
# Usage: run-single-node.sh <binaries-directory> <control-image> <postgres-image> <metrics-image> <logs-image> <dind-image>
#
# OWNSOLID_E2E_SCRATCH names the directory for the image archive (default /var/tmp), and
# OWNSOLID_E2E_DOCKER_TMPFS, for example 12g, keeps the isolated Docker daemon's storage in
# memory. Together they keep a run off a full disk: /dev/shm and 12g need no disk at all.
# OWNSOLID_E2E_SIMULATED_AGENTS sizes the simulated fleet (default 1000).
set -euo pipefail

binaries=${1:?binaries directory}
control_image=${2:?control image}
postgres_image=${3:?postgres image}
metrics_image=${4:?metrics store image}
logs_image=${5:?logs store image}
dind_image=${6:?docker-in-docker image}

work=$(mktemp -d "${OWNSOLID_E2E_SCRATCH:-/var/tmp}/ownsolid-e2e.XXXXXX")
name=$(basename "$work" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9-\n' '-')

cleanup() {
	docker rm --force --volumes "$name" >/dev/null 2>&1 || true
	rm -rf -- "$work"
}
trap cleanup EXIT

egress_interface=egress0
egress_log=/var/log/ownsolid-egress.jsonl

cp "$binaries/ownsolid-install" "$binaries/e2e.test" "$binaries/agent" "$binaries/inference-egress.test" "$binaries/egress-recorder" "$binaries/simulator" "$binaries/compose.yaml" "$work/"
chmod 755 "$work" "$work/ownsolid-install" "$work/e2e.test" "$work/agent" "$work/inference-egress.test" "$work/egress-recorder" "$work/simulator"
echo "saving images"
docker save --output "$work/images.tar" "$control_image" "$postgres_image" "$metrics_image" "$logs_image"

storage=()
if [ -n "${OWNSOLID_E2E_DOCKER_TMPFS:-}" ]; then
	storage=(--tmpfs "/var/lib/docker:size=$OWNSOLID_E2E_DOCKER_TMPFS")
fi
docker run --detach --name "$name" --privileged --network none "${storage[@]}" \
	--env DOCKER_TLS_CERTDIR= --volume "$work:/bundle:ro" "$dind_image" >/dev/null

echo "waiting for the isolated Docker daemon"
for _ in $(seq 1 60); do
	if docker exec "$name" docker info >/dev/null 2>&1; then
		break
	fi
	sleep 1
done
docker exec "$name" docker info >/dev/null

echo "routing everything off the host to the egress recorder"
docker exec --detach "$name" /bundle/egress-recorder -interface "$egress_interface" -log "$egress_log"
for _ in $(seq 1 30); do
	if docker exec "$name" test -e "/sys/class/net/$egress_interface"; then
		break
	fi
	sleep 1
done
# 198.18.0.0/15 (RFC 2544) and 2001:db8::/32 (RFC 3849) are never routed anywhere real.
docker exec "$name" sh -euc "
	ip link set $egress_interface up
	ip addr add 198.18.0.1/32 dev $egress_interface
	ip -6 addr add 2001:db8:e9e5::1/128 dev $egress_interface
	ip route add default dev $egress_interface
	ip -6 route add default dev $egress_interface
"

docker exec \
	--env OWNSOLID_E2E_INSTALLER=/bundle/ownsolid-install \
	--env OWNSOLID_E2E_AGENT=/bundle/agent \
	--env OWNSOLID_E2E_SIMULATOR=/bundle/simulator \
	--env OWNSOLID_E2E_SIMULATED_AGENTS="${OWNSOLID_E2E_SIMULATED_AGENTS:-}" \
	--env OWNSOLID_E2E_INFERENCE_EGRESS_TEST=/bundle/inference-egress.test \
	--env OWNSOLID_E2E_COMPOSE_FILE=/bundle/compose.yaml \
	--env OWNSOLID_E2E_IMAGE_ARCHIVE=/bundle/images.tar \
	--env OWNSOLID_E2E_CONTROL_IMAGE="$control_image" \
	--env OWNSOLID_E2E_POSTGRES_IMAGE="$postgres_image" \
	--env OWNSOLID_E2E_METRICS_IMAGE="$metrics_image" \
	--env OWNSOLID_E2E_LOGS_IMAGE="$logs_image" \
	--env OWNSOLID_E2E_ROOT=/srv/ownsolid \
	--env OWNSOLID_E2E_EGRESS_LOG="$egress_log" \
	--env OWNSOLID_E2E_EGRESS_INTERFACE="$egress_interface" \
	"$name" /bundle/e2e.test -test.v -test.timeout=30m
