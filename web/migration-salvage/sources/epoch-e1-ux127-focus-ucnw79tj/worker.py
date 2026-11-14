import time
from pathlib import Path
import base64,hashlib,json,os,re,signal,stat,subprocess,sys
assert sys.flags.isolated and not sys.flags.optimize
root=Path(sys.argv[1]);deadline=float(sys.argv[2]);info=json.loads((root/'preparation.json').read_text());work=Path(info['worktree']);web=work/'web';app=web/'apps/console';fd=int(os.environ['OWNSOLID_GATE_FD']);carrier=Path('/Users/passion./.local/state/ownsolid/coordination/workstation/ownsolid-2-20261113-e1/full-gate.lock');plan=json.loads((root/'stage-plan.json').read_text());results=[];round_number=0;groups=[os.getpgrp()]
def budget(cap=600):
 left=deadline-time.clock_gettime(time.CLOCK_MONOTONIC)
 if left<=0:raise TimeoutError('Absolute work cutoff')
 return min(cap,left)
def expired(a,b):raise TimeoutError('Absolute work cutoff')
signal.signal(signal.SIGALRM,expired);signal.setitimer(signal.ITIMER_REAL,budget())
def save(n,v):budget();(root/n).write_text(json.dumps(v,indent=2)+'\n')
def sha(f):budget();return hashlib.sha256(f.read_bytes()).hexdigest()
def git(*a):return subprocess.check_output(['git',*a],cwd=work,text=True,timeout=budget(4)).strip()
def identity():
 budget();assert sha(carrier.with_name('manifest.json'))=='7fa013a32f542f8ff6467863425e952da5e436b0ad470da25d766613c94eb983'
 for s in [os.fstat(fd),carrier.lstat()]:assert stat.S_ISREG(s.st_mode) and (s.st_dev,s.st_ino,s.st_uid,stat.S_IMODE(s.st_mode),s.st_nlink,s.st_size)==(16777231,53377467,501,0o600,1,0)
def cases(suites):
 for suite in suites:
  for sp in suite.get('specs',[]):
   for t in sp.get('tests',[]):yield sp,t
  yield from cases(suite.get('suites',[]))
env={k:v for k,v in os.environ.items() if not k.startswith(('OWNSOLID_LIVE_','OWNSOLID_GW120_')) and k not in ['NODE_OPTIONS','OWNSOLID_DEV_ORIGIN','TMOUT','PS1']};env.update(NODE_OPTIONS='--max-old-space-size=512',CI='1',npm_config_offline='true',VITEST_MAX_WORKERS='1',OWNSOLID_PLAYWRIGHT_PORT_OFFSET='26726',PYTHONDONTWRITEBYTECODE='1',DEBUG='pw:webserver')
assert git('rev-parse','HEAD')==info['source'] and not git('status','--porcelain')
for f,h in info['sourceFiles'].items():assert sha(work/f)==h,f
identity();save('effective-controls.json',{'NodeOldSpaceMiB':512,'workers':1,'retries':0,'offlineDependencies':True,'sameInheritedFD':fd,'separateStageGroups':True,'commandCapsUnchanged':True,'noFullNativeInstalled':True,'workCutoff':deadline})
def execute(stage):
 identity();name=stage['name'];folder=root/('round-'+str(round_number));folder.mkdir(exist_ok=True);args=list(stage['args']);report=stage.get('expected') is not None
 if report:
  args+=['--workers=1','--retries=0','--trace=off','--reporter=json','--output='+str(folder/(name+'-results'))]
 output=folder/(name+('.json' if report else '.log'));error=folder/(name+'.stderr.log');source=git('rev-parse','HEAD');save('worker-state.json',{'phase':name,'round':round_number,'source':source,'remaining':budget()});print('UX127_FOCUS',round_number,name,flush=True);started=time.clock_gettime(time.CLOCK_MONOTONIC)
 with output.open('x') as out,error.open('x') as err:
  child=subprocess.Popen(args,cwd=work/stage['cwd'],env=env,stdin=subprocess.DEVNULL,stdout=out,stderr=err,pass_fds=(fd,),start_new_session=True);groups.append(child.pid);save('owned-groups.json',groups)
  stage_end=started+stage['timeout']
  code=None
  while code is None:
   rows=[line.split() for line in subprocess.check_output(['ps','-axo','pid=,ppid=,pgid=,uid='],text=True,timeout=budget(2)).splitlines() if line.strip()]
   owned={child.pid}
   for _ in rows:
    added={int(r[0]) for r in rows if int(r[1]) in owned}-owned
    if not added:break
    owned.update(added)
   for row in rows:
    if int(row[0]) in owned:
     assert int(row[3])==501
     group=int(row[2])
     if group not in groups:groups.append(group)
   save('owned-groups.json',groups)
   remaining=min(stage_end,deadline)-time.clock_gettime(time.CLOCK_MONOTONIC)
   if remaining<=0:
    try:os.killpg(child.pid,signal.SIGTERM)
    except ProcessLookupError:pass
    try:child.wait(timeout=budget(2))
    except subprocess.TimeoutExpired:
     try:os.killpg(child.pid,signal.SIGKILL)
     except ProcessLookupError:pass
     child.wait(timeout=budget(2))
    code=124
   else:
    try:code=child.wait(timeout=min(.5,remaining))
    except subprocess.TimeoutExpired:pass
 result={'name':name,'round':round_number,'source':source,'webTree':git('rev-parse','HEAD:web'),'argv':args,'cwd':stage['cwd'],'exit':code,'seconds':time.clock_gettime(time.CLOCK_MONOTONIC)-started,'output':str(output),'stderr':str(error),'pass':code==0}
 if report:
  try:
   data=json.loads(output.read_text());selected=list(cases(data.get('suites',[])));result.update(stats=data.get('stats'),cases=len(selected),errors=data.get('errors',[]),images=[]);result['pass']=code==0 and len(selected)==stage['expected'] and not data.get('errors') and not any(data['stats'].get(k) for k in ['unexpected','skipped','flaky']) and all(len(t['results'])==1 and t['results'][0].get('retry',0)==0 for sp,t in selected)
   for sp,t in selected:
    for run in t['results']:
     for a in run.get('attachments',[]):
      if a.get('contentType')!='image/png':continue
      raw=base64.b64decode(a['body']) if a.get('body') else Path(a['path']).read_bytes();image_dir=folder/'images';image_dir.mkdir(exist_ok=True);f=image_dir/(name+'-'+str(len(result['images'])+1).zfill(2)+'.png');f.write_bytes(raw);result['images'].append({'name':a['name'],'path':str(f),'sha256':sha(f),'case':sp['title']})
  except Exception as e:result['pass']=False;result['reportError']=repr(e)
 results.append(result);save('stage-results.json',results);identity();assert git('rev-parse','HEAD')==source and not git('status','--porcelain');return result
