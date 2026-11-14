raise SystemExit("LANE_M_OLD_GATE_EPOCH_CLOSED")
import datetime,fcntl,hashlib,json,os,signal,stat,subprocess,time
from pathlib import Path
ROOT=Path(__file__).resolve().parent
WAIT_SECONDS=900
EXECUTION_SECONDS=900
m=json.loads((ROOT/'prepared.json').read_text())
report={'source':m['source'],'tree':m['tree'],'base':m['base'],'pid':os.getpid(),'wait_seconds_limit':WAIT_SECONDS,'execution_seconds_limit':EXECUTION_SECONDS,'lock_acquired':False,'child_started':False,'child_exit':None,'fd_closed':False,'owned_group_exited':True,'started_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'raw_exit':1}
fd=None;child=None;group=None;start=time.monotonic();wait_start=None;execution_start=None
class AdmissionTimeout(Exception):pass
def alarm(signum,frame):raise AdmissionTimeout('One kernel wait reached its deadline')
def stop(signum,frame):raise KeyboardInterrupt('Interrupted')
def members():
 if group is None:return []
 return [int(a[0]) for line in subprocess.check_output(['ps','-Ao','pid=,pgid='],text=True).splitlines() if len(a:=line.split())==2 and int(a[1])==group]
def terminate_owned():
 remaining=members()
 if not remaining:return
 os.killpg(group,signal.SIGTERM)
 if child is not None:
  try:child.wait(timeout=10)
  except subprocess.TimeoutExpired:pass
 if members():
  os.killpg(group,signal.SIGKILL)
  if child is not None:child.wait(timeout=5)
try:
 assert hashlib.sha256((ROOT/'prepared.json').read_bytes()).hexdigest()=='24dad3ce4280fa9d5cb883c50f920138e9f24102a314a32af16bdfe1fee141a6'
 assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=m['worktree'],text=True).strip()==m['source']
 assert not subprocess.check_output(['git','status','--porcelain'],cwd=m['worktree'],text=True).strip()
 for name,value in m['entry_sha256'].items():assert hashlib.sha256(Path(name).read_bytes()).hexdigest()==value,name
 carrier=Path(m['carrier']['path']);before=carrier.lstat()
 assert stat.S_ISREG(before.st_mode) and before.st_uid==os.geteuid() and before.st_nlink==1
 assert (before.st_dev,before.st_ino,before.st_size)==(m['carrier']['device'],50313339,0)
 fd=os.open(carrier,os.O_RDWR|os.O_NOFOLLOW)
 opened=os.fstat(fd);assert (opened.st_dev,opened.st_ino)==(before.st_dev,before.st_ino)
 report.update(fd=fd,device=opened.st_dev,inode=opened.st_ino,wait_started_utc=datetime.datetime.now(datetime.timezone.utc).isoformat())
 signal.signal(signal.SIGALRM,alarm);signal.signal(signal.SIGINT,stop);signal.signal(signal.SIGTERM,stop)
 wait_start=time.monotonic();print('Entering the single bounded kernel wait',flush=True)
 signal.setitimer(signal.ITIMER_REAL,WAIT_SECONDS)
 try:fcntl.flock(fd,fcntl.LOCK_EX)
 finally:signal.setitimer(signal.ITIMER_REAL,0)
 report.update(lock_acquired=True,wait_elapsed_seconds=time.monotonic()-wait_start,lock_acquired_utc=datetime.datetime.now(datetime.timezone.utc).isoformat())
 current=carrier.lstat();assert (current.st_dev,current.st_ino,current.st_size)==(before.st_dev,before.st_ino,0)
 print('Acquired original carrier; starting fresh gate admission',flush=True)
 execution_start=time.monotonic()
 with (ROOT/'one-window-child.log').open('x') as log:
  child=subprocess.Popen(['bash',str(ROOT/'run-native.sh'),m['worktree']],cwd=m['worktree'],stdout=log,stderr=subprocess.STDOUT,pass_fds=(fd,),start_new_session=True)
  group=child.pid;report.update(child_started=True,child_pid=child.pid,owned_pgid=group,execution_started_utc=datetime.datetime.now(datetime.timezone.utc).isoformat())
  try:report['child_exit']=child.wait(timeout=EXECUTION_SECONDS);report['raw_exit']=report['child_exit']
  except subprocess.TimeoutExpired:
   report.update(raw_exit=124,reason='Execution deadline reached independently of wait time');terminate_owned();report['child_exit']=child.poll()
except AdmissionTimeout as error:report.update(raw_exit=75,reason=str(error),admission_timeout=True)
except KeyboardInterrupt as error:report.update(raw_exit=130,reason=str(error))
except Exception as error:report.update(raw_exit=1,reason=type(error).__name__+': '+str(error),errno=getattr(error,'errno',None))
finally:
 signal.setitimer(signal.ITIMER_REAL,0)
 if wait_start is not None and 'wait_elapsed_seconds' not in report:report['wait_elapsed_seconds']=time.monotonic()-wait_start
 if execution_start is not None:report['execution_elapsed_seconds']=time.monotonic()-execution_start
 try:
  terminate_owned();report['owned_group_remaining']=members();report['owned_group_exited']=not report['owned_group_remaining']
 except Exception as error:report['cleanup_error']=type(error).__name__+': '+str(error);report['raw_exit']=1
 if fd is not None:os.close(fd);report['fd_closed']=True
 try:
  after=Path(m['carrier']['path']).lstat();report['carrier_preserved']=(after.st_dev,after.st_ino,after.st_size)==(m['carrier']['device'],50313339,0)
  report['source_unchanged']=subprocess.check_output(['git','rev-parse','HEAD'],cwd=m['worktree'],text=True).strip()==m['source']
 except Exception as error:report['final_verification_error']=str(error);report['raw_exit']=1
 report.update(total_elapsed_seconds=time.monotonic()-start,ended_utc=datetime.datetime.now(datetime.timezone.utc).isoformat())
 (ROOT/'one-window-result.json').write_text(json.dumps(report,indent=2)+'\n')
 print(json.dumps(report,indent=2),flush=True)
raise SystemExit(report['raw_exit'])
