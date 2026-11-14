#!/usr/bin/env bash
# Runs only against a disposable PostgreSQL container with loopback networking.
set -euo pipefail

repo=$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)
cd "$repo"
: "${OWNSOLID_TEST_PARQUET_PYTHON:?Set this to the protected Python 3.12 interpreter with the locked PyArrow wheel}"
: "${PROJECT_ONE_DATA_PYTHON:?Set this to the protected Python 3.12 interpreter for draft inspection}"
df -h .
free -m
ss -lntp
docker_context=$(docker context show)
image=$(docker --context "$docker_context" image inspect pgvector/pgvector:0.8.7-pg18-bookworm --format '{{.Id}}')
umask 077
scratch=$(mktemp -d "${TMPDIR:-/tmp}/ownsolid-draft-import.XXXXXXXX")
container=
cluster="draft-import-${scratch##*.}"
cleanup() {
    local stopped=0
    if [[ -n "$container" ]]; then
        docker --context "$docker_context" stop "$container" >/dev/null || stopped=$?
    fi
    if [[ -f "$scratch/dsn" ]]; then
        unlink "$scratch/dsn"
    fi
    rmdir "$scratch"
    return "$stopped"
}
trap cleanup EXIT
printf '%s\n' 'postgres://postgres@127.0.0.1:5432/ownsolid_draft_import_test?sslmode=disable' > "$scratch/dsn"
unset PROJECT_ONE_TEST_DSN PROJECT_ONE_TEST_NATIVE_POSTGRES_INPUT
export PROJECT_ONE_TEST_DSN_FILE="$scratch/dsn" GOPROXY=off
container=$(docker --context "$docker_context" run --rm --detach --pull never --network none --memory 1g --cpus 2 \
    --name "ownsolid-${scratch##*.}-draft-import" --label "ownsolid.fixture.cluster=$cluster" \
    --env POSTGRES_HOST_AUTH_METHOD=trust --env POSTGRES_DB=ownsolid_draft_import_test "$image" -c "cluster_name=$cluster")
export OWNSOLID_TEST_DRAFT_RESTORE_CONTAINER="$container" OWNSOLID_TEST_DRAFT_RESTORE_CLUSTER="$cluster"
export OWNSOLID_TEST_DRAFT_RESTORE_DOCKER_CONTEXT="$docker_context"
ready=false
for ((attempt=0; attempt<35; attempt++)); do
    if docker --context "$docker_context" exec "$container" pg_isready -h 127.0.0.1 -U postgres -d ownsolid_draft_import_test >/dev/null; then
        ready=true
        break
    fi
    sleep 1
done
if [[ "$ready" != true ]]; then
    echo 'The disposable PostgreSQL server did not become ready.' >&2
    exit 1
fi
docker --context "$docker_context" exec "$container" psql -h 127.0.0.1 -U postgres -d ownsolid_draft_import_test \
    -v ON_ERROR_STOP=1 -c 'CREATE EXTENSION vector WITH SCHEMA public'
pid=$(docker --context "$docker_context" inspect --format '{{.State.Pid}}' "$container")
nsenter -t "$pid" -n ip -br address
pattern=${1:-'^(TestActualDatasetTLSContractsLargeUnicodeRowsCSRFExportAndReadiness|TestSourceImportParquet|TestActualParquetDraftAfterMetadataAndObjectRestore|TestActualCSVDraftFileSnapshotAndRestore|TestActualDatasetFileSnapshotExactReferencesPrivateRestoreAndCorruption)$'}
nsenter -t "$pid" -n go -C services/control test -tags dataset_processor_integration \
    -p 1 -count=1 -timeout 5m -v ./internal/modules/datasets/... -run "$pattern"
