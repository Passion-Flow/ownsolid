#!/bin/bash
set -euo pipefail
umask 077
proof="$(cd "$(dirname "$0")" && pwd -P)"
model_root="$1"
test -d "$model_root"
exec 9>/run/lock/ownsolid-heavy.lock
flock -n 9 || exit 75
python3 - <<'PY'
import os,pathlib
available=next(int(line.split()[1])*1024 for line in pathlib.Path('/proc/meminfo').read_text().splitlines() if line.startswith('MemAvailable:'))
shared=os.statvfs('/dev/shm')
disk=os.statvfs('/root')
if available<12*1024**3 or shared.f_bavail*shared.f_frsize<7*1024**3 or disk.f_bavail*disk.f_frsize<15*1024**3:
 raise SystemExit('Insufficient isolated acceptance resources')
import re,subprocess
for line in subprocess.check_output(['ps','-Ao','pid=,comm=,args='],text=True).splitlines():
 fields=line.strip().split(None,2)
 if len(fields)<3: continue
 pid,comm,args=fields
 name=os.path.basename(comm)
 pattern={'go':r'\bgo (test|build)\b','cargo':r'\bcargo (test|build|clippy)\b','docker':r'\bdocker (run|build|compose|buildx)\b','node':r'playwright.*\btest\b'}.get(name)
 if name.endswith('.test') or (pattern and re.search(pattern,args)):
  raise SystemExit('Another heavy process is active: '+pid+' '+name)
print('Memory, shared scratch and disk resource preflight passed')
PY
cd "$proof"
python3 - <<'PY'
import hashlib,pathlib
for line in pathlib.Path('inputs.sha256').read_text().splitlines():
 digest,name=line.split(None,1)
 path=pathlib.Path(name).name
 if path not in ('models.test','models-http.test','deployments.test','source.tar.gz'):
  raise SystemExit('Unexpected input')
 h=hashlib.sha256()
 with open(path,'rb') as source:
  for part in iter(lambda:source.read(1024*1024),b''): h.update(part)
 if h.hexdigest()!=digest: raise SystemExit('Input digest mismatch')
print('Compiled acceptance inputs match the measured source package')
PY
mkdir source
tar -xzf source.tar.gz -C source
scratch="$(mktemp -d /dev/shm/ownsolid-lane-m-acceptance.XXXXXXXX)"
mkdir "$scratch/tmp" "$scratch/state"
container="ownsolid-lane-m-$(basename "$proof" | tr -cd 'a-zA-Z0-9')"
created=false
cleanup() {
 code=$?
 trap - EXIT
 if test "$code" = 0; then
  if test "$created" = true; then docker rm -f "$container" > "$proof/container-cleanup.log"; fi
  python3 - "$scratch" "$proof" <<'PY'
import pathlib,shutil,sys
p=pathlib.Path(sys.argv[1])
if p.parent!=pathlib.Path('/dev/shm') or not p.name.startswith('ownsolid-lane-m-acceptance.'):
 raise SystemExit('Invalid cleanup scope')
shutil.rmtree(p)
private=pathlib.Path(sys.argv[2])/'private'
for name in ('dsn','password'): (private/name).unlink()
private.rmdir()
PY
 else
  printf '%s\n' "$scratch" > "$proof/retained-scratch"
  printf '%s\n' "$container" > "$proof/retained-container"
 fi
 printf '%s\n' "$code" > "$proof/acceptance-exit-code"
 exit "$code"
}
trap cleanup EXIT
mkdir private
python3 - <<'PY'
import pathlib,secrets
secret=secrets.token_hex(32)
for name,data in [('password',secret),('dsn','postgresql://postgres:'+secret+'@127.0.0.1/lane_m_model_test?sslmode=disable')]:
 p=pathlib.Path('private')/name
 p.write_text(data+'\n')
 p.chmod(0o600)
PY
chmod 0755 models.test models-http.test deployments.test
docker run --pull never --rm -d --name "$container" --network none --cpus 2 --memory 8g \
 -e POSTGRES_PASSWORD_FILE=/fixture/private/password -e POSTGRES_DB=lane_m_model_test \
 --mount "type=bind,source=$proof,target=/fixture,readonly" \
 --mount "type=bind,source=$proof/source,target=/repo,readonly" \
 --mount "type=bind,source=$model_root,target=/approved,readonly" \
 --mount "type=bind,source=$scratch,target=/scratch" \
 sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47 > "$proof/container-id"
created=true
ready=false
for attempt in $(seq 1 45); do
 if docker exec "$container" pg_isready -U postgres -d lane_m_model_test >/dev/null 2>&1; then ready=true; break; fi
 sleep 1
