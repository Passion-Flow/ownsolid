import time
START=time.clock_gettime(time.CLOCK_MONOTONIC)
import datetime,fcntl,hashlib,json,os,re,resource,signal,stat,subprocess,sys
from pathlib import Path
assert sys.flags.isolated and not sys.flags.optimize
resource.setrlimit(resource.RLIMIT_CORE,(0,0));os.umask(0o077)
root=Path(sys.argv[1]);end=START+600;cutoff=end-60;carrier=Path('/Users/passion./.local/state/ownsolid/coordination/workstation/ownsolid-2-20261113-e1/full-gate.lock');manifest_hash='7fa013a32f542f8ff6467863425e952da5e436b0ad470da25d766613c94eb983';fd=None;child=None;awake=None;operational=0;child_exit=None;cleanup_errors=[];groups=set()
state={'parentPID':os.getpid(),'entryMonotonic':START,'workCutoffMonotonic':cutoff,'hardDeadlineMonotonic':end,'totalSeconds':600,'cleanupReserve':60,'childStarted':False,'phaseHistory':[]}
def left(cap=600,cleanup=False):
 remaining=(end if cleanup else cutoff)-time.clock_gettime(time.CLOCK_MONOTONIC)
 if remaining<=0:raise TimeoutError('Absolute '+('cleanup' if cleanup else 'work')+' deadline')
 return min(cap,remaining)
def save(name,data):
 left(cleanup=True);(root/name).write_text(json.dumps(data,indent=2)+'\n')
def record(phase,**data):
 state['phaseHistory'].append({'phase':phase,**data});state.update(phase=phase,**data);save('gate-state.json',state)
def check_identity(descriptor):
 raw=carrier.with_name('manifest.json').read_bytes();assert hashlib.sha256(raw).hexdigest()==manifest_hash;manifest=json.loads(raw);assert manifest['epoch']=='ownsolid-2-20261113-e1' and manifest['carrier_path']==str(carrier)
 for entry in manifest['parent_paths']:
  s=os.lstat(entry['path']);assert stat.S_ISDIR(s.st_mode) and not stat.S_ISLNK(s.st_mode) and s.st_uid==entry['uid'] and stat.S_IMODE(s.st_mode)==int(entry['mode'],8)
 for s in [os.fstat(descriptor),carrier.lstat()]:assert stat.S_ISREG(s.st_mode) and (s.st_dev,s.st_ino,s.st_uid,stat.S_IMODE(s.st_mode),s.st_nlink,s.st_size)==(16777231,53377467,501,0o600,1,0)
def snapshot():
 raw=subprocess.check_output(['ps','-axo','pid=,ppid=,pgid=,uid=,stat='],text=True,timeout=left(3,True));return [line.split() for line in raw.splitlines() if line.strip()]
def collect_groups(rows):
 owned={child.pid} if child else set()
 for _ in rows:
  added={int(r[0]) for r in rows if int(r[1]) in owned}-owned
  if not added:break
  owned.update(added)
 return {int(r[2]) for r in rows if int(r[0]) in owned}
def terminate_owned():
 rows=snapshot();groups.update(collect_groups(rows));reg=root/'owned-groups.json'
 if reg.exists():groups.update(json.loads(reg.read_text()))
 if child:groups.add(child.pid)
 # Only a fresh known group with our UID is signalled; exited groups are left alone.
 for group in sorted(groups):
  members=[r for r in rows if int(r[2])==group and not r[4].startswith('Z')]
  if not members:continue
  if group==os.getpgrp() or any(int(r[3])!=501 for r in members):cleanup_errors.append('Refused unowned group '+str(group));continue
  try:os.killpg(group,signal.SIGTERM)
  except ProcessLookupError:pass
  except PermissionError as e:
   cleanup_errors.append('Group TERM: '+repr(e))
   for r in members:
    try:os.kill(int(r[0]),signal.SIGTERM)
    except ProcessLookupError:pass
    except OSError as err:cleanup_errors.append('PID TERM: '+repr(err))
 if child and child.poll() is None:
  try:child.wait(timeout=left(2,True))
  except subprocess.TimeoutExpired:pass
 rows=snapshot()
 for group in sorted(groups):
  members=[r for r in rows if int(r[2])==group and not r[4].startswith('Z')]
  if not members:continue
  if group==os.getpgrp() or any(int(r[3])!=501 for r in members):continue
  try:os.killpg(group,signal.SIGKILL)
  except ProcessLookupError:pass
  except OSError as e:cleanup_errors.append('Group KILL: '+repr(e))
 # Preview ownership is bound to the initially empty port and exact worktree cwd.
 listing=subprocess.run(['/usr/sbin/lsof','-t','-nP','-iTCP:31036','-sTCP:LISTEN'],capture_output=True,text=True,timeout=left(3,True))
 for line in listing.stdout.splitlines():
  if not line.isdigit():continue
  pid=int(line);cwd=subprocess.run(['/usr/sbin/lsof','-a','-p',str(pid),'-d','cwd','-Fn'],capture_output=True,text=True,timeout=left(3,True))
  if 'n'+info['worktree']+'/web/apps/console' not in cwd.stdout.splitlines():cleanup_errors.append('Unowned preview listener '+str(pid));continue
  try:os.kill(pid,signal.SIGKILL)
  except ProcessLookupError:pass
  except OSError as e:cleanup_errors.append('Preview KILL: '+repr(e))
 rows=snapshot();live=[r for r in rows if int(r[2]) in groups and not r[4].startswith('Z')];save('owned-groups-cleanup.json',live)
 if live:cleanup_errors.append('Owned process group remains')
 ports=subprocess.run(['/usr/sbin/lsof','-nP','-iTCP:31036','-sTCP:LISTEN'],capture_output=True,text=True,timeout=left(3,True));save('preview-cleanup.json',{'exit':ports.returncode,'stdout':ports.stdout,'stderr':ports.stderr})
 if ports.returncode!=1:cleanup_errors.append('Preview listener remains or inventory failed')
