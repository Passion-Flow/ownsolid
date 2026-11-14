
# OWNSOLID_OLD_MAC_EPOCH_CLOSED
import sys as _ownsolid_closed_epoch_sys
_ownsolid_closed_epoch_sys.stderr.write("Old Mac admission epoch is closed; await coordinated activation.\n")
raise SystemExit(78)
import time
from pathlib import Path
import datetime,hashlib,json,os,re,signal,subprocess,sys,shutil
if not sys.flags.isolated or sys.flags.optimize:
 raise SystemExit('Run isolated and unoptimized; no work child started')
root=Path(sys.argv[1]);WORK_DEADLINE=float(sys.argv[2]);TOTAL_DEADLINE=float(sys.argv[3])
class DeadlineExpired(BaseException):pass
def now():return time.clock_gettime(time.CLOCK_MONOTONIC)
def budget(cap):
 left=WORK_DEADLINE-now()
 if left<=0:raise DeadlineExpired('Absolute work cutoff reached')
 return min(float(cap),left)
def alarm(signum,frame):raise DeadlineExpired('Absolute work backstop fired')
signal.signal(signal.SIGALRM,alarm);signal.setitimer(signal.ITIMER_REAL,budget(600))
info=json.loads((root/'preparation.json').read_text());plan=json.loads((root/'stage-plan.json').read_text());work=Path(info['worktree'])
env={k:os.environ[k] for k in ('PATH','HOME','TMPDIR','LANG','LC_ALL','USER','LOGNAME','SHELL','XDG_CACHE_HOME','PNPM_HOME') if k in os.environ}
caps=[int(v) for v in re.findall(r'max[-_]old[-_]space[-_]size[= ]+(\d+)',os.environ.get('NODE_OPTIONS',''))]
env.update(NODE_OPTIONS='--max-old-space-size='+str(min([512,*caps])),npm_config_offline='true',OWNSOLID_PLAYWRIGHT_PORT_OFFSET='26726',VITEST_MAX_WORKERS='1',CI='1')
results=[];serial=0;bound=None
files=list(json.loads((root/'input-files.json').read_text()))
def git(*args):return subprocess.check_output(['git',*args],cwd=work,text=True,timeout=budget(5)).strip()
def sha(path):
 budget(5);value=hashlib.sha256(path.read_bytes()).hexdigest();budget(5);return value
