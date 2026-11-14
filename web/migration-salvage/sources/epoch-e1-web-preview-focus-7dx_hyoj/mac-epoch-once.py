import time
ENTRY_STARTED=time.clock_gettime(time.CLOCK_MONOTONIC)
from pathlib import Path
import datetime,fcntl,hashlib,json,os,re,shutil,signal,stat,subprocess,sys,resource
def require_runtime(flags):
 if not flags.isolated or flags.optimize:
  raise SystemExit('Run with python3 -I and without -O; no acquisition attempted')
require_runtime(sys.flags)
TOTAL_DEADLINE=ENTRY_STARTED+600
WORK_DEADLINE=TOTAL_DEADLINE-60
cleaning=False
class DeadlineExpired(BaseException):pass
def budget(cap,deadline=None,now=None):
 target=(TOTAL_DEADLINE if cleaning else WORK_DEADLINE) if deadline is None else deadline
 remaining=target-(time.clock_gettime(time.CLOCK_MONOTONIC) if now is None else now)
 if remaining<=0:raise DeadlineExpired('The absolute operation budget expired')
 return min(float(cap),remaining)
def check_budget():budget(1800)
def bounded_write(path,value,deadline=None):
 budget(1800,deadline=deadline)
 if isinstance(value,bytes):path.write_bytes(value)
 else:path.write_text(value)
 budget(1800,deadline=deadline)
def deadline_expired(signum,frame):raise DeadlineExpired('The entry absolute backstop fired')
def arm_backstop(deadline):
 signal.signal(signal.SIGALRM,deadline_expired)
 signal.setitimer(signal.ITIMER_REAL,budget(1800,deadline=deadline))
arm_backstop(TOTAL_DEADLINE)
os.umask(0o077)
resource.setrlimit(resource.RLIMIT_CORE,(0,0))
root=Path(sys.argv[1]);carrier=Path('/Users/passion./.local/state/ownsolid/coordination/workstation/ownsolid-2-20261113-e1/full-gate.lock')
state={'parentPID':os.getpid(),'carrier':str(carrier),'expectedInode':53377467,'expectedDevice':16777231,'epoch':'ownsolid-2-20261113-e1','coreLimitBytes':0,'kernelWaitBudgetSeconds':0,'executionBudgetSeconds':600,'childStarted':False,'entryMonotonic':ENTRY_STARTED,'hardDeadlineMonotonic':TOTAL_DEADLINE,'workCutoffMonotonic':WORK_DEADLINE,'cleanupReserveSeconds':60,'cleanupConfirmed':False,'cleanupEvidenceFailed':False,'groupsConfirmed':True,'awakeConfirmed':True}
def record(phase,**values):
 state.setdefault('phaseHistory',[]).append({'phase':phase,**values});state.update(values);state['phase']=phase;state['updatedUTC']=datetime.datetime.now(datetime.timezone.utc).isoformat()
 budget(1800,deadline=TOTAL_DEADLINE);tmp=root/'gate-state.json.tmp';bounded_write(tmp,json.dumps(state,indent=2)+'\n',deadline=TOTAL_DEADLINE);budget(1800,deadline=TOTAL_DEADLINE);tmp.replace(root/'gate-state.json');budget(1800,deadline=TOTAL_DEADLINE)
def inspect(fd):
 check_budget()
 manifest=carrier.with_name('manifest.json');ms=manifest.lstat()
 assert stat.S_ISREG(ms.st_mode) and ms.st_uid==501 and not stat.S_ISLNK(ms.st_mode)
 raw=manifest.read_bytes();assert hashlib.sha256(raw).hexdigest()=='7fa013a32f542f8ff6467863425e952da5e436b0ad470da25d766613c94eb983'
 epoch=json.loads(raw);assert epoch['epoch']=='ownsolid-2-20261113-e1' and epoch['carrier_path']==str(carrier)
 expected=epoch['lock_identity'];assert expected=={'dev':16777231,'inode':53377467,'uid':501,'mode':'0600','regular':True,'nlink':1,'size':0}
 for directory in epoch['parent_paths']:
  s=os.lstat(directory['path']);assert not stat.S_ISLNK(s.st_mode) and stat.S_ISDIR(s.st_mode)
  assert s.st_uid==directory['uid'] and stat.S_IMODE(s.st_mode)==int(directory['mode'],8)
 for s in [os.fstat(fd),os.lstat(carrier)]:
  assert stat.S_ISREG(s.st_mode) and not stat.S_ISLNK(s.st_mode)
  assert (s.st_dev,s.st_ino,s.st_uid,stat.S_IMODE(s.st_mode),s.st_nlink,s.st_size)==(16777231,53377467,501,0o600,1,0)
 check_budget();return os.fstat(fd)
