from pathlib import Path
import os,fcntl,stat,subprocess,hashlib,json,signal,time,sys
r=Path('/root/lane-rel-tools/identity-audit-contract');w=Path('/root/lane-rel-identity-audit-contract');fds=[];child=None
proof={'contract_sha256':'7fce16936a778912b2ea21d3f70e653aed1d6584d7210de8dccc72c0de4acb32','source':subprocess.check_output(['git','rev-parse','HEAD'],cwd=w,text=True).strip(),'codec_input_source':'2426699bfad4c1a4e4ee6999cfc14f264c4cda12','run':False,'passed':False,'actual_backend_or_installed_run':False,'cli_b967d7c8_qualified':False,'locks':[]};exit_code=75

def resources():
 m=int(next(x.split()[1] for x in Path('/proc/meminfo').read_text().splitlines() if x.startswith('MemFree:')))//1024;proof['raw_memfree_mib']=m;v=os.statvfs('/root')
 if m<2048 or v.f_bavail*v.f_frsize<15*1024**3:raise RuntimeError('Resource threshold not met')
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
 for n in ['/run/lock/ownsolid-heavy.lock','/root/lane-sci-tools/heavy-test.lock','/root/lane-rel-tools/heavy-test.lock']:
  p=Path(n);fd=os.open(p,os.O_RDWR|os.O_NOFOLLOW|os.O_NONBLOCK);fds.append(fd);s=os.fstat(fd);assert stat.S_ISREG(s.st_mode) and s.st_uid==os.geteuid();assert(s.st_dev,s.st_ino)==(p.stat().st_dev,p.stat().st_ino)
  proof['attempted_lock']=n;fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB);proof['locks'].append(n)
 resources();proof['admitted']=True
 assert hashlib.sha256((w/'api/openapi/v1/identity/openapi.json').read_bytes()).hexdigest()==proof['contract_sha256']
 generated=json.loads((r/'generation.json').read_text());assert generated['input_contract_sha256']==proof['contract_sha256']
 for n,expected in generated['file_sha256'].items():assert hashlib.sha256((r/'sdk'/n).read_bytes()).hexdigest()==expected
 env=os.environ.copy();env.update(GOPROXY='off',GOSUMDB='off',GOTOOLCHAIN='local',GOWORK='off',GOMAXPROCS='2',GOFLAGS='-p=1 -mod=readonly',CGO_ENABLED='1')
 proof['go_version']=subprocess.check_output(['go','version'],env=env,text=True).strip();proof['wire_source_sha256']=hashlib.sha256((r/'wire/go/audit_wire_test.go').read_bytes()).hexdigest()
 with (r/'go-wire.log').open('w') as log:
  child=subprocess.Popen(['unshare','--net','bash','--noprofile','--norc','-euc','ip link set lo up\nexec go test -json -race -count=1 ./...'],cwd=r/'wire/go',env=env,stdout=log,stderr=subprocess.STDOUT,stdin=subprocess.DEVNULL,pass_fds=tuple(fds),start_new_session=True)
  code=child.wait(timeout=120);child=None
 proof['run']=True;proof['test_exit']=code
 events=[]
 for line in (r/'go-wire.log').read_text().splitlines():
  try:events.append(json.loads(line))
  except ValueError:pass
 roots=[x['Test'] for x in events if x.get('Action')=='pass' and x.get('Test') and '/' not in x['Test']]
 proof['passed_top_level_tests']=roots;proof['skipped_tests']=[x.get('Test') for x in events if x.get('Action')=='skip'];proof['log_sha256']=hashlib.sha256((r/'go-wire.log').read_bytes()).hexdigest();proof['passed']=code==0 and len(roots)==4 and not proof['skipped_tests'];exit_code=0 if proof['passed'] else 1
except BlockingIOError:proof['failure']='Shared file lock busy before compilation'
except BaseException as error:
 proof['failure']=type(error).__name__+': '+str(error)
 if proof.get('admitted'):exit_code=1
finally:
 stop()
 for fd in reversed(fds):os.close(fd)
 proof['exit']=exit_code;(r/'go-wire-proof.json').write_text(json.dumps(proof,indent=2)+'\n');(r/'go-wire.exit').write_text(str(exit_code)+'\n')
print(json.dumps(proof,indent=2));sys.exit(exit_code)