done
test "$ready" = true
docker inspect "$container" --format '{{.HostConfig.NetworkMode}} {{.Image}} {{.State.Status}}' > "$proof/isolation.log"
docker exec "$container" psql -X -U postgres -d lane_m_model_test -v ON_ERROR_STOP=1 -c "CREATE EXTENSION vector WITH SCHEMA public VERSION '0.8.7'; REVOKE CREATE ON SCHEMA public FROM PUBLIC; SELECT version();" > "$proof/database.log"
run_test() {
 label="$1"
 shift
 printf '%s\n' "$label" > "$proof/stage"
 docker exec -e TMPDIR=/scratch/tmp/ -e GOMAXPROCS=2 \
  -e PROJECT_ONE_TEST_DSN_FILE=/fixture/private/dsn \
  -e PROJECT_ONE_REPOSITORY_ENGINE=postgresql \
  -e PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE=/fixture/private/dsn \
  -e "PROJECT_ONE_REPOSITORY_POSTGRES_SCHEMA=m20_repository_$(openssl rand -hex 16)" \
  -e PROJECT_ONE_REPOSITORY_MODEL_FILES_DIRECTORY=/approved \
  -e PROJECT_ONE_STATE_ROOT=/scratch/state \
  -e OWNSOLID_TEST_MODEL_FILE_SAFETY_ARCHIVE=/scratch/state/dependencies/models/qwen3-0.6b/c1899de289a04d12100db370d81485cdf75e47ca/qwen3-0.6b.tar \
  "$container" "$@" -test.count=1 -test.v -test.timeout=15m > "$proof/$label.log" 2>&1
 if test "$label" != publication && grep -q -- '--- SKIP' "$proof/$label.log"; then return 1; fi
 if test "$label" != publication && grep -q 'warning: no tests to run' "$proof/$label.log"; then return 1; fi
 grep -q '^--- PASS: Test' "$proof/$label.log"
 tail -3 "$proof/$label.log"
}
run_test expiry /fixture/models.test -test.run '^TestActualAbandonedImportExpiry'
run_test https /bin/sh -c 'cd /repo/services/control/internal/modules/models/http && exec /fixture/models-http.test "$@"' sh -test.run '^Test(ModelImportHTTPS|StrictModelImportHTTP|ModelImportHTTPNondefault)'
run_test tls /bin/sh -c 'cd /repo/services/control/internal/modules/deployments && exec /fixture/deployments.test "$@"' sh -test.run '^TestRealTLSInferenceAPICSRFSchemaAndLeasedModelTransfer$'
run_test borrowed /fixture/models.test -test.run '^TestActualBorrowedModelAccess$/(base_range_hash_and_owned_descriptor|derived_metadata_file_mode_link_missing_refusal|cancelled_IO_returns_no_download)$'
printf '%s\n' archive-pack > "$proof/stage"
python3 - "$model_root" "$scratch" <<'PY'
import hashlib,json,pathlib,sys,tarfile
root=pathlib.Path(sys.argv[1])
scratch=pathlib.Path(sys.argv[2])
catalog=json.loads(pathlib.Path('source/api/agent/v1/model-catalog.json').read_text())
archive=scratch/'state'/'dependencies'/'models'/catalog['model_key']/catalog['revision']/'qwen3-0.6b.tar'
archive.parent.mkdir(parents=True)
class MeasuredReader:
 def __init__(self,source): self.source=source; self.digest=hashlib.sha256()
 def read(self,size=-1):
  part=self.source.read(size)
  self.digest.update(part)
  return part
with tarfile.open(archive,'w',format=tarfile.USTAR_FORMAT) as writer:
 for item in catalog['files']:
  source=root/item['path']
  if source.is_symlink() or source.stat().st_size!=item['size_bytes']:
   raise SystemExit('Pinned model input refused '+item['path'])
  header=tarfile.TarInfo(item['path'])
  header.mode=0o644
  header.size=item['size_bytes']
  with source.open('rb') as stream:
   measured=MeasuredReader(stream)
   writer.addfile(header,measured)
   if measured.digest.hexdigest()!=item['sha256']: raise SystemExit('Pinned model digest mismatch '+item['path'])
  print('Verified original model input',item['path'],item['size_bytes'])
digest=hashlib.sha256()
with archive.open('rb') as stream:
 for part in iter(lambda:stream.read(1024*1024),b''): digest.update(part)
receipt={'size_bytes':archive.stat().st_size,'sha256':digest.hexdigest(),'catalogue':catalog['revision'],'source':'Existing read-only approved model files on the test server'}
pathlib.Path('archive-input.json').write_text(json.dumps(receipt,indent=2)+'\n')
print('Prepared and measured the complete approved archive')
PY
run_test archive /fixture/models.test -test.run '^TestActualModelImportFileSafety$'
run_test publication /fixture/models.test -test.run '^TestActualModelImportPublicationAuditRollbackAndFreshWorkerRecovery$' -test.skip '/snapshot-and-retirement$'
printf '%s\n' passed > "$proof/stage"
