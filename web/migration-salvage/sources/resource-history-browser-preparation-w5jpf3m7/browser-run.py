
# OWNSOLID_OLD_MAC_EPOCH_CLOSED
import sys as _ownsolid_closed_epoch_sys
_ownsolid_closed_epoch_sys.stderr.write("Old Mac admission epoch is closed; await coordinated activation.\n")
raise SystemExit(78)
from pathlib import Path
import hashlib,json,os,re,signal,subprocess,sys
root=Path(sys.argv[1]);info=json.loads((root/'preparation.json').read_text());plan=json.loads((root/'stage-plan.json').read_text());work=Path(info['worktree']);app=work/'web/apps/console';old=Path(info['oldWorktree'])
env={k:v for k,v in os.environ.items() if not k.startswith(('OWNSOLID_LIVE_','OWNSOLID_GW120_')) and k not in ['OWNSOLID_DEV_ORIGIN','TMOUT','PS1']}
env.update({'npm_config_offline':'true','OWNSOLID_PLAYWRIGHT_PORT_OFFSET':'26726','VITEST_MAX_WORKERS':'2','GOPROXY':'off','GOSUMDB':'off','CARGO_NET_OFFLINE':'true'})
# Keep stricter inherited caps; make the otherwise uncapped proposal explicit.
options=env.get('NODE_OPTIONS','');caps=[int(v) for v in re.findall(r'max[-_]old[-_]space[-_]size[= ]+(\d+)',options)]
heap=min([512,*caps]);options=re.sub(r'--max[-_]old[-_]space[-_]size[= ]+\d+','',options)
env['NODE_OPTIONS']=options+' --max-old-space-size='+str(heap)
groups=[];results={}
def git(cwd,*args):return subprocess.check_output(['git',*args],cwd=cwd,text=True,timeout=5).strip()
def sha(path):return hashlib.sha256(path.read_bytes()).hexdigest()
def record(phase,**kw):(root/'browser-state.json').write_text(json.dumps({'phase':phase,**kw},indent=2)+'\n')
def cases(suites):
 for suite in suites:
  for spec in suite.get('specs',[]):
   for test in spec.get('tests',[]):yield spec,test
  yield from cases(suite.get('suites',[]))
def execute(name,args,timeout,report=False):
 record(name,command=args)
 with (root/(name+('.json' if report else '.log'))).open('w') as out,(root/(name+'.stderr.log')).open('w') as err:
  proc=subprocess.Popen(args,cwd=app,env=env,stdout=out,stderr=err,start_new_session=False)
  groups.append(os.getpgrp());(root/'owned-stage-groups.json').write_text(json.dumps(sorted(set(groups)))+'\n')
  try:code=proc.wait(timeout=timeout)
  except subprocess.TimeoutExpired:
   record(name+'-timeout',command=args)
   proc.terminate()
   try:proc.wait(timeout=3)
   except subprocess.TimeoutExpired:proc.kill();proc.wait(timeout=2)
   raise
 (root/(name+'.exit')).write_text(str(code)+'\n')
 return code
assert git(work,'rev-parse','HEAD')==info['source'] and git(work,'rev-parse','HEAD:web')==info['webTree']
assert not git(work,'status','--porcelain')
assert git(old,'rev-parse','HEAD')==info['oldSource'] and not git(old,'status','--porcelain')
for rel,digest in plan['inputFiles'].items():assert sha(app/rel)==digest,rel
for rel,digest in json.loads((root/'old-console-assets.json').read_text()).items():assert sha(old/rel)==digest,rel
try:
 for stage in plan['stages'][:2]:assert execute(stage['name'],stage['args'],stage['timeout'],True)==0
 primary=json.loads((root/'discover-primary.json').read_text());inventory=json.loads((root/'discover-inventory.json').read_text())
 selected=list(cases(primary['suites']));counts={}
 for spec,test in selected:counts[Path(spec['file']).name]=counts.get(Path(spec['file']).name,0)+1
 assert counts.get('resource-activity.spec.ts')==21,counts
 assert counts.get('audit-read-recovery.spec.ts')==41,counts
 audit=[spec['title'] for spec,test in selected if Path(spec['file']).name=='audit-read-recovery.spec.ts']
 partitions=[[s for s in audit if re.search(p,s)] for p in [' in en$',' in zh-CN$','does not expose an audit consumer|does not queue an offline audit retry']]
 assert sum(map(len,partitions))==41 and len(set(sum(partitions,[])))==41,partitions
 assert len(list(cases(inventory['suites'])))==30
 (root/'discovered-selection.json').write_text(json.dumps({'counts':counts,'auditPartitions':[len(p) for p in partitions],'inventory':30},indent=2)+'\n')
 build=plan['stages'][2];assert execute(build['name'],build['args'],build['timeout'])==0
 assets={str(p.relative_to(work)):sha(p) for p in (app/'dist').rglob('*') if p.is_file()}
 chunks=[p.read_text() for p in (app/'dist/assets').glob('*.js')]
 assert any('activity_from' in s and 'clientWindow' in s for s in chunks),'New dates/cache-window code absent from build'
 (root/'new-console-assets.json').write_text(json.dumps({'source':info['source'],'webTree':info['webTree'],'files':assets},indent=2)+'\n')
 for stage in plan['stages'][3:]:
  args=['pnpm','exec','playwright','test',*stage['spec']]
  if stage.get('config'):args+=['--config',stage['config']]
  if stage.get('grep'):args+=['--grep',stage['grep']]
  args+=['--workers=2','--retries=0','--reporter=json','--output='+str(root/(stage['name']+'-results'))]
  code=execute(stage['name'],args,stage['timeout'],True)
  report=json.loads((root/(stage['name']+'.json')).read_text());stats=report['stats'];expected_failures=stage.get('expectedFailures',0)
  results[stage['name']]={'exit':code,'stats':stats}
  assert not report.get('errors'),report.get('errors')
  assert stats['skipped']==0 and stats['flaky']==0 and stats['unexpected']==expected_failures,stats
  for spec,test in cases(report['suites']):
   assert len(test['results'])==1 and test['results'][0].get('retry',0)==0
   if expected_failures:
    text=re.sub(r'\x1b\[[0-9;]*m','','\n'.join(e.get('message','') for e in test['results'][0].get('errors',[])))
    assert 'toHaveCount' in text and 'Expected: 2' in text and 'Received: 0' in text,text
  assert code==(1 if expected_failures else 0),code
  assert stats['expected']+stats['unexpected']==len(list(cases(report['suites'])))
  if stage['name']=='resource-pages':assert stats['expected']==counts['resource-pages.spec.ts']+counts['node-page.spec.ts']
 assert sum(results[n]['stats']['expected'] for n in ['audit-en','audit-zh','audit-boundaries'])==41
 assert results['resource-activity']['stats']['expected']==21
 assert results['inventory-a11y']['stats']['expected']+results['inventory-keyboard']['stats']['expected']==30
 assert not git(work,'status','--porcelain') and not git(old,'status','--porcelain')
 (root/'browser-qualification.json').write_text(json.dumps({'source':info['source'],'results':results,'fullGate':False,'installed':False,'imagesRead':0},indent=2)+'\n')
 record('complete',source=info['source'])
finally:
 record('runner-ended',previousPhase=json.loads((root/'browser-state.json').read_text()).get('phase'),results=results)