def hashes():return {p:sha(work/p) for p in files}
def record(phase,**values):
 budget(5);payload={'phase':phase,'utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'remainingSeconds':WORK_DEADLINE-now(),**values};(root/'browser-state.json').write_text(json.dumps(payload,indent=2)+'\n');print(json.dumps(payload),flush=True)
def execute(name,args,cwd,cap,report=False):
 global serial
 budget(5);serial+=1;label='%02d-%s'%(serial,name)
 record(label,argv=args,cwd=str(cwd))
 out=root/(label+('.json' if report else '.log'));err=root/(label+'.stderr.log')
 before=hashes()
 with out.open('w') as stdout,err.open('w') as stderr:
  proc=subprocess.Popen(args,cwd=cwd,env=env,stdout=stdout,stderr=stderr,start_new_session=False)
  (root/'owned-stage-groups.json').write_text(json.dumps([os.getpgrp()])+'\n')
  try:code=proc.wait(timeout=budget(cap))
  except subprocess.TimeoutExpired:
   proc.terminate()
   try:proc.wait(timeout=budget(2))
   except subprocess.TimeoutExpired:proc.kill();proc.wait(timeout=budget(2))
   code=124
 result={'name':name,'label':label,'exit':code,'inputFiles':before,'outputFiles':hashes(),'stdout':str(out),'stderr':str(err),'head':git('rev-parse','HEAD'),'webTree':git('rev-parse','HEAD:web')}
 results.append(result);(root/(label+'.exit')).write_text(str(code)+'\n');(root/'stage-results.json').write_text(json.dumps(results,indent=2)+'\n');record(label+'-finished',exit=code)
 return result

def cases(suites):
 for suite in suites:
  for spec in suite.get('specs',[]):
   for test in spec.get('tests',[]):yield spec,test
  yield from cases(suite.get('suites',[]))
def wait_review(sequence,passed,previous):
 name='review-%d.json'%sequence
 record('await-source-review',controlFile=name,focusedPassed=passed,files=hashes(),head=git('rev-parse','HEAD'))
 while not (root/name).exists():
  budget(1);time.sleep(min(.2,budget(1)))
 control=json.loads((root/name).read_text());assert control['head']==git('rev-parse','HEAD')
 assert not git('status','--porcelain'),'Commit reviewed own changes before continuation'
 if not passed:assert hashes()!=previous,'An unchanged input cannot be retried'
 return control

def preflight_again():
 memory=subprocess.check_output(['vm_stat'],timeout=budget(2));(root/'before-build-memory.log').write_bytes(memory)
 page=int(re.search(rb'page size of (\d+)',memory).group(1));free=int(re.search(rb'Pages free:\s+(\d+)',memory).group(1))*page
 assert free>=2*1024**3 and shutil.disk_usage(work).free>=15*1024**3,'Resource preflight no longer satisfies admission'
 record('before-build-preflight',freeMemoryBytes=free,diskFreeBytes=shutil.disk_usage(work).free)

def bind():
 global bound
 assert not git('status','--porcelain')
 bound={'head':git('rev-parse','HEAD'),'webTree':git('rev-parse','HEAD:web'),'files':hashes()}
 (root/'tested-source.json').write_text(json.dumps(bound,indent=2)+'\n')
def assert_bound():
 assert bound and git('rev-parse','HEAD')==bound['head'] and hashes()==bound['files'] and not git('status','--porcelain')

def build_and_browser(round_number):
 assert_bound();preflight_again()
 for app_name in ('console','portal'):
  app=work/'web/apps'/app_name
  result=execute('build-'+app_name,['pnpm','run','build'],app,45)
  if result['exit']!=0:return False
  assets={str(p.relative_to(work)):sha(p) for p in (app/'dist').rglob('*') if p.is_file()}
  assert assets and any('own-browser-sessions' in p.read_text() for p in (app/'dist/assets').glob('*.js')),'New Account implementation absent from asset set'
  (root/('assets-%d-%s.json'%(round_number,app_name))).write_text(json.dumps({'source':bound,'files':assets},indent=2)+'\n')
 for app_name in ('console','portal'):
  app=work/'web/apps'/app_name
  result=execute('collect-'+app_name,['pnpm','exec','playwright','test','account-sessions.spec.ts','--list','--reporter=json'],app,30,True)
  if result['exit']!=0:return False
  report=json.loads(Path(result['stdout']).read_text());selected=list(cases(report['suites']))
  assert not report.get('errors') and len(selected)==plan['expectedCasesPerEntrance'],(app_name,len(selected),report.get('errors'))
  assert len({spec['title'] for spec,test in selected})==len(selected)
  (root/('selection-%d-%s.json'%(round_number,app_name))).write_text(json.dumps([{'file':s['file'],'title':s['title']} for s,t in selected],indent=2)+'\n')
 all_passed=True
 for app_name in ('console','portal'):
  assert_bound();app=work/'web/apps'/app_name
  output=root/('browser-%d-%s-results'%(round_number,app_name))
  result=execute('browser-'+app_name,['pnpm','exec','playwright','test','account-sessions.spec.ts','--workers=1','--retries=0','--reporter=json','--output='+str(output)],app,70,True)
  if result['exit']!=0:all_passed=False
  try:
   report=json.loads(Path(result['stdout']).read_text());selected=list(cases(report['suites']));stats=report['stats']
   result['stats']=stats
   for spec,test in selected:
    if test.get('results'):assert len(test['results'])==1 and test['results'][0].get('retry',0)==0
   if report.get('errors') or stats['skipped'] or stats['flaky'] or stats['expected']!=plan['expectedCasesPerEntrance']:all_passed=False
  except (ValueError,KeyError):all_passed=False
  (root/'stage-results.json').write_text(json.dumps(results,indent=2)+'\n')
 return all_passed

assert sha(root/'browser-run.py')==info['preparedFiles']['browser-run.py'] and sha(root/'stage-plan.json')==info['preparedFiles']['stage-plan.json']
assert git('rev-parse','HEAD')==info['source'] and hashes()==json.loads((root/'input-files.json').read_text())
sequence=1
try:
 # Existing scoped commands, same rules/caps, no broad or installed gate.
 passed=True
 for step in plan['steps']:
  result=execute(step['name'],step['argv'],Path(step['cwd']),45)
  if result['exit']!=0:passed=False
 previous=hashes()
 while True:
  control=wait_review(sequence,passed,previous);sequence+=1
  if control.get('stop'):raise SystemExit(1)
  if passed:break
  allowed={step['name']:step for step in plan['steps']};requested=control['run']
  assert requested and all(name in allowed for name in requested)
  previous=hashes();passed=True
  for name in requested:
   step=allowed[name];result=execute(name,step['argv'],Path(step['cwd']),45)
   if result['exit']!=0:passed=False
 bind();round_number=1
 while True:
  success=build_and_browser(round_number)
  if success:
   images={str(p.relative_to(root)):sha(p) for p in root.rglob('*.png')}
   (root/'browser-qualification.json').write_text(json.dumps({'source':bound,'passed':True,'results':results,'images':images,'imagesRead':0,'fullGate':False,'installed':False},indent=2)+'\n')
   record('complete',source=bound,imagesGenerated=len(images));break
  previous=hashes();control=wait_review(sequence,False,previous);sequence+=1
  if control.get('stop'):raise SystemExit(1)
  allowed={step['name']:step for step in plan['steps']};assert control['run'] and all(name in allowed for name in control['run'])
  passed=True
  for name in control['run']:
   step=allowed[name];result=execute(name,step['argv'],Path(step['cwd']),45)
   if result['exit']!=0:passed=False
  if not passed:
   # Never hide the failed rerun behind another same-input retry.
   record('focused-repair-failed');raise SystemExit(1)
  bind();round_number+=1
finally:
 budget(5);(root/'runner-results.json').write_text(json.dumps({'results':results,'lastSource':bound,'imagesGenerated':len(list(root.rglob('*.png'))),'imagesRead':0,'fullGate':False,'installed':False},indent=2)+'\n')
 record('runner-ended')
