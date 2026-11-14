#!/bin/sh
# Called only for the disposable fixture's PostgreSQL service.
set -eu
name=$1
user=$2
database=$3
docker exec -i "$name" psql -X -v ON_ERROR_STOP=1 -U "$user" -d "$database" <<'SQL'
ALTER SYSTEM SET log_line_prefix TO '%m [%p] %a %e ';
ALTER SYSTEM SET log_lock_waits TO 'on';
ALTER SYSTEM SET log_disconnections TO 'on';
ALTER SYSTEM SET log_connections TO 'on';
ALTER SYSTEM SET log_min_error_statement TO 'panic';
ALTER SYSTEM SET log_error_verbosity TO 'terse';
SELECT pg_reload_conf();
SQL
for i in $(seq 1 10); do
    actual=$(docker exec "$name" psql -X -qAt -v ON_ERROR_STOP=1 -U "$user" -d "$database" \
        -c "SELECT concat_ws('|',current_setting('log_line_prefix'),current_setting('log_lock_waits'),current_setting('log_disconnections'),current_setting('log_connections'),current_setting('log_min_error_statement'),current_setting('log_error_verbosity'))")
    if [ "$actual" = '%m [%p] %a %e |on|on|on|panic|terse' ]; then
        printf '%s\n' "$actual"
        exit 0
    fi
    sleep 1
done
exit 1
