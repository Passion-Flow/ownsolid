#!/usr/bin/env bash
set -euo pipefail
root=$1
pg=pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47
id=$(docker run -d --pull never --network none --cpus 0.5 --memory 256m \
  --tmpfs /var/lib/postgresql:rw,size=128m -e POSTGRES_HOST_AUTH_METHOD=trust "$pg" postgres -c "listen_addresses=")
trap 'docker rm -fv "$id" >/dev/null' EXIT
for _ in {1..40}; do
  if [[ $(docker exec "$id" cat /proc/1/comm) == postgres ]] && docker exec "$id" pg_isready -q -U postgres; then break; fi
  sleep 0.25
done
docker inspect "$id" --format 'network={{.HostConfig.NetworkMode}}'
psql() { docker exec -i "$id" psql -XqAt -U postgres -d postgres -v ON_ERROR_STOP=1 "$@"; }
psql <<'SQL'
CREATE TABLE rack_unit (
 tenant int NOT NULL,
 rack int NOT NULL,
 unit int NOT NULL CHECK (unit BETWEEN 1 AND 48),
 face text NOT NULL CHECK (face IN ('front','rear')),
 node int NOT NULL,
 PRIMARY KEY (tenant,rack,unit,face)
);
INSERT INTO rack_unit VALUES (1,1,1,'front',1), (1,1,2,'front',1);
SQL
psql >"$root/flt101-holder.log" 2>&1 <<'SQL' &
SET application_name = 'rack-holder';
BEGIN;
INSERT INTO rack_unit VALUES (1,2,4,'front',2),(1,2,4,'rear',2);
SELECT pg_sleep(4);
COMMIT;
SQL
holder=$!
wait_for() {
  local app=$1 event=$2
  local ready=
  for _ in {1..24}; do
    ready=$(psql -c "SELECT count(*) FROM pg_stat_activity WHERE application_name='$app' AND wait_event_type='$event'")
    if [[ $ready == 1 ]]; then return 0; fi
    sleep 0.1
  done
  return 1
}
wait_for rack-holder Timeout
psql >"$root/flt101-move.log" 2>&1 <<'SQL' &
SET application_name = 'rack-move';
BEGIN;
DELETE FROM rack_unit WHERE tenant=1 AND rack=1 AND node=1;
INSERT INTO rack_unit VALUES (1,2,4,'front',1),(1,2,5,'front',1);
COMMIT;
SQL
move=$!
wait_for rack-move Lock
wait "$holder"
if wait "$move"; then printf 'Concurrent overlap unexpectedly committed\n' >&2; exit 1; fi
grep -q 'duplicate key value violates unique constraint' "$root/flt101-move.log"
test "$(psql -c 'SELECT count(*) FROM rack_unit WHERE tenant=1 AND rack=1 AND node=1')" = 2
test "$(psql -c 'SELECT count(*) FROM rack_unit WHERE tenant=1 AND rack=2 AND node=1')" = 0
psql <<'SQL'
INSERT INTO rack_unit VALUES
 (2,2,4,'front',3),
 (1,3,4,'front',4),
 (1,2,5,'front',5),
 (1,2,5,'rear',6);
SQL
test "$(psql -c 'SELECT count(*) FROM rack_unit')" = 8
printf '%s\n' 'PASS concurrent overlap, atomic rollback, rack and tenant separation, independent faces'
