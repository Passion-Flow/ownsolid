#!/bin/sh
set -eu

input=$1
output=$2
models=$3
shift 3

if ! test -d "$models" || test -L "$models" ||
    [ "$(stat -c '%u:%g:%a' "$models")" != "$(id -u):$(id -g):700" ]; then
    printf 'HELM_MODEL_STORE_INVALID: prepare the models subdirectory with the Control UID and GID and mode 0700\n' >&2
    exit 1
fi

umask 077
install -d -m 0700 "$output/private"
# fsGroup gives the volume a setgid root. The control file contract requires 0700.
chmod 00700 "$output/private"

# Kubernetes swaps ..data atomically. A removed generation fails the init step.
cd -P "$input/..data"
for file do
    if ! test -s "$file"; then
        printf 'HELM_RUNTIME_FILE_REQUIRED: %s\n' "$file" >&2
        exit 1
    fi
    install -m 0600 -- "$file" "$output/private/$file"
done
