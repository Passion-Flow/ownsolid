import os,fcntl,stat,subprocess,json,time,signal,sys,hashlib,ctypes,shlex
from pathlib import Path
os.umask(0o077)
r=Path('/root/lane-rel-tools/api-compat');w=Path('/root/lane-rel-identity-audit-contract');source='9be01cbbf0144a2f2537667834539de31e40f58f'
locks=[Path('/run/lock/ownsolid-heavy.lock'),Path('/root/lane-sci-tools/heavy-test.lock')]
proof={'source':source,'base':'9e32b023a0a3deed6a8653a8878d52c2583fd6cf','host':'linux/amd64','check':False,'build':False,'whole_make_test':False,'cli_b967d7c8_qualified':False,'installed_acceptance':False,'locks':[]}
fds=[];child=None;group=None;seen=set();started=time.monotonic();exit_code=1
deadline=started+1200;execution_deadline=deadline-20;finishing=False
proof['absolute_seconds_cap']=1200;proof['cleanup_reserve_seconds']=20;proof['owned_process_groups']=[]
class AdmissionBusy(Exception):pass
class ResourceRefused(Exception):pass
class GateTimeout(Exception):pass
proof['entry_scope']='Exact original-author audit/SDK/tool and Web projection joint source; web-install/check/build/test. Strict live admission is required.'
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
 proof.setdefault('resource_snapshots',[]).append({'elapsed_seconds':round(time.monotonic()-started,3),**{name:proof[name] for name in ['raw_memfree_mib','memavailable_mib','disk_free_bytes','io_full_avg10']}})
 if values['MemFree']<2048*1024 or values['MemAvailable']<8192*1024 or proof['disk_free_bytes']<30*1024**3 or avg10>5:raise ResourceRefused('Strict resource threshold not met')
def save():
 if proof.get('evidence_already_exists'):return
 proof['elapsed_seconds']=round(time.monotonic()-started,3);(r/'joint-native-gate-9be01cbb-proof.json').write_text(json.dumps(proof,indent=2)+'\n')
def budget(cleanup=False):
 remaining=(deadline-8 if cleanup else execution_deadline)-time.monotonic()
 if remaining<=0:raise GateTimeout('Absolute gate budget exhausted')
 return remaining
def reap_group():
 if child is not None:child.poll()
 while group is not None:
  try:pid,status=os.waitpid(-1,os.WNOHANG)
  except ChildProcessError:break
  if pid==0:break
  if child is not None and pid==child.pid:child.returncode=os.waitstatus_to_exitcode(status)
def owned_children():
 return [int(pid) for pid in Path('/proc/self/task/'+str(os.getpid())+'/children').read_text().split()]
def group_alive():
 reap_group()
 try:os.killpg(group['pgid'],0);exists=True
 except ProcessLookupError:exists=False
 return exists or bool(owned_children())
def signal_owned(signum):
 try:os.killpg(group['pgid'],signum)
 except ProcessLookupError:pass
 # A detached orphan is still our child after subreaper adoption; never signal its unrelated group.
 for pid in owned_children():
  try:os.kill(pid,signum)
  except ProcessLookupError:pass
def stop():
 global child,group
 if group is None:return
 if group_alive():
  if 'cleanup_deadline' not in group:group['cleanup_deadline']=min(deadline-2,time.monotonic()+6)
  if not group.get('term_sent'):
   signal_owned(signal.SIGTERM)
   group['term_sent']=True;group['term_deadline']=min(group['cleanup_deadline'],time.monotonic()+3)
  while group_alive() and time.monotonic()<group['term_deadline']:
   time.sleep(max(0,min(0.05,group['term_deadline']-time.monotonic())))
  if group_alive() and not group.get('kill_sent'):
   signal_owned(signal.SIGKILL)
   group['kill_sent']=True
  while group_alive() and time.monotonic()<group['cleanup_deadline']:
   signal_owned(signal.SIGKILL)
   time.sleep(max(0,min(0.05,group['cleanup_deadline']-time.monotonic())))
 if group_alive():raise GateTimeout('Owned process group did not exit within cleanup budget')
 group['group_cleared']=True;group['owned_children_remaining']=0;group['inherited_lock_holders_remaining']=0
 group['leader_exit']=child.returncode;child=None;group=None
def spawn(command,phase,**options):
 global child,group
 assert child is None and group is None
 child=subprocess.Popen(command,start_new_session=True,**options)
 group={'pgid':child.pid,'phase':phase,'inherited_lock_fd_count':len(options.get('pass_fds',())),'group_cleared':False}
 proof['owned_process_groups'].append(group)