class LockBusy(Exception):pass
def cleanup_failure(phase,error):
 state['cleanupEvidenceFailed']=True;state['cleanupConfirmed']=False
 record(phase,exit=1,error=str(error),cleanupConfirmed=False)
 return 1
def preflight():
 global TOTAL_DEADLINE,WORK_DEADLINE
 free=shutil.disk_usage(root).free
 memory=subprocess.check_output(['vm_stat'],timeout=budget(2));bounded_write(root/'preflight-memory.log',memory)
 pages=int(re.search(rb'Pages free:\s+(\d+)',memory).group(1));page_size=int(re.search(rb'page size of (\d+)',memory).group(1));raw=pages*page_size
 bounded_write(root/'preflight-space.log',subprocess.check_output(['df','-h',str(root)],timeout=budget(2)))
 choices=json.loads((root/'scope-inputs.json').read_text())
 if raw>=3*1024**3 and free>=15*1024**3:scope='T';seconds=600;reserve=60;node=512
 else:
  record('resources-refused',rawFreeBytes=raw,diskFreeBytes=free,chosenScope='NONE',fourFiles='NOTRUN',observerControls='NOTRUN')
  raise RuntimeError('Directed browser profile resource floor is not met; no work child started')
 TOTAL_DEADLINE=ENTRY_STARTED+seconds;WORK_DEADLINE=TOTAL_DEADLINE-reserve;arm_backstop(TOTAL_DEADLINE)
 selected={'scope':scope,**choices[scope],'rawFreeBytes':raw,'freePages':pages,'pageBytes':page_size,'diskFreeBytes':free,'totalSeconds':seconds,'cleanupReserveSeconds':reserve,'nodeOldSpaceMaximumMiB':node,'unselectedScope':'NOTRUN','entryMonotonic':ENTRY_STARTED,'hardDeadlineMonotonic':TOTAL_DEADLINE,'workCutoffMonotonic':WORK_DEADLINE}
 bounded_write(root/'selected-scope.json',json.dumps(selected,indent=2)+'\n')
 state.update(executionBudgetSeconds=seconds,cleanupReserveSeconds=reserve,hardDeadlineMonotonic=TOTAL_DEADLINE,workCutoffMonotonic=WORK_DEADLINE)
 record('scope-selected',chosenScope=scope,selectedSource=selected['source'],selectedTree=selected['tree'],rawFreeBytes=raw,diskFreeBytes=free,unselectedScope='NOTRUN')
 result=subprocess.run(['lsof','-nP','-iTCP:31036','-iTCP:31037','-sTCP:LISTEN'],text=True,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,timeout=budget(2));bounded_write(root/'preflight-ports.log',result.stdout)
 if result.returncode not in [0,1]:raise RuntimeError('Cannot establish original preview-port preflight')
 if result.returncode==0 and result.stdout.strip():raise RuntimeError('Preview ports occupied; no child started')
def capture_groups(pid):
 raw=subprocess.check_output(['ps','-axo','pid=,ppid=,pgid='],text=True,timeout=budget(2))
 rows=[tuple(map(int,line.split())) for line in raw.splitlines() if line.strip()]
 descendants={pid}|{child for child,parent,group in rows if group==pid}
 for _ in rows:
  check_budget()
  added={child for child,parent,group in rows if parent in descendants}-descendants
  if not added:break
  descendants.update(added)
 return {group for child,parent,group in rows if child in descendants and group!=os.getpgrp()}
def cancelled(signum,frame):raise KeyboardInterrupt("Narrow window interrupted")
signal.signal(signal.SIGTERM,cancelled)
code=1;fd=None;child=None;awake=None;start=None;owned_groups=set()
# Verify the reviewed operational inputs before touching the lock carrier.
check_budget();prepared=json.loads((root/'preparation.json').read_text())
for name in prepared['preparedFiles']:
 check_budget();assert hashlib.sha256((root/name).read_bytes()).hexdigest()==prepared['preparedFiles'][name],name
