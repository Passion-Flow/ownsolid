import time
from pathlib import Path
import base64,hashlib,json,os,re,signal,stat,subprocess,sys
assert sys.flags.isolated and not sys.flags.optimize
root=Path(sys.argv[1]);deadline=float(sys.argv[2]);hard=float(sys.argv[3]);info=json.loads((root/'preparation.json').read_text());work=Path(info['worktree']);web=work/'web';app=web/'apps/console';fd=int(os.environ['OWNSOLID_GATE_FD'])
carrier=Path('/Users/passion./.local/state/ownsolid/coordination/workstation/ownsolid-2-20261113-e1/full-gate.lock')
def budget(cap=600):
 left=deadline-time.clock_gettime(time.CLOCK_MONOTONIC)
 if left<=0:raise TimeoutError('UX127 absolute work cutoff reached')
 return min(cap,left)
def expired(s,f):raise TimeoutError('UX127 absolute work cutoff reached')
signal.signal(signal.SIGALRM,expired);signal.setitimer(signal.ITIMER_REAL,budget())
def save(name,data):budget();(root/name).write_text(json.dumps(data,indent=2)+'\n')
def sha(p):budget();return hashlib.sha256(p.read_bytes()).hexdigest()
def git(*args):return subprocess.check_output(['git',*args],cwd=work,text=True,timeout=budget(5)).strip()
def identity():
 budget();assert sha(carrier.with_name('manifest.json'))=='7fa013a32f542f8ff6467863425e952da5e436b0ad470da25d766613c94eb983'
 for s in [os.fstat(fd),carrier.lstat()]:assert stat.S_ISREG(s.st_mode) and (s.st_dev,s.st_ino,s.st_uid,stat.S_IMODE(s.st_mode),s.st_nlink,s.st_size)==(16777231,53377467,501,0o600,1,0)
def cases(suites):
 for suite in suites:
  for spec in suite.get('specs',[]):
   for test in spec.get('tests',[]):yield spec,test
  yield from cases(suite.get('suites',[]))
env={k:v for k,v in os.environ.items() if not k.startswith(('OWNSOLID_LIVE_','OWNSOLID_GW120_')) and k not in ['OWNSOLID_DEV_ORIGIN','TMOUT','PS1']}
options=env.get('NODE_OPTIONS','');caps=[int(v) for v in re.findall(r'max[-_]old[-_]space[-_]size[= ]+(\d+)',options)];heap=min([512,*caps]);env.update(NODE_OPTIONS=re.sub(r'--max[-_]old[-_]space[-_]size[= ]+\d+','',options)+' --max-old-space-size='+str(heap),npm_config_offline='true',CI='1',OWNSOLID_PLAYWRIGHT_PORT_OFFSET='26726',VITEST_MAX_WORKERS='1',DEBUG='pw:webserver',PYTHONDONTWRITEBYTECODE='1')
for token in options.split():
 if token.partition('=')[0].replace('_','-') in ['--max-heap-size','--max-old-space-size-percentage']:raise ValueError('Absolute Node old-space cap required')
assert git('rev-parse','HEAD')==info['source'] and not git('status','--porcelain')
for name,h in json.loads((root/'source-file-sha256.json').read_text()).items():assert sha(work/name)==h,name
save('effective-controls.json',{'NodeOldSpaceMiB':heap,'workers':1,'retries':0,'offlineDependencies':True,'wholeSeconds':600,'cleanupReserve':60,'negativeControlNotRepeated':True,'noFullNativeInstalled':True,'trace':'off','inlinePngExtraction':True})
all_results=[];round_number=0
def execute(name,args,cwd,cap,report=False,expected=None):
 identity();started=time.clock_gettime(time.CLOCK_MONOTONIC);folder=root/('round-'+str(round_number));folder.mkdir(exist_ok=True);output=folder/(name+('.json' if report else '.log'));error=folder/(name+'.stderr.log')
 save('browser-state.json',{'phase':name,'round':round_number,'source':git('rev-parse','HEAD'),'argv':args,'remainingWorkSeconds':budget()});print('UX127_FIX_STAGE',round_number,name,flush=True)
 with output.open('x') as out,error.open('x') as err:
  child=subprocess.Popen(args,cwd=cwd,env=env,stdin=subprocess.DEVNULL,stdout=out,stderr=err,pass_fds=(fd,),umask=0o077);(root/'owned-stage-groups.json').write_text(json.dumps([os.getpgrp()])+'\n')
  try:code=child.wait(timeout=budget(cap))
  except subprocess.TimeoutExpired:
   child.terminate()
   try:child.wait(timeout=budget(2))
   except subprocess.TimeoutExpired:child.kill();child.wait(timeout=budget(2))
   code=124
 result={'name':name,'round':round_number,'source':git('rev-parse','HEAD'),'webTree':git('rev-parse','HEAD:web'),'argv':args,'cwd':str(cwd),'exit':code,'seconds':time.clock_gettime(time.CLOCK_MONOTONIC)-started,'output':str(output),'stderr':str(error),'pass':code==0}
 if report:
  try:
   data=json.loads(output.read_text());selected=list(cases(data.get('suites',[])));result.update(stats=data.get('stats',{}),collectedCases=len(selected),globalErrors=data.get('errors',[]))
   result['pass']=code==0 and len(selected)==expected and not data.get('errors') and data['stats'].get('skipped')==0 and data['stats'].get('flaky')==0 and data['stats'].get('unexpected')==0 and all(len(t['results'])==1 and t['results'][0].get('retry',0)==0 for s,t in selected)
   result['images']=[]
   for spec,test in selected:
    for run in test['results']:
     for attachment in run.get('attachments',[]):
      if attachment.get('contentType')!='image/png':continue
      if attachment.get('body'):raw=base64.b64decode(attachment['body'])
      elif attachment.get('path'):raw=Path(attachment['path']).read_bytes()
      else:continue
      image_dir=folder/'images';image_dir.mkdir(exist_ok=True);image=image_dir/(name+'-'+str(len(result['images'])+1).zfill(2)+'-'+re.sub(r'[^A-Za-z0-9_.-]','-',attachment['name'])+'.png');image.write_bytes(raw);result['images'].append({'name':attachment['name'],'path':str(image),'sha256':sha(image),'case':spec['title']})
  except (ValueError,KeyError,OSError) as e:result['pass']=False;result['reportError']=repr(e)
 all_results.append(result);save('stage-results.json',all_results);identity();return result
