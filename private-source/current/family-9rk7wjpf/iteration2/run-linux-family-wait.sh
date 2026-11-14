#!/bin/bash
set -euo pipefail
umask 077
proof="$(cd "$(dirname "$0")" && pwd -P)"
exec 9>>/run/lock/ownsolid-heavy.lock
flock -w 45 9 || exit 75
cd "$proof"
python3 - <<'PY'
import os,pathlib,re,subprocess
available=next(int(line.split()[1])*1024 for line in pathlib.Path('/proc/meminfo').read_text().splitlines() if line.startswith('MemAvailable:'))
disk=os.statvfs(str(pathlib.Path.cwd()))
if available<2*1024**3 or disk.f_bavail*disk.f_frsize<15*1024**3:
 raise SystemExit('Insufficient resources for the owned PostgreSQL test')
for line in subprocess.check_output(['ps','-Ao','pid=,comm=,args='],text=True).splitlines():
 fields=line.strip().split(None,2)
 if len(fields)!=3: continue
 pid,comm,args=fields
 name=os.path.basename(comm)
 pattern={'go':r'\bgo (test|build)\b','cargo':r'\bcargo (test|build|clippy)\b','docker':r'\bdocker (run|build|compose|buildx)\b'}.get(name)
 if name.endswith('.test') or pattern and re.search(pattern,args):
  raise SystemExit('Another heavy process is still active')
print('CPU lock and resources admitted')
PY
python3 - <<'PY' > source-verification.log
import hashlib,json,pathlib
p=pathlib.Path.cwd()
s=json.loads((p/'source-delta.json').read_text())
if (p/'source-ready').read_text().strip()!=s['target_commit']:
 raise SystemExit('Source reconstruction target mismatch')
if hashlib.sha256(b''.join((p/x['name']).read_bytes() for x in json.loads((p/'source-parts.json').read_text()))).hexdigest()!=s['delta_sha256']:
 raise SystemExit('Source reconstruction delta changed')
print('Verified reconstructed source',s['target_commit'])
PY
export GOMAXPROCS=2 GOPROXY=off GOSUMDB=off GOMODCACHE=/root/lane-rel-tools/gomod GOCACHE=/root/lane-rel-tools/gocache
CGO_ENABLED=0 /root/lane-rel-tools/go/bin/go -C "$proof/source/services/control" test -p=2 -tags repository_integration -c -o "$proof/models.test" ./internal/modules/models > compile.log 2>&1
sha256sum models.test > compiled-binary.sha256
mkdir private tmp
python3 - <<'PY'
import pathlib,secrets
secret=secrets.token_hex(32)
for name,data in [('password',secret),('dsn','postgresql://postgres:'+secret+'@127.0.0.1/lane_m_model_family_test?sslmode=disable')]:
 p=pathlib.Path('private')/name
 p.write_text(data+'\n')
 p.chmod(0o600)
PY
container="lane-m-family-$(basename "$proof" | tr -cd 'a-zA-Z0-9')"
created=false
cleanup() {
 code=$?
 trap - EXIT
 if test "$code" = 0; then
  if test "$created" = true; then docker rm -f "$container" > "$proof/container-cleanup.log"; fi
  python3 - "$proof" <<'PY'
import pathlib,shutil,sys
proof=pathlib.Path(sys.argv[1])
shutil.rmtree(proof/'tmp')
for name in ('dsn','password'): (proof/'private'/name).unlink()
(proof/'private').rmdir()
PY
 else
  printf '%s\n' "$container" > "$proof/retained-container"
 fi
 printf '%s\n' "$code" > "$proof/test-exit-code"
 exit "$code"
}
trap cleanup EXIT
chmod 0755 models.test
docker run --pull never --rm -d --name "$container" --network none --cpus 2 --memory 768m \
 -e POSTGRES_PASSWORD_FILE=/fixture/private/password -e POSTGRES_DB=lane_m_model_family_test \
 --mount "type=bind,source=$proof,target=/fixture,readonly" \
 --mount "type=bind,source=$proof/source,target=/repo,readonly" \
 --mount "type=bind,source=$proof/tmp,target=/testtmp" \
 --mount "type=bind,source=/root/lane-b/runner/model,target=/base,readonly" \
 sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47 > container-id
created=true
ready=false
for attempt in $(seq 1 45); do
 if docker exec "$container" pg_isready -U postgres -d lane_m_model_family_test >/dev/null 2>&1; then ready=true; break; fi
 sleep 1
done
test "$ready" = true
docker exec "$container" psql -X -U postgres -d lane_m_model_family_test -v ON_ERROR_STOP=1 -c "CREATE EXTENSION vector WITH SCHEMA public VERSION '0.8.7'; REVOKE CREATE ON SCHEMA public FROM PUBLIC; SELECT version();" > database.log
docker inspect "$container" --format '{{.HostConfig.NetworkMode}} {{.Image}} {{.State.Status}}' > isolation.log
docker exec -w /repo/services/control/internal/modules/models \
 -e PROJECT_ONE_REPOSITORY_ENGINE=postgresql \
 -e PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE=/fixture/private/dsn \
 -e PROJECT_ONE_REPOSITORY_POSTGRES_SCHEMA=m20_repository_$(cat /proc/sys/kernel/random/uuid | tr -d '-') \
 -e PROJECT_ONE_REPOSITORY_MODEL_FILES_DIRECTORY=/base \
 -e PROJECT_ONE_TEST_DSN_FILE=/fixture/private/dsn -e TMPDIR=/testtmp/ -e GOMAXPROCS=2 \
 "$container" /fixture/models.test -test.run '^TestActualModelFamily' -test.count=1 -test.v -test.timeout=8m > family.log 2>&1
if grep -q -- '--- SKIP' family.log; then exit 1; fi
test "$(grep -c '^--- PASS: TestActualModelFamily' family.log)" = 5
tail -8 family.log