requested=[s['name'] for s in plan['stages']]
while True:
 round_number+=1;assert not git('status','--porcelain');paths=sorted(set(info['formatPaths']+[p for p in git('diff','--name-only',info['source'],'HEAD').splitlines() if p.startswith('web/')]))
 assert all(p.startswith(('web/','docs/research/')) for p in paths)
 formatter={'name':'format-owned','args':['pnpm','exec','prettier','--write',*[str(work/p) for p in paths]],'cwd':'web','timeout':60}
 # Formatting is authoring; source is frozen before validation begins.
 identity();folder=root/('round-'+str(round_number));folder.mkdir(exist_ok=True)
 with (folder/'format.log').open('x') as log:
  f=subprocess.run(formatter['args'],cwd=web,env=env,stdout=log,stderr=subprocess.STDOUT,pass_fds=(fd,),timeout=budget(60));assert f.returncode==0
 changed=git('diff','--name-only').splitlines();assert set(changed)<=set(paths)
 if changed:
  subprocess.run(['git','add','--',*changed],cwd=work,check=True,timeout=budget(4));subprocess.run(['git','commit','-m','UX-127 Format the frozen focus and layout candidate','-m','Format owned inputs before source-bound directed validation.'],cwd=work,check=True,timeout=budget(4))
 assert not git('status','--porcelain');frozen={'source':git('rev-parse','HEAD'),'tree':git('rev-parse','HEAD^{tree}'),'webTree':git('rev-parse','HEAD:web'),'inputs':{p:sha(work/p) for p in git('ls-files').splitlines() if (work/p).is_file()}};save('frozen-round'+str(round_number)+'.json',frozen)
 build_ok=False
 for stage in plan['stages']:
  if stage['name'] not in requested:continue
  if stage.get('expected') is not None and not build_ok:continue
  result=execute(stage)
  if stage['name']=='build':
   build_ok=result['pass']
   if build_ok:save('assets-round'+str(round_number)+'.json',{'source':frozen['source'],'webTree':frozen['webTree'],'files':{str(p.relative_to(work)):sha(p) for p in (app/'dist').rglob('*') if p.is_file()}})
 current=[r for r in results if r['round']==round_number];images=[i for r in current for i in r.get('images',[])];save('review-round'+str(round_number)+'.json',{'source':frozen['source'],'results':current,'images':images,'allPass':len(current)==len(requested) and all(r['pass'] for r in current),'remaining':budget()});save('worker-state.json',{'phase':'image-review','round':round_number,'source':frozen['source'],'images':len(images),'remaining':budget()});print('UX127_REVIEW',round_number,len(images),flush=True)
 control=root/('control-round'+str(round_number)+'.json')
 while not control.exists():budget();time.sleep(min(.1,budget(.1)))
 decision=json.loads(control.read_text())
 if decision['action']=='finish':
  assert git('rev-parse','HEAD')==frozen['source'] and not git('status','--porcelain');read=decision.get('readImages',[]);assert set(read)=={i['path'] for i in images};save('worker-outcome.json',{'source':frozen['source'],'round':round_number,'images':len(images),'read':len(read),'allPass':len(current)==len(requested) and all(r['pass'] for r in current),'remaining':budget()});raise SystemExit(0 if len(current)==len(requested) and all(r['pass'] for r in current) else 1)
 if decision['action']!='rerun':raise RuntimeError('Stopped by review: '+decision.get('reason',''))
 assert not git('status','--porcelain');delta=git('diff','--name-only',frozen['source'],'HEAD').splitlines();assert all(p.startswith(('web/','docs/research/')) for p in delta),delta
 requested=decision['stages'];assert set(requested)<={s['name'] for s in plan['stages']} and 'build' in requested