def hard_expired(a,b):raise TimeoutError('Absolute hard deadline')
signal.signal(signal.SIGALRM,hard_expired);signal.setitimer(signal.ITIMER_REAL,end-time.clock_gettime(time.CLOCK_MONOTONIC))
info=json.loads((root/'preparation.json').read_text())
for name,h in info['preparedFiles'].items():assert hashlib.sha256((root/name).read_bytes()).hexdigest()==h,name
marker=os.open(root/'attempted',os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600);os.close(marker)
try:
 fd=os.open(carrier,os.O_RDWR|os.O_NOFOLLOW|os.O_CLOEXEC);check_identity(fd);record('one-nonblocking-attempt',descriptor=fd,device=16777231,inode=53377467)
 try:fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
 except BlockingIOError:operational=75;record('busy-NOTRUN');raise SystemExit(75)
 check_identity(fd);record('held')
 memory=subprocess.check_output(['vm_stat'],timeout=left(3));(root/'preflight-memory.log').write_bytes(memory);pages=int(re.search(rb'Pages free:\s+(\d+)',memory)[1]);page=int(re.search(rb'page size of (\d+)',memory)[1]);disk=os.statvfs(root);free=disk.f_bavail*disk.f_frsize
 record('preflight',rawFreeBytes=pages*page,diskFreeBytes=free)
 if pages*page<3*1024**3 or free<15*1024**3:operational=78;record('resources-NOTRUN');raise SystemExit(78)
 ports=subprocess.run(['/usr/sbin/lsof','-nP','-iTCP:31036','-sTCP:LISTEN'],capture_output=True,text=True,timeout=left(3));save('preflight-ports.json',{'exit':ports.returncode,'stdout':ports.stdout,'stderr':ports.stderr});assert ports.returncode==1,'Preview port unavailable'
 awake=subprocess.Popen(['caffeinate','-i','-w',str(os.getpid())]);env=os.environ.copy();env['OWNSOLID_GATE_FD']=str(fd);env['OWNSOLID_GATE_PARENT']=str(os.getpid())
 with (root/'worker.log').open('x') as out:
  check_identity(fd);child=subprocess.Popen([sys.executable,'-I',str(root/'worker.py'),str(root),str(cutoff)],stdout=out,stderr=subprocess.STDOUT,pass_fds=(fd,),env=env,start_new_session=True);groups.add(child.pid);record('executing',childStarted=True,childPID=child.pid,childPGID=child.pid)
  try:child_exit=child.wait(timeout=left())
  except subprocess.TimeoutExpired:operational=124;record('work-deadline',childExit=child.poll())
except SystemExit:pass
except BaseException as e:operational=124 if isinstance(e,TimeoutError) else 1;record('operational-error',error=repr(e))
finally:
 try:
  if child:terminate_owned()
  if child:child_exit=child.poll() if child.poll() is not None else child_exit
  if awake:
   awake.terminate()
   try:awake.wait(timeout=left(2,True))
   except subprocess.TimeoutExpired:awake.kill();awake.wait(timeout=left(2,True))
  clean=subprocess.check_output(['git','status','--porcelain'],cwd=info['worktree'],text=True,timeout=left(3,True));save('fixture-cleanup.json',{'fixtureFilesRemoved':[],'gitStatus':clean})
  if clean:cleanup_errors.append('Worktree not clean')
 except BaseException as e:cleanup_errors.append(repr(e))
 finally:
  try:
   if fd is not None:check_identity(fd)
  except BaseException as e:cleanup_errors.append('Carrier: '+repr(e))
  normalized_child = (128-child_exit) if child_exit is not None and child_exit<0 else child_exit
  overall=operational or (1 if cleanup_errors else (normalized_child if normalized_child is not None else 1))
  result={'exit':overall,'wrapperOperationalExit':operational,'childExit':child_exit,'cleanupExit':1 if cleanup_errors else 0,'cleanupConfirmed':not cleanup_errors,'cleanupErrors':cleanup_errors,'elapsedSeconds':time.clock_gettime(time.CLOCK_MONOTONIC)-START}
  try:record('cleanup-evidence',**result)
  finally:
   if fd is not None:
    try:fcntl.flock(fd,fcntl.LOCK_UN)
    finally:os.close(fd)
  os.write(1,(json.dumps(result)+'\n').encode());os._exit(overall)
