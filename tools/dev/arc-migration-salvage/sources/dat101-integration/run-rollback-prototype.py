from pathlib import Path
import fcntl,os,signal,stat,subprocess,tempfile,hashlib,json
base=Path(__file__).resolve().parent;repo=base.parent.parent/'lane-arc-dat101';os.chdir(repo)
carrier=Path('/private/tmp/ownsolid-workstation-full-gate.lock');fd=os.open(carrier,os.O_RDWR|os.O_NOFOLLOW);info=os.fstat(fd)
assert stat.S_ISREG(info.st_mode) and info.st_uid==os.getuid()
try:fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
except BlockingIOError:os.close(fd);raise SystemExit(75)
child=None
try:
 assert (info.st_dev,info.st_ino)==(carrier.lstat().st_dev,carrier.lstat().st_ino)
 assert subprocess.run(['pgrep','-x','make|cargo|rustc|compile|link'],capture_output=True).returncode!=0
 disk=os.statvfs(repo);assert disk.f_bavail*disk.f_frsize>15*1024**3
 run=Path(tempfile.mkdtemp(prefix='rollback-prototype.',dir=str(base)))
 (base/'latest-rollback-prototype').write_text(str(run)+'\n');(run/'runner-pid').write_text(str(os.getpid())+'\n')
 (run/'input').write_bytes(subprocess.check_output(['git','rev-parse','HEAD','HEAD^{tree}','origin/main']))
 (run/'resource-preflight').write_bytes(subprocess.check_output(['df','-h','/'])+subprocess.check_output(['memory_pressure','-Q']))
 files={'up.sql':repo/'services/control/internal/modules/datasets/testdata/draft_import_candidate.up.sql','original.down.sql':base/'original-fixture.down.sql','proposed.down.sql':base/'proposed-formal.down.sql'}
 for name,source in files.items():(run/name).write_bytes(source.read_bytes())
 (run/'sql-sha256.json').write_text(json.dumps({name:hashlib.sha256((run/name).read_bytes()).hexdigest() for name in files},indent=2)+'\n')
 old=(base.parent/'net102-startup/focused-postgres.sh').read_text()
 script=old.replace('arc-net102-','arc-datproto-').replace('arc-net102-source','arc-datproto-rollback').replace('registry_source_test','draft_rollback_test')
 script=script.replace('GOOS=linux GOARCH=$arch CGO_ENABLED=0 go -C services/control test -c -o "$run/server.test" ./cmd/server', 'GOOS=linux GOARCH=$arch CGO_ENABLED=0 go -C services/control test -c -o "$run/rollback.test" "$(dirname "$run")/rollback_probe_test.go"')
 begin=script.index('docker exec -e PROJECT_ONE_TEST_DSN_FILE=')
 script=script[:begin]+r"""run_probe() {
 docker exec -e PROJECT_ONE_TEST_DSN_FILE=/fixture/test-dsn -e ARC_DRAFT_UP_FILE=/fixture/up.sql -e ARC_DRAFT_ORIGINAL_DOWN_FILE=/fixture/original.down.sql -e ARC_DRAFT_SAFE_DOWN_FILE=/fixture/proposed.down.sql "$name" /fixture/rollback.test -test.v -test.timeout=90s -test.run "$1"
}
red=0
run_probe '^TestOriginalDownRetainsReceipt$' > "$run/red.log" 2>&1 || red=$?
[[ $red == 1 ]]
grep -F 'original down removed a retained import receipt; outer rollback preserved the reproduction data' "$run/red.log" >/dev/null
run_probe '^TestGuarded' > "$run/guarded.log" 2>&1
! grep -E -- '--- (SKIP|FAIL)' "$run/guarded.log"
for test in TestGuardedPristineDown TestGuardedRetainedReceipts TestGuardedPriorSnapshot TestGuardedFreshIsolation TestGuardedWaitsForCommittedWriter;do
 grep -F -- "--- PASS: $test" "$run/guarded.log" >/dev/null
done
printf 'Original unsafe down reproduced and rolled back; all guarded rollback cases passed\n'
"""
 script=script.replace('cd "$repo"','export GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local GOMAXPROCS=2 GOFLAGS=-p=1\ncd "$repo"')
 (run/'run.sh').write_text(script)
 subprocess.run(['bash','-n',str(run/'run.sh')],check=True)
 def stop(signum,frame):
  if child is not None and child.poll() is None:
   os.killpg(child.pid,signal.SIGTERM)
   try:child.wait(timeout=15)
   except subprocess.TimeoutExpired:os.killpg(child.pid,signal.SIGKILL);child.wait()
  raise SystemExit(143)
 signal.signal(signal.SIGTERM,stop);signal.signal(signal.SIGINT,stop)
 print('DAT-101 independent rollback prototype '+str(run),flush=True)
 with (run/'run.log').open('wb') as out:
  child=subprocess.Popen(['caffeinate','-i','bash',str(run/'run.sh'),str(run),str(repo)],stdout=out,stderr=subprocess.STDOUT,start_new_session=True)
  code=child.wait(timeout=180)
 (run/'exit').write_text(str(code)+'\n')
 for name in ['run.log','red.log','guarded.log']:
  if (run/name).exists():print(name+'\n'+(run/name).read_text()[-5000:],flush=True)
 raise SystemExit(code)
finally:
 if child is not None and child.poll() is None:
  os.killpg(child.pid,signal.SIGTERM)
  try:child.wait(timeout=15)
  except subprocess.TimeoutExpired:os.killpg(child.pid,signal.SIGKILL);child.wait()
 fcntl.flock(fd,fcntl.LOCK_UN);os.close(fd)
