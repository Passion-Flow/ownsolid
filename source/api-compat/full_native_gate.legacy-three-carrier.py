import os,fcntl,stat,subprocess,json,time,signal,sys,hashlib
from pathlib import Path
os.umask(0o077)
r=Path('/root/lane-rel-tools/api-compat');w=Path('/root/lane-rel-api-compat');source='89c1610e8de03ea8151153f3cbd740e34ac6cb32'
locks=[Path('/run/lock/ownsolid-heavy.lock'),Path('/root/lane-sci-tools/heavy-test.lock'),Path('/root/lane-rel-tools/heavy-test.lock')]
proof={'source':source,'base':'8b17a2954260e517b434906e95740a39ecdd4a9a','host':'linux/amd64','check':False,'build':False,'whole_make_test':False,'cli_b967d7c8_qualified':False,'installed_acceptance':False,'locks':[]}
fds=[];child=None;started=time.monotonic();exit_code=75
audit=w/'tools/audit-verify/audit-verify';preexisting=audit.exists()
def resources():
 m=int(next(x.split()[1] for x in Path('/proc/meminfo').read_text().splitlines() if x.startswith('MemFree:')))//1024
 v=os.statvfs('/root');proof['raw_memfree_mib']=m;proof['disk_free_bytes']=v.f_bavail*v.f_frsize
 if m<2048 or v.f_bavail*v.f_frsize<15*1024**3:raise RuntimeError('Resource threshold not met')
def save():
 proof['elapsed_seconds']=round(time.monotonic()-started,3);(r/'native-gate-proof.json').write_text(json.dumps(proof,indent=2)+'\n')
def stop():
 global child
 if child is None:return
 try:os.killpg(child.pid,signal.SIGTERM)
 except ProcessLookupError:pass
 try:child.wait(timeout=5)
 except subprocess.TimeoutExpired:os.killpg(child.pid,signal.SIGKILL);child.wait(timeout=5)
 child=None
def interrupted(signum,frame):raise KeyboardInterrupt('Interrupted')
signal.signal(signal.SIGTERM,interrupted);signal.signal(signal.SIGINT,interrupted)
try:
 resources()
 assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=w,text=True).strip()==source
 assert subprocess.check_output(['git','status','--porcelain'],cwd=w,text=True)==''
 for p in locks:
  fd=os.open(p,os.O_RDWR|os.O_NOFOLLOW|os.O_NONBLOCK);fds.append(fd);s=os.fstat(fd)
  assert stat.S_ISREG(s.st_mode) and s.st_uid==os.geteuid()
  assert (s.st_dev,s.st_ino)==(p.stat().st_dev,p.stat().st_ino)
  proof['attempted_lock']=str(p)
  fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB);proof['locks'].append({'path':str(p),'dev':s.st_dev,'inode':s.st_ino})
 resources();proof['admitted']=True;save()
 env=os.environ.copy();assert not [n for n in env if n.startswith('PROJECT_ONE_') and 'DSN' in n and env[n]]
 bindir=r/'native-gate-bin';bindir.mkdir(exist_ok=True)
 wrapper="""#!/bin/sh
set -eu
if test "${1-}" = install; then
 shift
 exec /root/lane-rel-tools/node/bin/pnpm install --offline "$@"
fi
if test "${1-}" = --recursive || test "${1-}" = -r; then
 shift
 exec /root/lane-rel-tools/node/bin/pnpm --recursive --workspace-concurrency=1 "$@"
fi
exec /root/lane-rel-tools/node/bin/pnpm "$@"
"""
 (bindir/'pnpm').write_text(wrapper);(bindir/'pnpm').chmod(0o700)
 env.update(PATH=str(bindir)+':'+env['PATH'],GOPROXY='off',GOSUMDB='off',GOTOOLCHAIN='local',GOMAXPROCS='2',GOFLAGS='-p=1',CARGO_BUILD_JOBS='1',CARGO_NET_OFFLINE='true',CARGO_TARGET_DIR=str(r/'native-rust-target'),NODE_OPTIONS='--max-old-space-size=2048',PYTHONDONTWRITEBYTECODE='1',MAKEFLAGS='-j1')
 for target in ['web-install','check','build']:
  print('NATIVE_GATE_TARGET',target,flush=True)
  child=subprocess.Popen(['unshare','--net','bash','--noprofile','--norc','-euc','ip link set lo up\nexec make '+target],cwd=w,env=env,stdin=subprocess.DEVNULL,pass_fds=tuple(fds),start_new_session=True)
  try:code=child.wait(timeout=max(1,1200-(time.monotonic()-started)))
  except BaseException:stop();raise
  child=None;proof[target+'_exit']=code;proof[target]=code==0;save()
  if code:raise RuntimeError(target+' exited '+str(code))
 if not preexisting and audit.exists():
  assert stat.S_ISREG(audit.lstat().st_mode) and audit.stat().st_uid==os.geteuid();audit.unlink()
 proof['source_clean_after']=subprocess.check_output(['git','status','--porcelain'],cwd=w,text=True)==''
 assert proof['source_clean_after'];proof['passed']=True;exit_code=0

except BlockingIOError:
 proof['admitted']=False;proof['failure']='Shared file lock busy before any target started'
except BaseException as error:
 proof['passed']=False;proof['failure']=type(error).__name__+': '+str(error)
 if proof.get('admitted'):exit_code=1
finally:
 stop()
 if not preexisting and audit.exists():
  try:
   assert stat.S_ISREG(audit.lstat().st_mode) and audit.stat().st_uid==os.geteuid();audit.unlink()
  except BaseException as error:proof['cleanup_failure']=type(error).__name__+': '+str(error);proof['passed']=False;exit_code=1
 proof['source_clean_after']=subprocess.check_output(['git','status','--porcelain'],cwd=w,text=True)==''
 save()
 for fd in reversed(fds):os.close(fd)
print('NATIVE_GATE_RESULT',json.dumps(proof),flush=True)
sys.exit(exit_code)
