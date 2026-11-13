import datetime,fcntl,hashlib,json,os,re,resource,select,signal,stat,subprocess,sys,time
from pathlib import Path
r=Path(__file__).resolve().parent
plan=json.loads((r/"plan.json").read_text());root=Path("/Users/passion./.local/state/ownsolid/lane-rel/strict-contract-json/work")
start=39988.72874075;deadline=start+525;cleanup_deadline=start+600;full_start=None;fd=None;active=None;phase="sdk";exitcode=1
proof={"joint_source":plan["source"],"joint_tree":plan["tree"],"started_utc":datetime.datetime.now(datetime.timezone.utc).isoformat(),"started_monotonic":start,"commands":[],"owned_groups":[],"nb_attempts":0,"full_stage":"NOTRUN","repairs":[],"reused_go_migration":"Original 23 roots/20 subcases, not rerun"}
carrier=Path("/Users/passion./.local/state/ownsolid/coordination/workstation/ownsolid-2-20261113-e1/full-gate.lock");manifest_sha="7fa013a32f542f8ff6467863425e952da5e436b0ad470da25d766613c94eb983"
manifest=next(p for p in carrier.parent.glob("*.json") if hashlib.sha256(p.read_bytes()).hexdigest()==manifest_sha)
def sha(p):return hashlib.sha256(Path(p).read_bytes()).hexdigest()
def identity():
 for s in ([os.lstat(carrier)] if fd is None else [os.lstat(carrier),os.fstat(fd)]):
  assert (s.st_dev,s.st_ino,s.st_uid,stat.S_IMODE(s.st_mode),s.st_nlink,s.st_size)==(16777231,53377467,501,0o600,1,0) and stat.S_ISREG(s.st_mode),"Carrier identity changed"
 assert sha(manifest)==manifest_sha,"Manifest changed"
def git(*args):return subprocess.check_output(["git","-c","core.commitGraph=false",*args],cwd=root,text=True,timeout=10).strip()
def resources(label,free_gib,disk_gib,full=False):
 text=subprocess.check_output(["/usr/bin/vm_stat"],text=True,timeout=5);page=int(re.search(r"page size of (\d+)",text)[1]);free=int(re.search(r"Pages free:\s+(\d+)",text)[1])*page;s=os.statvfs(r);disk=s.f_bavail*s.f_frsize
 report={"label":label,"free_bytes":free,"disk_bytes":disk,"elapsed":round(time.monotonic()-start,3)}
 if full:
  pressure=subprocess.check_output(["/usr/bin/memory_pressure","-Q"],text=True,timeout=5);match=re.search(r"free percentage:\s*(\d+)%",pressure);report["pressure_free_percent"]=int(match[1]) if match else None
 proof.setdefault("resources",[]).append(report);print(json.dumps({"resource":report}),flush=True)
 return free>=free_gib*1024**3 and disk>=disk_gib*1024**3 and (not full or report["pressure_free_percent"] is not None and report["pressure_free_percent"]>=40)
def alarm(signum,frame):raise TimeoutError("Fixed execution deadline reached; cleanup reserve retained")
def stop_group(pgid):
 end=min(cleanup_deadline,time.monotonic()+8)
 while True:
  try:os.killpg(pgid,0)
  except ProcessLookupError:return
  try:os.killpg(pgid,signal.SIGKILL)
  except ProcessLookupError:return
  if time.monotonic()>=end:raise RuntimeError("Owned group remains after bounded cleanup")
  time.sleep(0.05)
