import time
from pathlib import Path
import hashlib,json,os,re,shlex,signal,subprocess,sys
assert sys.flags.isolated and not sys.flags.optimize
root=Path(sys.argv[1]);deadline=float(sys.argv[2]);hard=float(sys.argv[3]);info=json.loads((root/'preparation.json').read_text());plan=json.loads((root/'stage-plan.json').read_text());work=Path(info['worktree']);app=work/'web/apps/console';old=Path(info['oldWorktree'])
def budget(cap=600):
 left=deadline-time.clock_gettime(time.CLOCK_MONOTONIC)
 if left<=0:raise TimeoutError('UX127 absolute work cutoff reached')
 return min(cap,left)
def expired(s,f):raise TimeoutError('UX127 absolute work cutoff reached')
signal.signal(signal.SIGALRM,expired);signal.setitimer(signal.ITIMER_REAL,budget());signal.signal(signal.SIGTERM,lambda s,f:sys.exit(143))
def save(name,data):budget();(root/name).write_text(json.dumps(data,indent=2)+'\n')
def sha(p):budget();return hashlib.sha256(p.read_bytes()).hexdigest()
def git(cwd,*args):return subprocess.check_output(['git',*args],cwd=cwd,text=True,timeout=budget(4)).strip()
def cases(suites):
 for suite in suites:
  for spec in suite.get('specs',[]):
   for test in spec.get('tests',[]):yield spec,test
  yield from cases(suite.get('suites',[]))
env={k:v for k,v in os.environ.items() if not k.startswith(('OWNSOLID_LIVE_','OWNSOLID_GW120_')) and k not in ['OWNSOLID_DEV_ORIGIN','TMOUT','PS1']}
options=env.get('NODE_OPTIONS','');caps=[int(v) for v in re.findall(r'max[-_]old[-_]space[-_]size[= ]+(\d+)',options)];heap=min([512,*caps]);env.update(NODE_OPTIONS=re.sub(r'--max[-_]old[-_]space[-_]size[= ]+\d+','',options)+' --max-old-space-size='+str(heap),npm_config_offline='true',CI='1',OWNSOLID_PLAYWRIGHT_PORT_OFFSET='26726',VITEST_MAX_WORKERS='1',DEBUG='pw:webserver',PYTHONDONTWRITEBYTECODE='1')
for token in shlex.split(options):
 if token.partition('=')[0].replace('_','-') in ['--max-heap-size','--max-old-space-size-percentage']:raise ValueError('Absolute Node old-space cap required')
assert git(work,'rev-parse','HEAD')==info['source'] and git(work,'rev-parse','HEAD:web')==info['webTree'];assert not git(work,'status','--porcelain');assert git(old,'rev-parse','HEAD')==info['oldSource'] and not git(old,'status','--porcelain')
for name,h in json.loads((root/'source-file-sha256.json').read_text()).items():assert sha(work/name)==h,name
for name,h in plan['inputFiles'].items():assert sha(app/name)==h,name
for name,h in json.loads((root/'old-console-assets.json').read_text()).items():assert sha(old/name)==h,name
for path,h in json.loads((root/'corrected-probe-inputs.json').read_text()).items():assert sha(Path(path))==h,path
save('effective-controls.json',{'nodeMaximumOldSpaceMiB':heap,'workers':1,'retries':0,'perCommandCapsUnchanged':True,'wholeSeconds':600,'cleanupReserve':60,'priorDiscoveryAnd44UnitsNotRepeated':True,'buildReason':'300 current asset mismatches; rebuild affected Console only','externalProbeMinimalESM':'Existing package type:module, unchanged test/fixture/config hashes','networkTrace':'off; no request header/body archive','offlineDependencies':True})
round_number=0;current_source=info['source'];all_results=[]
expected={'negative-old-runtime':4,'resource-activity':21,'audit-en':19,'audit-zh':19,'audit-boundaries':3,'resource-pages':19,'inventory-a11y':20,'inventory-keyboard':10}
class StageTimeout(BaseException):pass
def execute(stage,rounddir):
 name=stage['name'];report=name!='console-build';args=stage.get('args') if not report else ['pnpm','exec','playwright','test',*stage['spec']]
 if report:
  if stage.get('config'):args+=['--config',stage['config']]
  if stage.get('grep'):args+=['--grep',stage['grep']]
  args+=['--workers=1','--retries=0','--trace=off','--reporter=json','--output='+str(rounddir/(name+'-results'))]
 save('browser-state.json',{'phase':name,'round':round_number,'source':current_source,'command':args});print('UX127_STAGE',round_number,name,flush=True);started=time.clock_gettime(time.CLOCK_MONOTONIC)
 output=rounddir/(name+('.json' if report else '.log'))
 with output.open('x') as out,(rounddir/(name+'.stderr.log')).open('x') as err:
  child=subprocess.Popen(args,cwd=app,env=env,stdin=subprocess.DEVNULL,stdout=out,stderr=err,pass_fds=(int(os.environ['OWNSOLID_GATE_FD']),),umask=0o077);(root/'owned-stage-groups.json').write_text(json.dumps([os.getpgrp()])+'\n')
  try:code=child.wait(timeout=budget(stage['timeout']))
  except subprocess.TimeoutExpired:
   child.terminate()
   try:child.wait(timeout=budget(2))
   except subprocess.TimeoutExpired:child.kill();child.wait(timeout=budget(2))
   save('stage-timeout.json',{'stage':name,'originalCapSeconds':stage['timeout'],'childActualExit':child.returncode,'productAssertionFailureInferred':False});raise StageTimeout(name+' original command cap reached')
 result={'name':name,'source':current_source,'round':round_number,'argv':args,'exit':code,'seconds':time.clock_gettime(time.CLOCK_MONOTONIC)-started,'output':str(output)};all_results.append(result);save('stage-results.json',all_results)
 if not report:
  if code:raise RuntimeError('Console build failed with exit '+str(code))
  assets={str(f.relative_to(work)):sha(f) for f in (app/'dist').rglob('*') if f.is_file()};previous=json.loads((root/'previous-new-console-assets.json').read_text())['files'];save('new-console-assets-round'+str(round_number)+'.json',{'source':current_source,'webTree':git(work,'rev-parse','HEAD:web'),'files':assets,'matchesOriginal696FileMap':assets==previous,'oldManifestSha256':'31ecbbae60314b2cc5a6ae229cc1efeafc802734df85ac1e29d9993842fee9e0'});return
 data=json.loads(output.read_text());stats=data.get('stats',{});selected=list(cases(data.get('suites',[])));wanted=stage.get('expectedFailures',0);result['stats']=stats;result['collectedCases']=len(selected);save('stage-results.json',all_results)
 if data.get('errors'):raise RuntimeError(name+' has collection/global errors')
 if len(selected)!=expected[name] or stats.get('skipped')!=0 or stats.get('flaky')!=0 or stats.get('unexpected')!=wanted:raise RuntimeError(name+' actual case/status mismatch')
 for spec,test in selected:
  if len(test['results'])!=1 or test['results'][0].get('retry',0)!=0:raise RuntimeError(name+' retried or missing result')
  if wanted:
   text=re.sub(r'\x1b\[[0-9;]*m','','\n'.join(e.get('message','') for e in test['results'][0].get('errors',[])))
   if not ('toHaveCount' in text and 'Expected: 2' in text and 'Received: 0' in text):raise RuntimeError('Negative did not fail at original policy row assertion')
 if code!=(1 if wanted else 0):raise RuntimeError(name+' actual exit mismatch')