# An accidental second invocation cannot create a second admission attempt.
check_budget();marker=os.open(root/'attempted',os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600);os.close(marker);check_budget()
try:
 try:
  fd=os.open(carrier,os.O_RDWR|os.O_NOFOLLOW|os.O_CLOEXEC);info=inspect(fd)
  record('one-nonblocking-attempt',descriptor=fd,device=info.st_dev,inode=info.st_ino)
  try:fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
  except BlockingIOError:raise LockBusy('Shared Mac window occupied; no child started')
  inspect(fd);record('held',descriptor=fd)
  preflight()
  check_budget();awake=subprocess.Popen(['caffeinate','-i','-w',str(os.getpid())]);state['awakeConfirmed']=False;check_budget()
  with (root/'gate-output.log').open('w') as out:
   check_budget()
   try:
    inspect(fd);child_env=os.environ.copy();child_env['OWNSOLID_GATE_FD']=str(fd);child_env['OWNSOLID_GATE_PARENT']=str(os.getpid())
    child=subprocess.Popen([sys.executable,'-I',str(root/'target-run.py'),str(root),str(WORK_DEADLINE),str(TOTAL_DEADLINE)],stdout=out,stderr=subprocess.STDOUT,start_new_session=True,pass_fds=(fd,),env=child_env)
   except OSError as error:
    record('child-start-error',error=str(error),errno=error.errno);raise
   state['groupsConfirmed']=False;check_budget();start=time.clock_gettime(time.CLOCK_MONOTONIC);record('executing',childStarted=True,childPID=child.pid,childGroup=child.pid)
   try:code=child.wait(timeout=budget(1675))
   except subprocess.TimeoutExpired:
    cleaning=True;code=124
    record('execution-timeout',childStarted=True)
    try:owned_groups.update(capture_groups(child.pid))
    except (OSError,subprocess.SubprocessError):record('timeout-descendant-snapshot-failed')
    os.killpg(child.pid,signal.SIGTERM)
    try:child.wait(timeout=budget(5))
    except subprocess.TimeoutExpired:
     os.killpg(child.pid,signal.SIGKILL);child.wait(timeout=budget(3))
    code=124
  record('finished' if code==0 else 'child-failed',exit=code,executionSeconds=time.clock_gettime(time.CLOCK_MONOTONIC)-start)
 except DeadlineExpired as error:
  cleaning=True;code=124;record('absolute-deadline-expired',exit=code,error=str(error))
 except LockBusy as error:
  cleaning=True;code=75;record('lock-busy',exit=code,error=str(error),childStarted=False,busy=True)
 except BaseException as error:
  cleaning=True;code=1;record('wrapper-error',exit=code,error=repr(error),childStarted=child is not None)
 finally:
  cleaning=True
  try:
   if child is not None and child.poll() is None:
    try:owned_groups.update(capture_groups(child.pid))
    except (OSError,subprocess.SubprocessError) as error:code=cleanup_failure('cleanup-initial-descendant-snapshot-failed',error)
    try:os.killpg(child.pid,signal.SIGTERM)
    except ProcessLookupError:pass
    try:child.wait(timeout=budget(5))
    except subprocess.TimeoutExpired:
     try:os.killpg(child.pid,signal.SIGKILL)
     except ProcessLookupError:pass
     child.wait(timeout=budget(3))
   if child is not None:
    groups={child.pid}|owned_groups
    try:groups.update(capture_groups(child.pid))
    except (OSError,subprocess.SubprocessError) as error:code=cleanup_failure('cleanup-descendant-snapshot-failed',error)
    registry=root/'owned-stage-groups.json'
    if registry.exists():
     try:groups.update(json.loads(registry.read_text()))
     except (ValueError,OSError) as error:code=cleanup_failure('cleanup-registry-unreadable',error)
    info=json.loads((root/'preparation.json').read_text())
    check_budget()
    for port,cwd in [(31036,info['worktree']+'/web/apps/console'),(31037,info['worktree']+'/web/apps/portal')]:
     listeners=subprocess.run(['lsof','-t','-nP','-iTCP:'+str(port),'-sTCP:LISTEN'],stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,timeout=budget(2))
     for raw in listeners.stdout.splitlines():
      check_budget()
      if not raw.isdigit():continue
      pid=int(raw)
      current=subprocess.run(['lsof','-a','-p',str(pid),'-d','cwd','-Fn'],stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,timeout=budget(2))
      if ('n'+cwd) in current.stdout.splitlines():
       try:groups.add(os.getpgid(pid))
       except ProcessLookupError:pass
    for group in groups:
     check_budget()
     if group==os.getpgrp():continue
     try:os.killpg(group,signal.SIGTERM)
     except ProcessLookupError:pass
    for group in groups:
     check_budget()
     if group==os.getpgrp():continue
     try:os.killpg(group,signal.SIGKILL)
     except ProcessLookupError:pass
    ports=subprocess.run(['lsof','-nP','-iTCP:31036','-iTCP:31037','-sTCP:LISTEN'],stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,timeout=budget(2))
    bounded_write(root/'preview-cleanup.log',ports.stdout)
    if ports.returncode!=1:code=cleanup_failure('cleanup-listener-remains',ports.stdout)
    snapshot=subprocess.check_output(['ps','-axo','pgid=,stat='],text=True,timeout=budget(2))
    live=[]
    for row in snapshot.splitlines():
     check_budget();parts=row.split()
     if parts and int(parts[0]) in groups and not parts[1].startswith('Z'):live.append(row)
    bounded_write(root/'owned-groups-cleanup.json',json.dumps(live)+'\n')
    state['groupsConfirmed']=not live
    if live:code=cleanup_failure('cleanup-owned-group-remains',live)
  except BaseException as error:
   code=124 if isinstance(error,DeadlineExpired) else 1;state['cleanupEvidenceFailed']=True;state['cleanupConfirmed']=False;record('child-cleanup-error',error=repr(error),exit=code,cleanupConfirmed=False)
  finally:
   try:
    if child is not None:
     resource_cleanup=subprocess.run([sys.executable,'-I',str(root/'cleanup-epoch-owned.py'),str(root),str(TOTAL_DEADLINE-5)],stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,timeout=budget(60))
     bounded_write(root/'epoch-resource-cleanup-parent.log',resource_cleanup.stdout)
     if resource_cleanup.returncode!=0:code=cleanup_failure('full-resource-cleanup-failed',resource_cleanup.stdout)
   except BaseException as error:
    code=cleanup_failure('full-resource-cleanup-error',error)
   try:
    if awake is not None:
     awake.terminate()
     try:awake.wait(timeout=budget(2))
     except subprocess.TimeoutExpired:awake.kill();awake.wait(timeout=budget(2))
     state['awakeConfirmed']=True
   except BaseException as error:
    code=124 if isinstance(error,DeadlineExpired) else 1;state['cleanupEvidenceFailed']=True;state['cleanupConfirmed']=False;record('awake-cleanup-error',error=repr(error),exit=code,cleanupConfirmed=False)
   finally:
    if fd is not None:
     try:
      inspect(fd)
      state['cleanupConfirmed']=state['groupsConfirmed'] and state['awakeConfirmed'] and not state['cleanupEvidenceFailed']
      if not state['cleanupConfirmed'] and code==0:code=1
      record('cleanup-evidence',exit=code,cleanupConfirmed=state['cleanupConfirmed'])
     except BaseException as error:
      code=124 if isinstance(error,DeadlineExpired) else 1;state['cleanupEvidenceFailed']=True;state['cleanupConfirmed']=False;record('carrier-cleanup-error',error=repr(error),exit=code,cleanupConfirmed=False)
     finally:
      try:fcntl.flock(fd,fcntl.LOCK_UN)
      finally:os.close(fd)
    bounded_write(root/'gate-exit-status',str(code)+'\n')
except BaseException as error:
 code=124 if isinstance(error,DeadlineExpired) else 1
 state['cleanupConfirmed']=False;state['cleanupEvidenceFailed']=True;state['terminalError']=repr(error)
finally:
 # Keep the absolute timer armed through evidence/output and exit. No unbounded
 # post-deadline recording is attempted; missing terminal evidence cannot pass.
 if time.clock_gettime(time.CLOCK_MONOTONIC)>=TOTAL_DEADLINE:os._exit(124)
 try:
  payload=json.dumps({'phase':state.get('phase'),'exit':code,'childStarted':state['childStarted'],'cleanupConfirmed':state['cleanupConfirmed'],'elapsedSeconds':time.clock_gettime(time.CLOCK_MONOTONIC)-ENTRY_STARTED,'state':str(root/'gate-state.json')})+'\n'
  os.write(1,payload.encode())
 except BaseException as error:
  code=124 if isinstance(error,DeadlineExpired) else 1
 os._exit(code)
