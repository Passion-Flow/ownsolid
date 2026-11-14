
# OWNSOLID_OLD_MAC_EPOCH_CLOSED
import sys as _ownsolid_closed_epoch_sys
_ownsolid_closed_epoch_sys.stderr.write("Old Mac admission epoch is closed; await coordinated activation.\n")
raise SystemExit(78)
from pathlib import Path
import datetime,fcntl,json,os,shutil,signal,stat,subprocess,sys,time
os.umask(0o077)
root=Path(sys.argv[1]);carrier=Path('/private/tmp/ownsolid-workstation-full-gate.lock')
state={'parentPID':os.getpid(),'carrier':str(carrier),'expectedInode':50313339,'kernelWaitBudgetSeconds':900,'executionBudgetSeconds':300,'childStarted':False}
def record(phase,**values):
 state.update(values);state['phase']=phase;state['updatedUTC']=datetime.datetime.now(datetime.timezone.utc).isoformat()
 tmp=root/'gate-state.json.tmp';tmp.write_text(json.dumps(state,indent=2)+'\n');tmp.replace(root/'gate-state.json')
def inspect(fd):
 opened=os.fstat(fd);current=os.stat(carrier,follow_symlinks=False)
 assert stat.S_ISREG(opened.st_mode) and opened.st_uid==os.geteuid()
 assert opened.st_ino==50313339 and (opened.st_dev,opened.st_ino)==(current.st_dev,current.st_ino)
 return opened
class WaitExpired(Exception):pass
def wait_expired(signum,frame):raise WaitExpired('The one authorized kernel wait reached its deadline')
def preflight():
 free=shutil.disk_usage(root).free
 (root/'preflight-memory.log').write_bytes(subprocess.check_output(['vm_stat']))
 (root/'preflight-space.log').write_bytes(subprocess.check_output(['df','-h',str(root)]))
 assert free>=15*1024**3,'Under15GiB free; no child started'
 for label,args in [('ports',['lsof','-nP','-iTCP:30376','-iTCP:30377','-sTCP:LISTEN']),('legacy',['lsof','-nP','/private/tmp/ownsolid-macos-full-gate.lock'])]:
  result=subprocess.run(args,text=True,stdout=subprocess.PIPE,stderr=subprocess.STDOUT)
  (root/('preflight-'+label+'.log')).write_text(result.stdout)
  if result.returncode==0 and result.stdout.strip():raise RuntimeError(label+' occupied; no child started')
def cancelled(signum,frame):raise KeyboardInterrupt("Narrow window interrupted")
signal.signal(signal.SIGTERM,cancelled)
code=1;fd=None;child=None;awake=None;start=None
try:
 fd=os.open(carrier,os.O_RDWR|os.O_NOFOLLOW);info=inspect(fd)
 record('kernel-wait',descriptor=fd,device=info.st_dev,inode=info.st_ino)
 signal.signal(signal.SIGALRM,wait_expired);signal.alarm(900)
 try:fcntl.flock(fd,fcntl.LOCK_EX)
 finally:signal.alarm(0)
 inspect(fd);record('held',descriptor=fd)
 preflight()
 awake=subprocess.Popen(['caffeinate','-i','-w',str(os.getpid())])
 with (root/'gate-output.log').open('w') as out:
  try:child=subprocess.Popen([sys.executable,str(root/'narrow-run.py'),str(root)],stdout=out,stderr=subprocess.STDOUT,start_new_session=True)
  except OSError as error:
   record('child-start-error',error=str(error),errno=error.errno);raise
  start=time.monotonic();record('executing',childStarted=True,childPID=child.pid,childGroup=child.pid)
  try:code=child.wait(timeout=285)
  except subprocess.TimeoutExpired:
   record('execution-timeout',childStarted=True)
   os.killpg(child.pid,signal.SIGTERM)
   try:child.wait(timeout=5)
   except subprocess.TimeoutExpired:
    os.killpg(child.pid,signal.SIGKILL);child.wait(timeout=3)
   code=124
 record('finished' if code==0 else 'child-failed',exit=code,executionSeconds=time.monotonic()-start)
except WaitExpired as error:
 code=124;record('kernel-wait-timeout',exit=code,error=str(error),childStarted=False)
except BaseException as error:
 code=1;record('wrapper-error',exit=code,error=repr(error),childStarted=child is not None)
finally:
 if child is not None and child.poll() is None:
  try:os.killpg(child.pid,signal.SIGTERM)
  except ProcessLookupError:pass
  try:child.wait(timeout=5)
  except subprocess.TimeoutExpired:
   try:os.killpg(child.pid,signal.SIGKILL)
   except ProcessLookupError:pass
   child.wait(timeout=3)
 if awake is not None:
  awake.terminate()
  try:awake.wait(timeout=2)
  except subprocess.TimeoutExpired:awake.kill();awake.wait()
 if fd is not None:
  try:
   inspect(fd)
   info=json.loads((root/'preparation.json').read_text());target=Path(info['frozenWorktree'])/info['testPath']
   if target.exists() and target.read_bytes()==(root/'negative-help-flows.ts').read_bytes():target.write_bytes((root/'fixed-help-flows.ts').read_bytes())
  except BaseException as error:
   code=1;record('carrier-or-cleanup-error',error=repr(error),exit=code)
  finally:
   fcntl.flock(fd,fcntl.LOCK_UN);os.close(fd)
 (root/'gate-exit-status').write_text(str(code)+'\n')
print(json.dumps({'phase':state['phase'],'exit':code,'childStarted':state['childStarted'],'state':str(root/'gate-state.json')}))
raise SystemExit(code)
