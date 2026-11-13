import fcntl,hashlib,json,os,re,signal,stat,subprocess,sys,time
from pathlib import Path
r=Path(__file__).resolve().parent
start=time.monotonic();fd=None;child=None;result={"nb_attempts":0,"worker_started":False,"groups_cleared":False,"stage":"preflight"}
carrier=Path("/Users/passion./.local/state/ownsolid/coordination/workstation/ownsolid-2-20261113-e1/full-gate.lock")
expected="7fa013a32f542f8ff6467863425e952da5e436b0ad470da25d766613c94eb983"
manifest=next(p for p in carrier.parent.glob("*.json") if hashlib.sha256(p.read_bytes()).hexdigest()==expected)
def verify():
 for s in ([os.lstat(carrier)] if fd is None else [os.lstat(carrier),os.fstat(fd)]):
  assert (s.st_dev,s.st_ino,s.st_uid,stat.S_IMODE(s.st_mode),s.st_nlink,s.st_size)==(16777231,53377467,501,0o600,1,0) and stat.S_ISREG(s.st_mode)
 assert hashlib.sha256(manifest.read_bytes()).hexdigest()==expected
def resources():
 text=subprocess.check_output(["/usr/bin/vm_stat"],text=True,timeout=5);page=int(re.search(r"page size of (\d+)",text)[1]);free=int(re.search(r"Pages free:\s+(\d+)",text)[1])*page;s=os.statvfs(r);disk=s.f_bavail*s.f_frsize
 result.setdefault("resources",[]).append({"free_bytes":free,"disk_bytes":disk,"at_seconds":time.monotonic()-start})
 assert free>=2*1024**3 and disk>=15*1024**3,"Resource admission refused"
def terminate(signum,frame):raise TimeoutError("Absolute execution deadline")
signal.signal(signal.SIGALRM,terminate);signal.signal(signal.SIGTERM,terminate);signal.signal(signal.SIGINT,terminate);signal.setitimer(signal.ITIMER_REAL,540)
exitcode=75
try:
 with (r/"attempt.json").open("x") as output:json.dump({"consumed":True,"pid":os.getpid(),"started_monotonic":start},output)
 resources();verify();fd=os.open(carrier,os.O_RDWR|os.O_NOFOLLOW|os.O_CLOEXEC);verify();result["parent_carrier_fd"]={"pid":os.getpid(),"fd":fd,"role":"parent-held original carrier open-file description","device":os.fstat(fd).st_dev,"inode":os.fstat(fd).st_ino};result["nb_attempts"]=1;fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB);verify();resources()
 (r/"launch.json").write_text(json.dumps({"started_monotonic":start}))
 env={**os.environ,"OWNSOLID_MAC_WINDOW_FD":str(fd)}
 child=subprocess.Popen([sys.executable,str(r/"worker.py")],env=env,pass_fds=(fd,),start_new_session=True)
 result["worker_started"]=True;result["worker_pid"]=child.pid
 while child.poll() is None:
  verify()
  if time.monotonic()-start>535:raise TimeoutError("Execution stopped for cleanup reserve")
  time.sleep(0.2)
 exitcode=child.returncode;result["worker_exit"]=exitcode
except BaseException as error:result["error"]=type(error).__name__+": "+str(error)
finally:
 signal.setitimer(signal.ITIMER_REAL,0)
 if child is not None:
  if child.poll() is None:
   try:os.killpg(child.pid,signal.SIGTERM)
   except ProcessLookupError:pass
   try:child.wait(timeout=5)
   except subprocess.TimeoutExpired:
    os.killpg(child.pid,signal.SIGKILL);child.wait(timeout=5)
  try:os.killpg(child.pid,signal.SIGKILL)
  except ProcessLookupError:pass
  if (r/"worker-result.json").is_file():
   worker=json.loads((r/"worker-result.json").read_text());result["groups_cleared"]=worker.get("all_owned_groups_cleared",False)
 verify()
 if fd is not None:
  os.close(fd)
  try:os.fstat(fd);result["fd_closed"]=False
  except OSError:result["fd_closed"]=True
 result.update(exit=exitcode,elapsed_seconds=round(time.monotonic()-start,3),carrier_identity_preserved=True,manifest_sha256=expected,queued=False)
 (r/"result.json").write_text(json.dumps(result,indent=2)+"\n")
 print(json.dumps(result),flush=True)
raise SystemExit(exitcode)