def run_step(name,argv,cwd,env,profile,limit=120):
 global active
 identity();assert active is None;remaining=deadline-time.monotonic()-8
 if remaining<=0:raise TimeoutError("No command execution budget remains")
 index=len(proof["commands"])+1;item={"name":name,"phase":phase,"source":git("rev-parse","HEAD"),"argv":argv,"cwd":str(cwd),"profile":str(profile),"profile_sha256":sha(profile),"timeout_seconds":min(limit,remaining),"started_utc":datetime.datetime.now(datetime.timezone.utc).isoformat()};proof["commands"].append(item)
 out=r/f"command-{index:03d}.stdout";err=r/f"command-{index:03d}.stderr";item.update(stdout_log=str(out),stderr_log=str(err));print(json.dumps({"command":index,"name":name,"phase":phase,"budget":item["timeout_seconds"]}),flush=True)
 started=time.monotonic();peak=0
 with out.open("xb") as output,err.open("xb") as errors:
  active=subprocess.Popen(["/usr/bin/sandbox-exec","-f",str(profile),*argv],cwd=cwd,env=env,stdin=subprocess.DEVNULL,stdout=output,stderr=errors,start_new_session=True,pass_fds=(fd,))
  item["pid"]=active.pid;item["pgid"]=active.pid;pid_start=subprocess.check_output(["/bin/ps","-p",str(active.pid),"-o","lstart="],text=True,timeout=3).strip();item["os_pid_start"]=pid_start;proof["owned_groups"].append({"pgid":active.pid,"pid_start":pid_start,"cleared":False});group=proof["owned_groups"][-1]
  try:
   next_sample=0
   while True:
    identity();pid,status,usage=os.wait4(active.pid,os.WNOHANG)
    if pid:active.returncode=os.waitstatus_to_exitcode(status);item.update(exit=active.returncode,wait4_maxrss_bytes=usage.ru_maxrss);break
    now=time.monotonic()
    if now-started>=item["timeout_seconds"]:raise TimeoutError("Command deadline")
    if now>=next_sample:
     ps=subprocess.check_output(["/bin/ps","-axo","pgid=,rss="],text=True,timeout=3);peak=max(peak,sum(int(x[1])*1024 for line in ps.splitlines() if len(x:=line.split())==2 and int(x[0])==active.pid));next_sample=now+0.5
    time.sleep(0.05)
  finally:
   if active.returncode is None:
    try:os.killpg(active.pid,signal.SIGKILL)
    except ProcessLookupError:pass
    _,status,usage=os.wait4(active.pid,0);active.returncode=os.waitstatus_to_exitcode(status);item.update(exit=active.returncode,wait4_maxrss_bytes=usage.ru_maxrss)
   stop_group(active.pid);group["cleared"]=True;active=None;identity()
 item.update(seconds=round(time.monotonic()-started,3),group_peak_rss_bytes=peak,stdout_sha256=sha(out),stderr_sha256=sha(err));print(json.dumps({"completed":name,"exit":item["exit"],"seconds":item["seconds"]}),flush=True)
 if item["exit"]:print(err.read_text()[-4500:]+out.read_text()[-1500:],flush=True)
 return item

def checked(name,step,profile,env,limit=120):
 result=run_step(name,step["argv"],step["cwd"],env,profile,limit)
 while result["exit"]:
  print("REPAIR_REQUIRED: send a JSON repair step with changed_inputs, reason, argv, cwd; or stop. Deadline is unchanged.",flush=True)
  request=None
  while time.monotonic()<deadline-10:
   ready,_,_=select.select([sys.stdin],[],[],min(1,max(0,deadline-time.monotonic()-10)))
   if ready:
    line=sys.stdin.readline()
    if not line:raise RuntimeError("No repair input; original failed exit retained")
    request=json.loads(line);break
  if request is None or request.get("action")=="stop":raise RuntimeError("Stage stopped after failed command")
  changed=request.get("changed_inputs",{});assert request.get("reason") and changed,"No blind command retry"
  for path,pin in changed.items():assert sha(path)==pin
  proof["repairs"].append(request);result=run_step(name+"-directed-repair",request["argv"],request["cwd"],env,profile,limit)
 return result

