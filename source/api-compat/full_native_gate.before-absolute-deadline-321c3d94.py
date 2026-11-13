import os,fcntl,stat,subprocess,json,time,signal,sys,hashlib
from pathlib import Path
os.umask(0o077)
r=Path('/root/lane-rel-tools/api-compat');w=Path('/root/lane-rel-identity-audit-contract');source='9be01cbbf0144a2f2537667834539de31e40f58f'
locks=[Path('/run/lock/ownsolid-heavy.lock'),Path('/root/lane-sci-tools/heavy-test.lock')]
proof={'source':source,'base':'9e32b023a0a3deed6a8653a8878d52c2583fd6cf','host':'linux/amd64','check':False,'build':False,'whole_make_test':False,'cli_b967d7c8_qualified':False,'installed_acceptance':False,'locks':[]}
fds=[];child=None;seen=set();started=time.monotonic();exit_code=1
class AdmissionBusy(Exception):pass
class ResourceRefused(Exception):pass
proof['entry_scope']='Exact original-author audit/SDK/tool and Web projection joint source; web-install/check/build/test. Prepared only; no resource admission is granted by this entry or manifest.'
audit=w/'tools/audit-verify/audit-verify';preexisting=audit.exists()
work_sum=w/'go.work.sum';work_sum_preexisting=os.path.lexists(work_sum)
def cleanup_workspace_sum():
 if proof.get('admitted') and not work_sum_preexisting and os.path.lexists(work_sum):
  metadata=work_sum.lstat()
  assert stat.S_ISREG(metadata.st_mode) and metadata.st_uid==os.geteuid()
  content=work_sum.read_bytes();archive=r/'joint-native-gate-9be01cbb-generated-go.work.sum'
  with archive.open('xb') as output:output.write(content)
  proof['generated_workspace_sum']={'path':'go.work.sum','archived':str(archive),'sha256':hashlib.sha256(content).hexdigest()}
  work_sum.unlink();proof['generated_workspace_sum']['removed_from_source_checkout']=True
def resources():
 values={line.split(':',1)[0]:int(line.split()[1]) for line in Path('/proc/meminfo').read_text().splitlines() if line.startswith(('MemFree:','MemAvailable:'))}
 pressure=next(line for line in Path('/proc/pressure/io').read_text().splitlines() if line.startswith('full '));avg10=float(pressure.split('avg10=',1)[1].split()[0])
 v=os.statvfs('/root');proof['raw_memfree_mib']=values['MemFree']//1024;proof['memavailable_mib']=values['MemAvailable']//1024;proof['disk_free_bytes']=v.f_bavail*v.f_frsize;proof['io_full_avg10']=avg10
 if values['MemFree']<2048*1024 or values['MemAvailable']<8192*1024 or proof['disk_free_bytes']<30*1024**3 or avg10>5:raise ResourceRefused('Strict resource threshold not met')
def save():
 if proof.get('evidence_already_exists'):return
 proof['elapsed_seconds']=round(time.monotonic()-started,3);(r/'joint-native-gate-9be01cbb-proof.json').write_text(json.dumps(proof,indent=2)+'\n')
def stop():
 global child
 if child is None:return
 try:os.killpg(child.pid,signal.SIGTERM)
 except ProcessLookupError:pass
 try:child.wait(timeout=5)
 except subprocess.TimeoutExpired:
  try:os.killpg(child.pid,signal.SIGKILL)
  except ProcessLookupError:pass
  child.wait(timeout=5)
 child=None
