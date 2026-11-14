#!/bin/sh
set -eu
umask 077
scratch=$(cd "$(dirname "$0")" && pwd -P)
export GOPROXY=off GOSUMDB=off GOFLAGS='-p=2' CGO_ENABLED=0 GOOS=linux
GOARCH=amd64 go -C services/agent build -trimpath -o "$scratch/agent-amd64" ./cmd/agent
GOARCH=arm64 go -C services/control test -c -trimpath -tags repository_integration -o "$scratch/enrollment.test" ./internal/modules/nodes
openssl rand -hex 24 > "$scratch/pg-password"
python3 - "$scratch" <<'PY'
from pathlib import Path
import sys
root = Path(sys.argv[1]); password = (root / 'pg-password').read_text().strip()
(root / 'pg-dsn').write_text(f'postgres://postgres:{password}@127.0.0.1:5432/arc_flt102_test?sslmode=disable\n')
PY
name="ownsolid-arc-flt102-$(uuidgen | tr A-Z a-z)"
docker run -d --pull never --name "$name" --network none --cpus 2 --memory 512m --mount "type=bind,src=$scratch,dst=/probe,readonly" -e POSTGRES_PASSWORD_FILE=/probe/pg-password -e POSTGRES_DB=arc_flt102_test --tmpfs /var/lib/postgresql:rw,size=256m sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47 >/dev/null
trap 'docker rm -f "$name" >/dev/null; rm "$scratch/pg-password" "$scratch/pg-dsn"' EXIT
for i in $(seq 1 30); do
  if docker exec "$name" pg_isready -h 127.0.0.1 -U postgres -d arc_flt102_test >/dev/null; then break; fi
  sleep 1
done
docker exec "$name" psql -X -v ON_ERROR_STOP=1 -U postgres -d arc_flt102_test -c "CREATE EXTENSION vector WITH SCHEMA public VERSION '0.8.7'; REVOKE CREATE ON SCHEMA public FROM PUBLIC;"
docker inspect "$name" --format 'network={{.HostConfig.NetworkMode}}'
docker exec -e PROJECT_ONE_REPOSITORY_ENGINE=postgresql -e PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE=/probe/pg-dsn -e "PROJECT_ONE_REPOSITORY_POSTGRES_SCHEMA=m20_repository_$(openssl rand -hex 16)" -e OWNSOLID_TEST_AGENT_BINARY=/probe/agent-amd64 "$name" /probe/enrollment.test -test.run '^TestActualAgentEnrollmentCommand$' -test.v > "$scratch/enrollment.log" 2>&1 || { tail -42 "$scratch/enrollment.log"; exit 1; }
tail -35 "$scratch/enrollment.log"