signal.signal(signal.SIGALRM,alarm);signal.signal(signal.SIGTERM,alarm);signal.signal(signal.SIGINT,alarm);signal.setitimer(signal.ITIMER_REAL,max(0.1,deadline-time.monotonic()))
try:
 with (r/"attempt.json").open("x") as stream:json.dump({"consumed":True,"pid":os.getpid(),"original_window_start":start,"new_source":plan["source"]},stream)
 assert git("rev-parse","HEAD")==plan["source"] and git("rev-parse","HEAD^{tree}")==plan["tree"] and not git("status","--porcelain")
 prior=json.loads((r.parent/"result.json").read_text());assert prior["controller_fd_closed"] and prior["all_owned_groups_cleared"]
 assert resources("directed-repair-before-carrier",2,15);identity();fd=os.open(carrier,os.O_RDWR|os.O_NOFOLLOW|os.O_CLOEXEC);identity();proof["nb_attempts"]=1;fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB);identity();proof["controller_carrier_fd"]={"pid":os.getpid(),"fd":fd,"device":os.fstat(fd).st_dev,"inode":os.fstat(fd).st_ino,"role":"directed coverage-repair controller within the unchanged original fixed window"}
 for rel,pin in plan["generation_36_inputs_sha256"].items():assert sha(root/rel)==pin
 for rel,pin in plan["unchanged_qualified_go_fixtures"].items():assert sha(root/rel)==pin
 step=json.loads((r/"bootstrap.json").read_text());assert sha(r/"wire_smoke.py")==step["harness_sha256"]
 (r/"tmp").mkdir();env={**os.environ,**plan["next_sdk_commands"][1]["environment"],"TMPDIR":str(r/"tmp")+"/","PYTHONPYCACHEPREFIX":str(r/"unused-bytecode"),"PYTHONDONTWRITEBYTECODE":"1"}
 proof["previous_stage1_receipt"]=str(r.parent/"result.json");proof["directed_repair_reason"]="Add omitted Python undeclared-status coverage; revalidate only the changed protocol function with 409 and 418. No original Go build/import-all/client/122-command rerun."
 item=run_step("directed-python-409-and-undeclared-status",step["argv"],r,env,r/"python-sdk.sb",60);assert item["exit"]==0
 events=[json.loads(line) for line in Path(item["stdout_log"]).read_text().splitlines() if line.startswith("{")];cases=[entry for entry in events if entry.get("check")=="python_audit_tcp"];assert len(cases)==16 and {entry["first_status"] for entry in cases}=={409,418} and all(entry["exit"]==0 and entry["requests"]==2 for entry in cases)
 proof["python_tcp_cases"]=cases;proof["full_stage"]="NOTRUN_RESOURCE_REFUSED_IN_ORIGINAL_ATTEMPT";proof["repairs"]=[{"source":plan["source"],"harness_sha256":step["harness_sha256"],"scope":"changed Python protocol function only"}];proof["original_deadline_not_reset"]=True
 exitcode=0

except BaseException as error:
 proof["error"]=type(error).__name__+": "+str(error);print(json.dumps({"error":proof["error"]}),flush=True)
finally:
 signal.setitimer(signal.ITIMER_REAL,0)
 if active is not None:
  try:os.killpg(active.pid,signal.SIGKILL);active.wait(timeout=5);stop_group(active.pid)
  except ProcessLookupError:pass
 if fd is not None:
  identity();os.close(fd)
  try:os.fstat(fd);proof["controller_fd_closed"]=False
  except OSError:proof["controller_fd_closed"]=True
 proof.update(exit=exitcode,elapsed_seconds=round(time.monotonic()-start,3),cleanup_completed_utc=datetime.datetime.now(datetime.timezone.utc).isoformat(),all_owned_groups_cleared=all(x["cleared"] for x in proof["owned_groups"]),carrier_identity_preserved=True,manifest_sha256=manifest_sha,parent_peak_rss_bytes=resource.getrusage(resource.RUSAGE_SELF).ru_maxrss,queued=False)
 (r/"result.json").write_text(json.dumps(proof,indent=2)+"\n");print(json.dumps({key:value for key,value in proof.items() if key not in {"commands","owned_groups","go_sdk_packages"}}),flush=True)
raise SystemExit(exitcode)