def interrupted(signum,frame):raise KeyboardInterrupt('Interrupted')
signal.signal(signal.SIGTERM,interrupted);signal.signal(signal.SIGINT,interrupted)
try:
 if (r/'joint-native-gate-9be01cbb-proof.json').exists():
  proof['evidence_already_exists']=True
  raise RuntimeError('The single joint-source attempt already has evidence; do not overwrite or retry it')
 if sys.flags.optimize:raise RuntimeError('Gate requires source and ownership assertions enabled')
 resources()
 assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=w,text=True).strip()==source
 assert subprocess.check_output(['git','status','--porcelain'],cwd=w,text=True)==''
 for p in locks:
  fd=os.open(p,os.O_RDWR|os.O_NOFOLLOW|os.O_NONBLOCK);fds.append(fd);s=os.fstat(fd)
  assert stat.S_ISREG(s.st_mode) and s.st_uid==os.geteuid()
  assert (s.st_dev,s.st_ino)==(p.stat().st_dev,p.stat().st_ino)
  assert (s.st_dev,s.st_ino) not in seen;seen.add((s.st_dev,s.st_ino))
  proof['attempted_lock']=str(p)
  try:fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
  except BlockingIOError as error:raise AdmissionBusy(str(error)) from error
  proof['locks'].append({'path':str(p),'dev':s.st_dev,'inode':s.st_ino,'fd':fd})
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
 env.update(PATH=str(bindir)+':'+env['PATH'],GOPROXY='off',GOSUMDB='off',GOTOOLCHAIN='local',GOMAXPROCS='2',GOFLAGS='-p=1',CARGO_BUILD_JOBS='1',CARGO_NET_OFFLINE='true',CARGO_TARGET_DIR=str(r/'native-rust-target'),NODE_OPTIONS='--max-old-space-size=2048',PYTHONDONTWRITEBYTECODE='1',MAKEFLAGS='-j1',PLAYWRIGHT_BROWSERS_PATH='/root/lane-rel-tools/playwright-browsers')
 env['LD_LIBRARY_PATH']='/root/lane-rel-tools/playwright-libs.tUcBz3Xq/sysroot/usr/lib/x86_64-linux-gnu'
 for target in ['web-install','check','build','test']:
  resources()
  remaining=1200-(time.monotonic()-started)
  if remaining<=0:raise TimeoutError('Gate deadline reached before '+target)
  print('NATIVE_GATE_TARGET',target,flush=True)
  child=subprocess.Popen(['unshare','--net','bash','--noprofile','--norc','-euc','ip link set lo up\numask 022\nexec make '+target],cwd=w,env=env,stdin=subprocess.DEVNULL,pass_fds=tuple(fds),start_new_session=True)
  try:code=child.wait(timeout=remaining)
  except BaseException:stop();raise
  child=None;proof[target+'_exit']=code;proof[target]=code==0
  if target=='test':proof['whole_make_test']=code==0
  save()
  if code:raise RuntimeError(target+' exited '+str(code))
 if not preexisting and audit.exists():
  assert stat.S_ISREG(audit.lstat().st_mode) and audit.stat().st_uid==os.geteuid();audit.unlink()
 cleanup_workspace_sum()
 proof['source_clean_after']=subprocess.check_output(['git','status','--porcelain'],cwd=w,text=True)==''
 assert proof['source_clean_after'];proof['passed']=True;exit_code=0

except AdmissionBusy:
 exit_code=75;proof['failure_kind']='lock_contention';proof['admitted']=False;proof['failure']='Shared file lock busy before any target started'
except ResourceRefused as error:
 exit_code=1 if proof.get('admitted') else 75;proof['failure_kind']='resource_refusal';proof['passed']=False;proof['failure']=str(error)
except BaseException as error:
 proof['passed']=False;proof['failure']=type(error).__name__+': '+str(error)
 exit_code=1
 proof['failure_kind']='execution_or_precondition_error'
finally:
 stop()
 if not preexisting and audit.exists():
  try:
   assert stat.S_ISREG(audit.lstat().st_mode) and audit.stat().st_uid==os.geteuid();audit.unlink()
  except BaseException as error:proof['cleanup_failure']=type(error).__name__+': '+str(error);proof['passed']=False;exit_code=1
 try:cleanup_workspace_sum()
 except BaseException as error:proof['workspace_sum_cleanup_failure']=type(error).__name__+': '+str(error);proof['passed']=False;exit_code=1
 proof['source_clean_after']=subprocess.check_output(['git','status','--porcelain'],cwd=w,text=True)==''
 for fd in reversed(fds):
  os.close(fd)
  record=next((row for row in proof['locks'] if row['fd']==fd),None)
  if record is not None:
   record['fd_closed']=True
   try:
    current=os.lstat(record['path']);record['carrier_preserved']=(current.st_dev,current.st_ino)==(record['dev'],record['inode'])
   except OSError:record['carrier_preserved']=False
   if not record['carrier_preserved']:exit_code=1
 proof['exit']=exit_code
 save()
print('NATIVE_GATE_RESULT',json.dumps(proof),flush=True)
sys.exit(exit_code)
