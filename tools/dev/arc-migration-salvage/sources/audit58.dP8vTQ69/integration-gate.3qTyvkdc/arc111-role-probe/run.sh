#!/usr/bin/env bash
set -euo pipefail
root=$1
image=pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47
id=$(docker run -d --pull never --network none --cpus 0.5 --memory 320m \
  --tmpfs /var/lib/postgresql:rw,size=192m -e POSTGRES_HOST_AUTH_METHOD=trust \
  "$image" postgres -c "listen_addresses=")
trap 'docker rm -fv "$id" >/dev/null' EXIT
for _ in {1..40}; do
  if [[ $(docker exec "$id" cat /proc/1/comm) == postgres ]] && docker exec "$id" pg_isready -q -U postgres; then break; fi
  sleep 0.25
done
docker inspect "$id" --format 'network={{.HostConfig.NetworkMode}}'
sql() {
  local role=$1
  shift
  docker exec -i "$id" psql -XqAt -U "$role" -d postgres -v ON_ERROR_STOP=1 "$@"
}
sql postgres < "$root/install.sql" > "$root/install.log" 2>&1
sql postgres <<'SQL'
CREATE ROLE arc_fixture_control LOGIN NOINHERIT CONNECTION LIMIT 4;
CREATE ROLE arc_fixture_gateway LOGIN NOINHERIT CONNECTION LIMIT 1;
GRANT USAGE ON SCHEMA public TO arc_fixture_control, arc_fixture_gateway;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO arc_fixture_control;
REVOKE INSERT, UPDATE, DELETE ON schema_migrations FROM arc_fixture_control;
REVOKE ALL ON audit_chain_head FROM arc_fixture_control;
GRANT SELECT ON schema_migrations, installation_state, tenants, workspaces, managed_nodes TO arc_fixture_gateway;
SQL
test "$(sql arc_fixture_control < "$root/predicate.sql")" = t
test "$(sql arc_fixture_gateway < "$root/predicate.sql")" = f
test "$(sql arc_fixture_gateway -c 'SELECT count(*) FROM managed_nodes')" = 0
if sql arc_fixture_gateway -c 'DELETE FROM users' > "$root/unrelated-write.log" 2>&1; then exit 1; fi
grep -q 'permission denied' "$root/unrelated-write.log"
if sql arc_fixture_gateway -c 'SELECT * FROM audit_chain_head' > "$root/audit-head.log" 2>&1; then exit 1; fi
grep -q 'permission denied' "$root/audit-head.log"
sql arc_fixture_gateway -c "SET application_name='arc111-holder'; SELECT pg_sleep(4)" > "$root/holder.log" 2>&1 &
holder=$!
for _ in {1..24}; do
  if [[ $(sql postgres -c "SELECT count(*) FROM pg_stat_activity WHERE application_name='arc111-holder' AND wait_event_type='Timeout'") == 1 ]]; then break; fi
  sleep 0.1
done
if sql arc_fixture_gateway -c 'SELECT 1' > "$root/connection-limit.log" 2>&1; then exit 1; fi
grep -q 'too many connections for role' "$root/connection-limit.log"
test "$(sql arc_fixture_control -c 'SELECT 1')" = 1
wait "$holder"
printf '%s\n' 'PASS actual schema-58 serving admission requires broad grants; narrow role denies unrelated access and caps gateway sessions independently'
