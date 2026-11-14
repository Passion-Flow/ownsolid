#!/usr/bin/env bash
set -euo pipefail
umask 077
root=$1
repo=$2
image=pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47
name="arc109-payload-$(basename "$root" | cut -d. -f2 | tr '[:upper:]' '[:lower:]')"
test -x "$root/training-before.test"
test -x "$root/training-after.test"
test -x "$root/python-local/bin/python3.12"
openssl rand -hex 32 > "$root/password"
docker volume create "$name" >/dev/null
trap 'docker rm -f "$name" >/dev/null 2>&1 || true; docker volume rm "$name" >/dev/null' EXIT
docker run -d --name "$name" --pull never --network none --cpus 2 --memory 1536m --shm-size 128m \
  --mount "type=volume,src=$name,dst=/var/lib/postgresql" \
  --mount "type=bind,src=$root,dst=/fixture,readonly" \
  --mount "type=bind,src=$repo,dst=$repo,readonly" \
  -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD_FILE=/run/fixture-password \
  --entrypoint bash "$image" -ec '
    install -o postgres -m 0400 /fixture/password /run/fixture-password
    exec docker-entrypoint.sh postgres -c listen_addresses=127.0.0.1
  ' >/dev/null
for _ in {1..60}; do
  if [[ $(docker exec "$name" cat /proc/1/comm) == postgres ]] &&
    docker exec "$name" pg_isready -q -h 127.0.0.1 -U postgres -d postgres; then break; fi
  sleep 0.5
done
docker inspect "$name" --format 'network={{.HostConfig.NetworkMode}}'
docker exec "$name" bash -ec '
  test -z "$(ip route show default)"
  test -z "$(ip -6 route show default)"
  /fixture/python-local/bin/python3.12 --version
  psql -XAt -U postgres -d postgres -c "SHOW server_version"
'
for stage in before after; do
  database="training_payload_${stage}_test"
  printf 'postgresql://postgres:%s@127.0.0.1:5432/%s?sslmode=disable\n' "$(<"$root/password")" "$database" > "$root/$stage.dsn"
  docker exec "$name" createdb -U postgres "$database"
  docker exec "$name" psql -Xq -U postgres -d "$database" -v ON_ERROR_STOP=1 \
    -c "CREATE EXTENSION vector VERSION '0.8.7'; REVOKE CREATE ON SCHEMA public FROM PUBLIC"
  docker exec -e "PROJECT_ONE_TEST_DSN_FILE=/fixture/$stage.dsn" \
    -e PROJECT_ONE_DATA_PYTHON=/fixture/python-local/bin/python3.12 \
    -e GOMAXPROCS=2 -w "$repo/services/control/internal/modules/training" "$name" \
    "/fixture/training-$stage.test" -test.v -test.timeout 3m -test.count 1 \
    -test.run '^TestAgentCallCostDoesNotGrowWithTheFleet$/^training_authority$' | tee "$root/$stage-cost.log"
done
docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/after.dsn \
  -e PROJECT_ONE_DATA_PYTHON=/fixture/python-local/bin/python3.12 \
  -e GOMAXPROCS=2 -w "$repo/services/control/internal/modules/training" "$name" \
  /fixture/training-after.test -test.v -test.timeout 3m -test.count 1 \
  -test.run '^(TestActualTrainingClaimCancellationRaceAndTamperedCommandRollback|TestActualTrainingAtomicOrderedEventBatchConcurrentReplayAndRevocation|TestActualTrainingChunkStreamReleasesBusinessLocksAndRejectsStaleAuthority|TestActualTLSMachineTrainingClaimAndRenewMatchesGeneratedSchema)$' | tee "$root/after-regression.log"
