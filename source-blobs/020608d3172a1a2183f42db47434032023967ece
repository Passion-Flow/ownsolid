"""Try one nonblocking native CLI build admission; never queue under a lock."""
import datetime,fcntl,hashlib,json,os
from pathlib import Path
import signal,stat,subprocess,sys,time
os.umask(0o077)
source='5055d774ec6941df008f21f7e69abd48bad3fab0'
result=Path('/root/lane-rel-tools/workspace-operations/native-build-admission-5055d774.json')
assert not result.exists(), 'One-shot admission record already exists'
held=[]
proof={'source':source,'utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'build_started':False}
def resources():
 mem=int(next(line.split()[1] for line in Path('/proc/meminfo').read_text().splitlines() if line.startswith('MemFree:')))//1024
 disk=os.statvfs('/root');return {'mem_free_mib':mem,'disk_free_mib':disk.f_bavail*disk.f_frsize//1048576}
def finish(code,reason):
 proof.update(exit=code,reason=reason);result.write_text(json.dumps(proof,indent=2)+'\n');print(json.dumps(proof),flush=True);raise SystemExit(code)
proof['before']=resources()
if proof['before']['mem_free_mib']<2048 or proof['before']['disk_free_mib']<15360:finish(75,'Resource admission unavailable; no worktree or compiler started')
for path in ['/run/lock/ownsolid-heavy.lock','/root/lane-sci-tools/heavy-test.lock','/root/lane-rel-tools/heavy-test.lock']:
 fd=os.open(path,os.O_RDWR|os.O_NOFOLLOW);held.append(fd);assert stat.S_ISREG(os.fstat(fd).st_mode)
 try:fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
 except BlockingIOError:finish(75,'Busy heavy-test lock: '+path+'; no worktree or compiler started')
proof['after_locks']=resources()
if proof['after_locks']['mem_free_mib']<2048 or proof['after_locks']['disk_free_mib']<15360:finish(75,'Resource admission changed; no worktree or compiler started')
work=Path('/root/lane-rel-client-build-5055d774');artifact=Path('/root/lane-rel-tools/workspace-operations/native-client-5055d774')
assert not work.exists() and not artifact.exists()
subprocess.run(['git','-C','/root/ownsolid','worktree','add','--detach',str(work),source],check=True,capture_output=True)
assert subprocess.check_output(['git','-C',str(work),'rev-parse','HEAD'],text=True).strip()==source
assert not subprocess.check_output(['git','-C',str(work),'status','--porcelain'],text=True)
artifact.mkdir(mode=0o700)
command=['bash','--noprofile','--norc','-c','set -e\nsource /root/lane-rel-tools/env\nexport GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local GOMAXPROCS=2 GOFLAGS=-p=1 CGO_ENABLED=0 GOOS=linux GOARCH=amd64\ntest "$(go version)" = "go version go1.27.3 linux/amd64" || exit 1\nunshare --net -- go build -buildvcs=true -trimpath -o /root/lane-rel-tools/workspace-operations/native-client-5055d774/ownsolid ./tools/cli/cmd/ownsolid\ngo version -m /root/lane-rel-tools/workspace-operations/native-client-5055d774/ownsolid > /root/lane-rel-tools/workspace-operations/native-client-5055d774/build-metadata.txt']
proof['build_started']=True
with (artifact/'build.log').open('wb') as log:
 child=subprocess.Popen(command,cwd=work,stdout=log,stderr=subprocess.STDOUT,start_new_session=True)
 try: code=child.wait(timeout=180)
 except subprocess.TimeoutExpired:
  os.killpg(child.pid,signal.SIGTERM)
  try:child.wait(timeout=5)
  except subprocess.TimeoutExpired:os.killpg(child.pid,signal.SIGKILL);child.wait(timeout=5)
  code=124
proof['build_exit']=code
if code:finish(code,'Native CLI build failed; no artifact admission')
target=artifact/'ownsolid';os.chmod(target,0o500);sha=hashlib.sha256(target.read_bytes()).hexdigest()
meta=(artifact/'build-metadata.txt').read_text()
assert '\tvcs.revision='+source in meta and '\tvcs.modified=false' in meta
assert '\tCGO_ENABLED=0' in meta
(artifact/'build-metadata.txt').write_text(meta)
manifest={'format_version':1,'source_revision':source,'source_modified':False,'binary_sha256':sha,'binary_bytes':target.stat().st_size,'os':'linux','arch':'amd64','build_host':'linux/amd64','go_version':'go version go1.27.3 linux/amd64','flags':['-buildvcs=true','-trimpath','CGO_ENABLED=0','GOFLAGS=-p=1'],'network_scope':'unshare --net with GOPROXY/GOSUMDB off','native_linux_startup_run':False,'installed_acceptance':False}
(artifact/'client-manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
proof.update(binary=str(target),binary_sha256=sha,binary_bytes=target.stat().st_size)
finish(0,'Native offline CLI build passed; startup and installed acceptance remain separate')
