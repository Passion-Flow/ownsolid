import time
from pathlib import Path
import hashlib,json,os,re,shlex,signal,subprocess,sys,tarfile
assert sys.flags.isolated and not sys.flags.optimize
root=Path(sys.argv[1]);deadline=float(sys.argv[2]);hard=float(sys.argv[3]);info=json.loads((root/'preparation.json').read_text());work=Path(info['worktree'])
def now():return time.clock_gettime(time.CLOCK_MONOTONIC)
def budget(cap=1800):
 left=deadline-now()
 if left<=0:raise TimeoutError('Absolute work cutoff reached')
 return min(cap,left)
def expired(signum,frame):raise TimeoutError('Absolute work backstop reached')
signal.signal(signal.SIGALRM,expired);signal.setitimer(signal.ITIMER_REAL,budget())
signal.signal(signal.SIGTERM,lambda s,f:sys.exit(143))
def git(*args):return subprocess.check_output(['git',*args],cwd=work,text=True,timeout=budget(5)).strip()
def sha(path):return hashlib.sha256(path.read_bytes()).hexdigest()
def save(name,data):budget();(root/name).write_text(json.dumps(data,indent=2)+'\n')
def assert_source():
 assert git('rev-parse','HEAD')==info['source']
 for p,h in json.loads((root/'source-file-sha256.json').read_text()).items():
  budget();assert sha(work/p)==h,p
assert_source();assert not git('status','--porcelain')
assert not (work/'tools/audit-verify/audit-verify').exists()
assert not os.path.lexists(work/'go.work.sum')
assert not [k for k,v in os.environ.items() if k.startswith('PROJECT_ONE_') and 'DSN' in k and v]
options=os.environ.get('NODE_OPTIONS','');tokens=shlex.split(options);cap=4096
for i,token in enumerate(tokens):
 name,sep,value=token.partition('=');name=name.replace('_','-')
 if name in ['--max-heap-size','--max-old-space-size-percentage']:raise ValueError('Absolute Node heap cap required')
 if name=='--max-old-space-size':
  value=value if sep else tokens[i+1]
  assert value.isascii() and value.isdecimal() and int(value)>0
  cap=min(cap,int(value))
def positive(name,maximum):
 n=int(os.environ.get(name,str(maximum)));assert n>0;return str(min(n,maximum))
env=os.environ.copy();env.update(NODE_OPTIONS=options+' --max-old-space-size='+str(cap),GOPROXY='off',GOSUMDB='off',GOTOOLCHAIN='local',GOMAXPROCS=positive('GOMAXPROCS',2),GOFLAGS=os.environ.get('GOFLAGS','')+' -p=1',CARGO_NET_OFFLINE='true',CARGO_BUILD_JOBS=positive('CARGO_BUILD_JOBS',1),RUST_TEST_THREADS=positive('RUST_TEST_THREADS',1),VITEST_MAX_WORKERS=positive('VITEST_MAX_WORKERS',1),npm_config_offline='true',CI='1',MAKEFLAGS='-j1',PYTHONDONTWRITEBYTECODE='1',OWNSOLID_PLAYWRIGHT_PORT_OFFSET='26726',DEBUG='pw:webserver')
# The original package scripts and Makefile targets remain authoritative. The
# existing pnpm command wrapper adds only offline/resource flags to those scripts.
env['PATH']=str(root/'bin')+':'+env['PATH'];env['OWNSOLID_GATE_PACKET']=str(root)
env['PYTHON']=subprocess.check_output(['uv','python','find','3.12'],text=True,timeout=budget(5)).strip()
save('effective-controls.json',{'nodeOldSpaceMiB':cap,'gomaxprocs':int(env['GOMAXPROCS']),'goParallel':1,'cargoJobs':int(env['CARGO_BUILD_JOBS']),'rustTestThreads':int(env['RUST_TEST_THREADS']),'vitestWorkers':int(env['VITEST_MAX_WORKERS']),'playwrightWorkers':1,'workspaceConcurrency':1,'networkIsolation':'Mac offline tool caches only; no OS namespace or installed acceptance','preexistingAuditBinary':False,'preexistingWorkspaceSum':False})
results=[]
def execute(name,args,cwd=work):
 budget();start=now();(root/'stage').write_text(name+'\n');print('ORIGINAL_FULL_TARGET',name,flush=True)
 with (root/(name+'.log')).open('xb') as out:
  child=subprocess.Popen(args,cwd=cwd,env=env,stdout=out,stderr=subprocess.STDOUT,stdin=subprocess.DEVNULL,umask=0o022,pass_fds=(int(os.environ['OWNSOLID_GATE_FD']),))
  (root/'owned-stage-groups.json').write_text(json.dumps([os.getpgrp()])+'\n')
  try:code=child.wait(timeout=budget())
  except subprocess.TimeoutExpired:raise TimeoutError(name+' exceeded absolute work cutoff') from None
 result={'name':name,'argv':args,'exit':code,'elapsedSeconds':now()-start};results.append(result);save('stage-results.json',results);print(json.dumps(result),flush=True)
 if code:raise RuntimeError(name+' exited '+str(code))
mount=Path('/private/tmp/ownsolid-por112-joint-'+str(os.getpid()))
assert not mount.exists();mount.mkdir(mode=0o700)
save('owned-resources.json',{'mount':str(mount),'image':str(root/'scratch.sparseimage'),'source':info['source'],'auditBinaryCreatedOnlyAfterBuild':True,'workspaceSumAbsentAtEntry':True})
execute('scratch-create',['hdiutil','create','-size','12g','-type','SPARSE','-fs','APFS','-volname','ownsolid-por112-joint',str(root/'scratch')])
execute('scratch-attach',['hdiutil','attach','-nobrowse','-owners','on','-mountpoint',str(mount),str(root/'scratch.sparseimage')])
(mount/'tmp').mkdir(mode=0o700);env['TMPDIR']=str(mount/'tmp')+'/'
for target in ['web-install','check','build','test']:
 assert_source();execute(target,['make',target])
assert_source()
assets={}
for app in ['console','portal']:
 files={str(p.relative_to(work)):sha(p) for p in sorted((work/'web/apps'/app/'dist').rglob('*')) if p.is_file()}
 assert files;assets[app]=files
save('full-built-assets.json',{'source':info['source'],'tree':info['tree'],'webTree':info['webTree'],'files':assets})
with tarfile.open(root/'full-built-assets.tar.gz','x:gz') as archive:
 for app,files in assets.items():
  for p in files:budget();archive.add(work/p,arcname=p,recursive=False)
save('full-asset-archive.json',{'sha256':sha(root/'full-built-assets.tar.gz'),'files':sum(map(len,assets.values())),'source':info['source']})
(root/'stage').write_text('complete\n')
