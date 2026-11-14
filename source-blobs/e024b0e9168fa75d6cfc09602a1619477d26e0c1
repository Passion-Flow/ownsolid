#!/bin/sh
set -eu
umask 077
cd /fixture
export PGUSER=postgres
root=/repo/services/control/migrations/postgresql
candidate=/repo/services/control/internal/modules/audit/testdata/000058_audit_chain_head_scope.up.sql
export_sql=/repo/tools/audit-verify/export.sql

sql() {
	db=$1
	shift
	psql -XqAt -v ON_ERROR_STOP=1 -d "$db" "$@"
}

create_empty() {
	createdb "$1"
}

init() {
	create_empty "$1"
	sql "$1" -c 'CREATE EXTENSION vector'
	sql "$1" -c 'CREATE TABLE schema_migrations(version integer PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP)'
}

apply() {
	name=$1
	first=$2
	last=$3
	(
		set -eu
		printf 'BEGIN;\n'
		for version in $(seq "$first" "$last"); do
			if [ "$version" -eq 58 ]; then
				file=$candidate
			else
				prefix=$(printf '%06d' "$version")
				set -- "$root/$prefix"_*.up.sql
				test "$#" -eq 1 && test -f "$1"
				file=$1
			fi
			sha=$(sha256sum "$file" | cut -d ' ' -f 1)
			printf '\\i %s\nINSERT INTO schema_migrations(version,checksum) VALUES(%s,'\''%s'\'');\n' "$file" "$version" "$sha"
		done
		printf 'COMMIT;\n'
	) > apply.sql
	sql "$name" -f apply.sql > /dev/null
}

fingerprint() {
	sql "$1" -f /fixture/catalog.sql
}

expect57() {
	got=$(fingerprint "$1")
	printf 'catalog 57 %s %s\n' "$1" "$got"
	test "$got" = "$2"
}

at58() {
	test "$(sql "$1" -c "SELECT count(*) FROM schema_migrations")" = 58
	test "$(sql "$1" -c "SELECT site_id||'|'||cell_id FROM audit_chain_head WHERE singleton")" = 'default|default'
	test "$(sql "$1" -c "SELECT count(*) FROM information_schema.columns
	 WHERE table_schema=current_schema() AND table_name='audit_chain_head' AND is_nullable='NO'
	 AND ((column_name='site_id' AND column_default='local_site_id()')
	  OR (column_name='cell_id' AND column_default='local_cell_id()'))")" = 2
	printf 'catalog 58 %s %s\n' "$1" "$(fingerprint "$1")"
}

dumpdb() {
	pg_dump --format=custom --file "$2" "$1"
}

restore() {
	create_empty "$1"
	pg_restore --exit-on-error --dbname "$1" "$2"
}

event() {
	sql "$1" -c "INSERT INTO audit_events(id,occurred_at,audience,action,outcome,request_id)
	 VALUES ('00000000-0000-4000-8000-$2','2026-11-09 10:00:00.123456+00','system','fixture.scope','success','00000000-0000-4000-8000-$2')"
}

head_state() {
	sql "$1" -c "SELECT chain_position||'|'||coalesce(scoped_from::text,'')||'|'||encode(hash,'hex')
	 FROM audit_chain_head WHERE singleton"
}

retained() {
	test "$(sql "$1" -c "SELECT count(*) FROM audit_events WHERE chain_position IS NULL")" = 1
	test "$(head_state "$1")" = "$old_head"
	sql "$1" -f "$export_sql" > export.jsonl
	cmp prefix.jsonl export.jsonl
	./audit-verify --file export.jsonl --expected-last-hash "$checkpoint" > /dev/null
	if sql "$1" -c "INSERT INTO audit_events(id,occurred_at,audience,action,outcome,request_id,site_id,cell_id)
	   VALUES ('00000000-0000-4000-8000-000000000100',clock_timestamp(),'system','fixture.foreign','success','00000000-0000-4000-8000-000000000100','default','other')" 2>refusal.log; then
		echo "foreign scope was accepted" >&2
		exit 1
	fi
	grep -q 'AUDIT_CHAIN_CELL_SCOPE_MISMATCH' refusal.log
	test "$(head_state "$1")" = "$old_head"
	printf 'retained checkpoint and scope refusal %s\n' "$1"
}

concurrent() {
	pids=
	for n in $(seq 10 17); do
		event "$1" "$(printf '%012d' "$n")" &
		pids="$pids $!"
	done
	for pid in $pids; do
		wait "$pid"
	done
	sql "$1" -f "$export_sql" > export.jsonl
	head -n 2 export.jsonl | cmp prefix.jsonl -
	tail -n +3 export.jsonl > suffix.jsonl
	test "$(wc -l < suffix.jsonl)" = 8
	tip=$(head_state "$1" | cut -d '|' -f 3)
	./audit-verify --file suffix.jsonl --previous-hash "$checkpoint" --expected-last-hash "$tip"
	test "$(head_state "$1" | cut -d '|' -f 1)" = 10
	printf 'eight concurrent appends verified %s\n' "$1"
}

fresh57=8454a9e3039dfb669a532aaabd26419661c404d683cd9abfa45eb4ee2b25b4a4
restored55=4ae1c70a752af0056647e275114e2a16e8831bbcf875e6018ad3a7754b11efcc
restored57=c160dfad296f6a93ec88aa88cc63bd232cbfd5dca0518c22d390961b0593e7be
printf 'PostgreSQL %s, pgvector ' "$(sql postgres -c 'SHOW server_version')"
sql audit_scope_test -c "SELECT extversion FROM pg_extension WHERE extname='vector'"

init fresh58_test
apply fresh58_test 1 58
at58 fresh58_test
test "$(head_state fresh58_test | cut -d '|' -f 1)" = 0
event fresh58_test 000000000001
sql fresh58_test -f "$export_sql" > fresh.jsonl
./audit-verify --file fresh.jsonl --expected-last-hash "$(head_state fresh58_test | cut -d '|' -f 3)"

init native_test
apply native_test 1 52
event native_test 000000000001
apply native_test 53 53
event native_test 000000000002
apply native_test 54 55
event native_test 000000000003
sql native_test -f "$export_sql" > prefix.jsonl
test "$(wc -l < prefix.jsonl)" = 2
old_head=$(head_state native_test)
checkpoint=$(printf '%s\n' "$old_head" | cut -d '|' -f 3)
./audit-verify --file prefix.jsonl --expected-last-hash "$checkpoint" > /dev/null

dumpdb native_test schema55.dump
apply native_test 56 56
dumpdb native_test schema56.dump
apply native_test 57 57
expect57 native_test "$fresh57"
dumpdb native_test schema57.dump
apply native_test 58 58
at58 native_test
retained native_test
dumpdb native_test schema58.dump

restore restored55_test schema55.dump
apply restored55_test 56 57
expect57 restored55_test "$restored55"
apply restored55_test 58 58
at58 restored55_test
retained restored55_test
concurrent restored55_test

restore restored56_test schema56.dump
apply restored56_test 57 57
expect57 restored56_test "$restored57"
apply restored56_test 58 58
at58 restored56_test
retained restored56_test

restore restored57_test schema57.dump
expect57 restored57_test "$restored57"
apply restored57_test 58 58
at58 restored57_test
retained restored57_test

restore restored58_test schema58.dump
at58 restored58_test
retained restored58_test
concurrent restored58_test