tasks=list(plan['stages'])
while True:
 round_number+=1;rounddir=root/('round-'+str(round_number));rounddir.mkdir(mode=0o700);failure=None
 try:
  for stage in tasks:execute(stage,rounddir)
 except (Exception,subprocess.SubprocessError) as e:
  failure={'errorClass':type(e).__name__,'detail':str(e)}
 images=[]
 for f in sorted(root.rglob('*.png')):
  if f.is_file():images.append({'path':str(f),'sha256':sha(f)})
 save('images-ready-round'+str(round_number)+'.json',{'source':current_source,'webTree':git(work,'rev-parse','HEAD:web'),'round':round_number,'failure':failure,'generatedImages':len(images),'images':images,'workCutoffMonotonic':deadline,'caseResults':all_results})
 save('browser-state.json',{'phase':'active-image-review','round':round_number,'generatedImages':len(images),'failure':failure,'workCutoffMonotonic':deadline})
 control=root/('review-control-round'+str(round_number)+'.json')
 while not control.exists():budget();time.sleep(min(.15,budget(.15)))
 decision=json.loads(control.read_text())
 if decision['action']=='accept':
  if failure:raise RuntimeError('Failed browser round cannot be accepted')
  if set(decision['readImages'])!={r['path'] for r in images}:raise RuntimeError('Every actual generated image must be read')
  for row in images:assert sha(Path(row['path']))==row['sha256']
  assert git(work,'rev-parse','HEAD')==current_source and not git(work,'status','--porcelain') and not git(old,'status','--porcelain')
  save('browser-qualification.json',{'source':current_source,'webTree':git(work,'rev-parse','HEAD:web'),'results':all_results,'actualImagesGenerated':len(images),'actualImagesRead':len(images),'visualReview':decision['visualReview'],'fullGate':False,'installed':False});break
 if decision['action']=='abort':raise RuntimeError('Review ended without acceptance: '+decision.get('reason',''))
 if decision['action']!='rerun':raise RuntimeError('Unknown review action')
 next_source=git(work,'rev-parse','HEAD');assert next_source==decision['source'] and not git(work,'status','--porcelain')
 changed=git(work,'diff','--name-only',current_source,next_source).splitlines()
 if not changed:raise RuntimeError('No confirmed correction; automatic retry refused')
 for path in changed:
  if not path.startswith(('web/apps/console/src/','web/packages/i18n/locales/','web/packages/ui/src/','docs/research/','docs/development/design-system.md')):raise RuntimeError('Correction outside approved UI source: '+path)
 for name,h in plan['inputFiles'].items():assert sha(app/name)==h,name
 for stage_name,override in decision.get('stageOverrides',{}).items():
  if stage_name!='negative-old-runtime' or set(override)!={'config','probeInputs'}:raise RuntimeError('Only a confirmed external ESM packaging correction is accepted')
  config=Path(override['config']);assert config.is_relative_to(root) if hasattr(config,'is_relative_to') else str(config).startswith(str(root)+'/')
  for name,h in override['probeInputs'].items():assert sha(Path(name))==h
  for stage in plan['stages']:
   if stage['name']==stage_name:stage['config']=str(config)
 save('correction-round'+str(round_number)+'.json',{'previous':current_source,'next':next_source,'paths':changed,'reason':decision['reason'],'requestedStages':decision['stages']});current_source=next_source;wanted=decision['stages'];tasks=[s for s in plan['stages'] if s['name'] in wanted]
 if not tasks:raise RuntimeError('No affected verification stage selected')
 if any(path.startswith('web/') for path in changed) and 'console-build' not in wanted:raise RuntimeError('UI source correction requires a fresh build')
save('browser-state.json',{'phase':'complete','source':current_source,'rounds':round_number})