def format_and_freeze():
 paths=info['formatPaths'];result=execute('format-owned',['pnpm','exec','prettier','--write',*[str(work/p) for p in paths]],web,60)
 if not result['pass']:return False
 changed=git('diff','--name-only').splitlines();assert set(changed)<=set(paths),changed
 if changed:
  subprocess.run(['git','add','--',*changed],cwd=work,check=True,timeout=budget(5));subprocess.run(['git','commit','-m','UX-127 Format the frozen detail action candidate','-m','Apply the repository formatter to owned candidate inputs before directed validation. No validation result is claimed by this formatting commit.'],cwd=work,check=True,timeout=budget(5))
 assert not git('status','--porcelain')
 save('frozen-round'+str(round_number)+'.json',{'source':git('rev-parse','HEAD'),'tree':git('rev-parse','HEAD^{tree}'),'webTree':git('rev-parse','HEAD:web'),'inputs':{p:sha(work/p) for p in paths}});return True
plan=json.loads((root/'stage-plan.json').read_text())
while True:
 round_number+=1
 assert not git('status','--porcelain')
 if not format_and_freeze():raise RuntimeError('Owned formatting failed')
 frozen=git('rev-parse','HEAD');prerequisites=True
 for stage in plan['prerequisites']:
  result=execute(stage['name'],stage['args'],work/stage['cwd'],stage['timeout']);prerequisites=prerequisites and result['pass']
 if prerequisites:
  result=execute('console-build',['pnpm','run','build'],app,60);prerequisites=result['pass']
  if prerequisites:save('console-assets-round'+str(round_number)+'.json',{'source':frozen,'webTree':git('rev-parse','HEAD:web'),'files':{str(f.relative_to(work)):sha(f) for f in (app/'dist').rglob('*') if f.is_file()}})
 if prerequisites:
  for stage in plan['browsers']:
   args=['pnpm','exec','playwright','test',*stage['spec']]
   if stage.get('grep'):args+=['--grep',stage['grep']]
   args+=['--workers=1','--retries=0','--trace=off','--reporter=json','--output='+str(root/('round-'+str(round_number))/(stage['name']+'-results'))]
   execute(stage['name'],args,app,stage['timeout'],True,stage['expected'])
 assert git('rev-parse','HEAD')==frozen and not git('status','--porcelain')
 current=[r for r in all_results if r['round']==round_number];images=[i for r in current for i in r.get('images',[])];save('images-ready-round'+str(round_number)+'.json',{'source':frozen,'generatedImages':len(images),'images':images,'stages':current,'allStagesPass':all(r['pass'] for r in current),'browserStagesRun':sum('stats' in r for r in current),'browserStagesPlanned':len(plan['browsers'])})
 save('browser-state.json',{'phase':'active-image-review','round':round_number,'source':frozen,'generatedImages':len(images),'allStagesPass':all(r['pass'] for r in current),'remainingWorkSeconds':budget()});print('UX127_FIX_REVIEW',round_number,len(images),flush=True)
 control=root/('review-control-round'+str(round_number)+'.json')
 while not control.exists():budget();time.sleep(min(.15,budget(.15)))
 decision=json.loads(control.read_text())
 if decision['action']=='finish':
  save('worker-outcome.json',{'source':frozen,'round':round_number,'allStagesPass':all(r['pass'] for r in current),'imagesGenerated':len(images),'imagesRead':len(decision.get('readImages',[])),'readImages':decision.get('readImages',[]),'remaining':budget()});raise SystemExit(0 if all(r['pass'] for r in current) and prerequisites and len(current)==len(plan['prerequisites'])+len(plan['browsers'])+2 else 1)
 if decision['action']!='rerun':raise RuntimeError('Review stopped: '+decision.get('reason',''))
 assert not git('status','--porcelain');delta=git('diff','--name-only',frozen,'HEAD').splitlines();assert all(s.startswith(('web/apps/console/src/','web/apps/console/e2e/','web/packages/api/src/','web/packages/i18n/locales/','docs/research/')) for s in delta),delta
