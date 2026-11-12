#!/usr/bin/env bash
# Replays only retained CPU evidence and an isolated PostgreSQL service read.
set -euo pipefail

repo=$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)
cd "$repo"
: "${PROJECT_ONE_ACTUAL_TRAINING_OUTPUT:?Set the retained CPU output directory}"
: "${PROJECT_ONE_ACTUAL_DATASET_DIRECTORY:?Set the authorized publication export directory}"
: "${PROJECT_ONE_APPROVED_MODEL_DIRECTORY:?Set the verified base configuration directory}"

df -h .
free -m
ss -lnt
docker_context=$(docker context show)
image=$(docker --context "$docker_context" image inspect pgvector/pgvector:0.8.7-pg18-bookworm --format '{{.Id}}')
umask 077
scratch=$(mktemp -d "${TMPDIR:-/tmp}/ownsolid-training-experiment.XXXXXXXX")
container=
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

database=ownsolid_training_experiment_test
printf '%s\n' "postgres://postgres@127.0.0.1:5432/$database?sslmode=disable" > "$scratch/dsn"
export PROJECT_ONE_REPOSITORY_ENGINE=postgresql PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE="$scratch/dsn"
export PROJECT_ONE_REPOSITORY_POSTGRES_SCHEMA="m20_repository_$(tr -d '-' < /proc/sys/kernel/random/uuid)"
export GOPROXY=off
container=$(docker --context "$docker_context" run --rm --detach --pull never --network none --memory 1g --cpus 2 \
    --name "lane-trn-experiment-${scratch##*.}" --env POSTGRES_HOST_AUTH_METHOD=trust \
    --env "POSTGRES_DB=$database" "$image")
ready=false
for ((attempt=0; attempt<35; attempt++)); do
    if docker --context "$docker_context" exec "$container" pg_isready -h 127.0.0.1 -U postgres -d "$database" >/dev/null; then
        ready=true
        break
    fi
    sleep 1
done
if [[ "$ready" != true ]]; then
    echo 'The disposable PostgreSQL server did not become ready.' >&2
    exit 1
fi
docker --context "$docker_context" exec "$container" psql -h 127.0.0.1 -U postgres -d "$database" \
    -v ON_ERROR_STOP=1 -c 'CREATE EXTENSION vector WITH SCHEMA public'
pid=$(docker --context "$docker_context" inspect --format '{{.State.Pid}}' "$container")
nsenter -t "$pid" -n ip -br address
nsenter -t "$pid" -n go -C services/control test \
    -tags 'training_artifact_integration,training_experiment_integration,repository_integration' \
    -p 1 -count=1 -timeout 5m -v ./internal/modules/training \
    -run '^(TestActualAcceptedResultJSONBMayExceedRawBudget|TestActualTrainingExecutionEvidenceAdmission|TestActualCPUExperimentInputs|TestActualApprovedCpuAdapterMetadataShapeAndEveryF32Value|TestActualAcceptedExperimentRead|TestActualBorrowedAcceptedAdapter|TestTrainingLocalCellOwnerFence|TestActualPairedTrainingDispatch|TestActualPairedTrainingOperator)$'
