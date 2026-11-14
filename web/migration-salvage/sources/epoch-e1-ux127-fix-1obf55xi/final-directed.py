import time
from pathlib import Path
import base64,hashlib,json,os,signal,stat,subprocess,sys
p=Path(__file__).resolve().parent;s=json.loads((p/'gate-state.json').read_text());info=json.loads((p/'preparation.json').read_text());w=Path(info['worktree']);web=w/'web';app=web/'apps/console';deadline=s['workCutoffMonotonic'];carrier=Path(s['carrier']);parent=s['parentPID'];descriptor=s['descriptor'];plan=json.loads((p/'stage-plan.json').read_text());all_results=[]
def budget(cap=600):
 left=deadline-time.clock_gettime(time.CLOCK_MONOTONIC)
 if left<=0:raise TimeoutError('Original admitted work deadline')
 return min(cap,left)
def expired(a,b):raise TimeoutError('Original admitted work cutoff')
signal.signal(signal.SIGALRM,expired);signal.setitimer(signal.ITIMER_REAL,budget())
def save(n,v):(p/n).write_text(json.dumps(v,indent=2)+'\n')
def sha(f):return hashlib.sha256(f.read_bytes()).hexdigest()
def git(*a):return subprocess.check_output(['git',*a],cwd=w,text=True,timeout=budget(3)).strip()
def identity():
 budget();st=carrier.lstat();assert stat.S_ISREG(st.st_mode) and (st.st_dev,st.st_ino,st.st_uid,stat.S_IMODE(st.st_mode),st.st_nlink,st.st_size)==(16777231,53377467,501,0o600,1,0)
 assert sha(carrier.with_name('manifest.json'))=='7fa013a32f542f8ff6467863425e952da5e436b0ad470da25d766613c94eb983'
 fields=subprocess.check_output(['/usr/sbin/lsof','-a','-p',str(parent),'-d',str(descriptor),'-FpfDin'],text=True,timeout=budget(2)).splitlines();assert all(v in fields for v in ['p'+str(parent),'f'+str(descriptor),'D0x100000f','i53377467','n'+str(carrier)]),fields
 assert git('rev-parse','HEAD')==source and not git('status','--porcelain')
 save('final-parent-fd.json',{'parentPID':parent,'sameOriginalDescriptor':descriptor,'lsofFields':fields,'checkedMonotonic':time.clock_gettime(time.CLOCK_MONOTONIC),'noLockAttempt':True})
def cases(suites):
 for suite in suites:
  for spec in suite.get('specs',[]):
   for test in spec.get('tests',[]):yield spec,test
  yield from cases(suite.get('suites',[]))
source=git('rev-parse','HEAD');env={k:v for k,v in os.environ.items() if not k.startswith(('OWNSOLID_LIVE_','OWNSOLID_GW120_')) and k not in ['OWNSOLID_DEV_ORIGIN','NODE_OPTIONS']};env.update(NODE_OPTIONS='--max-old-space-size=512',CI='1',npm_config_offline='true',VITEST_MAX_WORKERS='1',OWNSOLID_PLAYWRIGHT_PORT_OFFSET='26726',PYTHONDONTWRITEBYTECODE='1',DEBUG='pw:webserver')
identity();registry=p/'owned-stage-groups.json';groups=json.loads(registry.read_text());assert os.getpgrp()==os.getpid();groups.append(os.getpgrp());registry.write_text(json.dumps(sorted(set(groups)))+'\n');folder=p/'final';folder.mkdir();save('final-admission.json',{'source':source,'tree':git('rev-parse','HEAD^{tree}'),'webTree':git('rev-parse','HEAD:web'),'parentOriginalFD':descriptor,'guardParentPID':parent,'supervisedPGID':os.getpgrp(),'sameGuardSameCarrierNoNewNB':True,'executorFDInherited':False,'binding':'Original guard continuously holds FD; kernel lsof verifies that exact parent descriptor before and after each command. Registered group belongs to original deadline cleanup.','nodeMiB':512,'workers':1,'retries':0,'originalWorkDeadline':deadline,'scriptSha256':sha(Path(__file__))})
def execute(name,args,cwd,cap=70,expected=None):
 identity();start=time.clock_gettime(time.CLOCK_MONOTONIC);report=expected is not None;out=folder/(name+('.json' if report else '.log'));err=folder/(name+'.stderr.log');save('final-state.json',{'phase':name,'source':source,'remaining':budget()});print('UX127_SUPPLEMENTAL',name,flush=True)
 with out.open('x') as of,err.open('x') as ef:
  child=subprocess.Popen(args,cwd=cwd,env=env,stdin=subprocess.DEVNULL,stdout=of,stderr=ef)
  try:code=child.wait(timeout=budget(cap))
  except subprocess.TimeoutExpired:
   child.terminate()
   try:child.wait(timeout=budget(2))
   except subprocess.TimeoutExpired:child.kill();child.wait(timeout=budget(2))
   code=124
 r={'name':name,'source':source,'argv':args,'cwd':str(cwd),'exit':code,'seconds':time.clock_gettime(time.CLOCK_MONOTONIC)-start,'output':str(out),'stderr':str(err),'pass':code==0}
 if report:
  try:
   d=json.loads(out.read_text());cs=list(cases(d.get('suites',[])));r.update(stats=d.get('stats'),cases=len(cs),globalErrors=d.get('errors',[]),images=[]);r['pass']=code==0 and len(cs)==expected and not d.get('errors') and not any(d['stats'].get(k) for k in ['skipped','flaky','unexpected']) and all(len(t['results'])==1 and t['results'][0].get('retry',0)==0 for sp,t in cs)
   for sp,t in cs:
    for run in t['results']:
     for a in run.get('attachments',[]):
      if a.get('contentType')!='image/png':continue
      raw=base64.b64decode(a['body']) if a.get('body') else Path(a['path']).read_bytes();image_dir=folder/'images';image_dir.mkdir(exist_ok=True);f=image_dir/(name+'-'+str(len(r['images'])+1).zfill(2)+'.png');f.write_bytes(raw);r['images'].append({'name':a['name'],'path':str(f),'sha256':sha(f),'case':sp['title']})
  except Exception as e:r['pass']=False;r['reportError']=repr(e)
 all_results.append(r);save('final-stage-results.json',all_results);identity();return r

execute('api-unit',['pnpm','exec','vitest','run','src/audit.test.ts','--maxWorkers=1'],web/'packages/api',60)
execute('focused-lint',['pnpm','exec','eslint','--config','packages/config/eslint.config.js','--max-warnings','0','packages/api/src/audit.ts','packages/api/src/audit.test.ts','apps/console/src/node-actions.tsx','apps/console/src/resource-page.tsx'],web,60)
build=execute('console-build',['pnpm','run','build'],app,60)
if build['pass']:
 save('final-assets.json',{'source':source,'webTree':git('rev-parse','HEAD:web'),'files':{str(f.relative_to(w)):sha(f) for f in (app/'dist').rglob('*') if f.is_file()}})
 for stage in plan['browsers']:
  if stage['name'] not in ['resource-actions']:continue
  args=['pnpm','exec','playwright','test',*stage['spec'],'--workers=1','--retries=0','--trace=off','--reporter=json','--output='+str(folder/(stage['name']+'-results'))];execute(stage['name'],args,app,stage['timeout'],stage['expected'])
save('final-outcome.json',{'source':source,'stages':all_results,'remaining':budget(),'images':[i for r in all_results for i in r.get('images',[])]})