def metadata(command,phase,cleanup=False):
 remaining=budget(cleanup);proof['phase']=phase
 spawn(command,phase,cwd=w,stdin=subprocess.DEVNULL,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
 try:
  try:output,error=child.communicate(timeout=remaining)
  except subprocess.TimeoutExpired:raise GateTimeout('Metadata command exceeded remaining budget') from None
  if child.returncode:raise RuntimeError('Metadata command failed in '+phase)
  return output
 finally:stop()
def positive_cap(environment,name,maximum):
 value=int(environment.get(name,str(maximum)))
 if value<=0:raise ValueError('Invalid resource limit: '+name)
 return str(min(value,maximum))
def node_options(value):
 tokens=shlex.split(value);maximum=2048
 for index,token in enumerate(tokens):
  name,separator,amount=token.partition('=')
  if name.replace('_','-')!='--max-old-space-size':continue
  amount=amount if separator else tokens[index+1]
  if int(amount)<=0:raise ValueError('Invalid Node heap limit')
  maximum=min(maximum,int(amount))
 return value+' --max-old-space-size='+str(maximum)
def expired(signum,frame):
 if finishing:
  if group is not None:
   signal_owned(signal.SIGKILL)
  os.write(2,b'NATIVE_GATE_ABSOLUTE_DEADLINE: cleanup not proven; exit124\n');os._exit(124)
 raise GateTimeout('Execution deadline reached; cleanup reserve retained')
def interrupted(signum,frame):raise KeyboardInterrupt('Interrupted')
signal.signal(signal.SIGTERM,interrupted);signal.signal(signal.SIGINT,interrupted)
signal.signal(signal.SIGALRM,expired);signal.setitimer(signal.ITIMER_REAL,max(0.001,execution_deadline-time.monotonic()))
try:
 if (r/'joint-native-gate-9be01cbb-proof.json').exists():
  proof['evidence_already_exists']=True
  raise RuntimeError('The single joint-source attempt already has evidence; do not overwrite or retry it')
 if sys.flags.optimize:raise RuntimeError('Gate requires source and ownership assertions enabled')
 # Reap orphaned descendants from our own targets before declaring their group empty.
 PR_SET_CHILD_SUBREAPER=36
 if ctypes.CDLL(None,use_errno=True).prctl(PR_SET_CHILD_SUBREAPER,1,0,0,0)!=0:raise RuntimeError('Cannot reap owned descendant processes')
 resources()
 assert metadata(['git','rev-parse','HEAD'],'source-head').strip()==source
 assert metadata(['git','status','--porcelain'],'source-clean')==''
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
 env.update(PATH=str(bindir)+':'+env['PATH'],GOPROXY='off',GOSUMDB='off',GOTOOLCHAIN='local',GOMAXPROCS=positive_cap(env,'GOMAXPROCS',2),GOFLAGS=env.get('GOFLAGS','')+' -p=1',CARGO_BUILD_JOBS=positive_cap(env,'CARGO_BUILD_JOBS',1),CARGO_NET_OFFLINE='true',CARGO_TARGET_DIR=str(r/'native-rust-target'),NODE_OPTIONS=node_options(env.get('NODE_OPTIONS','')),PYTHONDONTWRITEBYTECODE='1',MAKEFLAGS='-j1',PLAYWRIGHT_BROWSERS_PATH='/root/lane-rel-tools/playwright-browsers')
 env['LD_LIBRARY_PATH']='/root/lane-rel-tools/playwright-libs.tUcBz3Xq/sysroot/usr/lib/x86_64-linux-gnu'
 for target in ['web-install','check','build','test']:
  resources()
  remaining=budget();proof['phase']=target
  print('NATIVE_GATE_TARGET',target,flush=True)
  spawn(['unshare','--net','bash','--noprofile','--norc','-euc','ip link set lo up\numask 022\nexec make '+target],target,cwd=w,env=env,stdin=subprocess.DEVNULL,pass_fds=tuple(fds))
  save()
  try:
   try:code=child.wait(timeout=remaining)
   except subprocess.TimeoutExpired:raise GateTimeout('Target exceeded remaining execution budget') from None
  finally:stop()
  proof[target+'_exit']=code;proof[target]=code==0
  if target=='test':proof['whole_make_test']=code==0
  save()
  if code:raise RuntimeError(target+' exited '+str(code))
 if not preexisting and audit.exists():
  assert stat.S_ISREG(audit.lstat().st_mode) and audit.stat().st_uid==os.geteuid();audit.unlink()
 cleanup_workspace_sum()
 proof['source_clean_after']=metadata(['git','status','--porcelain'],'source-clean-after')==''
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
 finishing=True;signal.signal(signal.SIGTERM,signal.SIG_IGN);signal.signal(signal.SIGINT,signal.SIG_IGN)
 signal.setitimer(signal.ITIMER_REAL,max(0.001,deadline-time.monotonic()))
 try:stop()
 except BaseException as error:proof['process_cleanup_failure']=type(error).__name__;proof['passed']=False;exit_code=1
 if group is None:
  if not preexisting and audit.exists():
   try:
    assert stat.S_ISREG(audit.lstat().st_mode) and audit.stat().st_uid==os.geteuid();audit.unlink()
   except BaseException as error:proof['cleanup_failure']=type(error).__name__+': '+str(error);proof['passed']=False;exit_code=1
  try:cleanup_workspace_sum()
  except BaseException as error:proof['workspace_sum_cleanup_failure']=type(error).__name__+': '+str(error);proof['passed']=False;exit_code=1
  try:proof['source_clean_after']=metadata(['git','status','--porcelain'],'final-source-clean',cleanup=True)==''
  except BaseException as error:proof['source_cleanup_check_failure']=type(error).__name__;proof['passed']=False;exit_code=1
 proof['all_owned_process_groups_cleared']=group is None and all(row['group_cleared'] for row in proof['owned_process_groups'])
 if not proof['all_owned_process_groups_cleared']:proof['passed']=False;exit_code=1
 for fd in reversed(fds) if proof['all_owned_process_groups_cleared'] else []:
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
signal.setitimer(signal.ITIMER_REAL,0)
sys.exit(exit_code)
