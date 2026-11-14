import time
from pathlib import Path
import hashlib,importlib.util,json,os,re,resource,shlex,signal,subprocess,sys
root=Path(sys.argv[1]);deadline=float(sys.argv[2]);info=json.loads((root/'preparation.json').read_text());work=Path(info['worktree']);web=work/'web';fd=int(os.environ['OWNSOLID_GATE_FD']);guard=int(os.environ['OWNSOLID_GATE_PARENT']);resource.setrlimit(resource.RLIMIT_CORE,(0,0))
def now():return time.clock_gettime(time.CLOCK_MONOTONIC)
def budget(cap=600):
 remaining=deadline-now()
 if remaining<=0:raise TimeoutError('B measurement cutoff reached')
 return min(cap,remaining)
def expire(s,f):raise TimeoutError('B measurement cutoff reached')
signal.signal(signal.SIGALRM,expire);signal.setitimer(signal.ITIMER_REAL,budget());signal.signal(signal.SIGTERM,lambda s,f:sys.exit(143))
spec=importlib.util.spec_from_file_location('native_sampler',root/'native-sampler.py');native=importlib.util.module_from_spec(spec);spec.loader.exec_module(native)
def save(name,data):budget();(root/name).write_text(json.dumps(data,indent=2)+'\n')
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def check_source():
 assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=work,text=True,timeout=budget(3)).strip()=='56a19799bfec788170d71680b4f0663c23073107'
 for p,h in json.loads((root/'source-B-sha256.json').read_text()).items():budget();assert sha(work/p)==h,p
 assert subprocess.check_output(['git','status','--porcelain'],cwd=work,timeout=budget(3))==b''
options=os.environ.get('NODE_OPTIONS','');tokens=shlex.split(options);cap=2048
for i,token in enumerate(tokens):
 key,sep,amount=token.partition('=');key=key.replace('_','-')
 if key in ['--max-heap-size','--max-old-space-size-percentage']:raise ValueError('Absolute Node old-space limit required')
 if key=='--max-old-space-size':
  amount=amount if sep else tokens[i+1];assert amount.isascii() and amount.isdecimal() and int(amount)>0;cap=min(cap,int(amount))
env=os.environ.copy();env.update(NODE_OPTIONS=options+' --max-old-space-size='+str(cap),npm_config_offline='true',CI='1',VITEST_MAX_WORKERS='1',PYTHONDONTWRITEBYTECODE='1')
for p,h in json.loads((root/'pinned-discovery-sha256.json').read_text()).items():assert sha(Path(p))==h,p
cli=next(p for p in json.loads((root/'pinned-discovery-sha256.json').read_text()) if p.endswith('/bin/eslint.js'))
save('B-effective-controls.json',{'nodeOldSpaceMiB':cap,'coreBytes':0,'commandsSerial':True,'intervalSeconds':.05,'ownerTreeDefinition':'Admission guard plus all transitive descendants, including awake guard, controller and sampler; retain known same-lifetime reparented descendants. Sum of resident bytes at each validated snapshot, including repeated shared pages.','continuousMaximumClaimed':False,'samplingLimit':'50ms sampled, non-atomic per-process reads; shorter lived processes or intersample peaks are not guaranteed. Any failed/truncated/unattributed sample refuses the owner-tree conclusion.','monitorFields':['pid','ppid','pgid','uid','startTimeSecondsAndMicroseconds','rssBytes','status'],'argvOrEnvironmentDumped':False,'functionalSevenCli372CoverageAndSevenNegativeControls':'Retained unchanged from prior exact-source qualification; not counted again here.'})
results=[]
def publish_groups():
 raw=subprocess.check_output(['ps','-axo','pid=,ppid=,pgid='],text=True,timeout=budget(3));rows=[tuple(map(int,line.split())) for line in raw.splitlines() if line.strip()];pids={os.getpid()}
 while True:
  added={pid for pid,parent,group in rows if parent in pids}-pids
  if not added:break
  pids.update(added)
 groups={group for pid,parent,group in rows if pid in pids};save('owned-stage-groups.json',sorted(groups))
def measure(name,args):
 check_source();print('TREE_PEAK_STAGE',name,flush=True);(root/'stage').write_text(name+'\n');sampler=native.OwnerSampler(guard,os.getpid());first=sampler.snapshot();baseline={(r['pid'],tuple(r['startTime'])) for r in first['rows']};peak=0;single=0;sampler_peak=0;peak_frame=None;frames=0;started=now();monitor_cpu_start=time.process_time();previous_sample=first['sampleMonotonic'];max_gap=0.;groups={os.getpgrp()};guard_group=next(r['pgid'] for r in first['rows'] if r['pid']==guard)
 with (root/(name+'.stdout')).open('xb') as out,(root/(name+'.stderr')).open('xb') as err,(root/(name+'-samples.jsonl')).open('x') as trace:
  child=subprocess.Popen(['/usr/bin/time','-l',*args],cwd=web,env=env,stdin=subprocess.DEVNULL,stdout=out,stderr=err,pass_fds=(fd,),umask=0o022)
  try:
   while True:
    budget();frame=sampler.snapshot();gap=frame['sampleMonotonic']-previous_sample;previous_sample=frame['sampleMonotonic'];max_gap=max(max_gap,gap)
    if gap>.25:raise native.SampleGap('Owned sampling interval exceeded 250ms ceiling')
    frames+=1;trace.write(json.dumps(frame)+'\n');trace.flush();peak=max(peak,frame['sumRssBytes']);single=max(single,max(r['rssBytes'] for r in frame['rows']));sampler_peak=max(sampler_peak,frame['samplerRssBytes'])
    if frame['sumRssBytes']==peak:peak_frame=frame
    live_ids={r['pid'] for r in frame['rows']};groups={r['pgid'] for r in frame['rows'] if r['pgid']!=guard_group and (r['pgid'] in live_ids or r['pgid']==os.getpgrp())};save('owned-stage-groups.json',sorted(groups|{os.getpgrp()}))
    try:code=child.wait(timeout=budget(.05));break
    except subprocess.TimeoutExpired:pass
   final=sampler.snapshot();trace.write(json.dumps(final)+'\n');trace.flush();remaining=[r for r in final['rows'] if (r['pid'],tuple(r['startTime'])) not in baseline and r['status']!=5]
   if remaining:raise native.SampleGap('New owned process remains after measured command')
   save('owned-stage-groups.json',[os.getpgrp()])
  except BaseException as error:
   save(name+'-sampling-failure.json',{'error':repr(error),'samplesCompleted':frames,'treeConclusionAccepted':False});publish_groups();raise
 data=(root/(name+'.stderr')).read_text(errors='replace');m=re.search(r'^\s*(\d+)\s+maximum resident set size',data,re.MULTILINE)
 result={'name':name,'argv':args,'exit':code,'seconds':now()-started,'validSnapshots':frames,'samplerFailures':0,'sampledWholeOwnerTreePeakRssBytes':peak,'sampledSingleProcessPeakRssBytes':single,'samplerPeakRssBytes':sampler_peak,'samplerCpuSeconds':sampler.cpu_seconds,'samplerWallSeconds':sampler.wall_seconds,'monitorTotalCpuSeconds':time.process_time()-monitor_cpu_start,'maximumObservedSampleGapSeconds':max_gap,'commandCrossPgidEdges':[list(v) for v in sorted(sampler.cross_edges) if v[1] not in {pid for pid,start in baseline}],'crossPgidEdgesObserved':len(sampler.cross_edges),'crossPgidEdgeIds':[list(v) for v in sorted(sampler.cross_edges)],'timeReportedMaximumRssBytes':int(m.group(1)) if m else None,'peakFrame':peak_frame,'source':'56a19799bfec788170d71680b4f0663c23073107'};results.append(result);save('B-command-results.json',results);print(json.dumps({k:v for k,v in result.items() if k not in ['peakFrame','argv','crossPgidEdgeIds']}),flush=True);return result
try:
 old=measure('B-old-complete-cli-memory',['node',cli,'--config','packages/config/eslint.config.js','--max-warnings','0','--format','json','.'])
 assert old['exit']==0 or 'heap out of memory' in (root/'B-old-complete-cli-memory.stderr').read_text(errors='replace'),'Unexpected original command failure'
 new=measure('B-new-complete-cli-memory',['pnpm','--silent','lint','--format','json']);assert new['exit']==0,'Candidate memory command failed'
 check_source();result={'source':'56a19799bfec788170d71680b4f0663c23073107','oldExit':old['exit'],'newExit':new['exit'],'bothOwnerTreesSampledWithoutError':True,'oldWholeOwnerTreePeakRssBytes':old['sampledWholeOwnerTreePeakRssBytes'],'newWholeOwnerTreePeakRssBytes':new['sampledWholeOwnerTreePeakRssBytes'],'oldSingleProcessPeakRssBytes':old['sampledSingleProcessPeakRssBytes'],'newSingleProcessPeakRssBytes':new['sampledSingleProcessPeakRssBytes'],'observedTreePeakReductionPercent':100*(old['sampledWholeOwnerTreePeakRssBytes']-new['sampledWholeOwnerTreePeakRssBytes'])/old['sampledWholeOwnerTreePeakRssBytes'],'newCrossPgidEdges':new['crossPgidEdgesObserved'],'measurementValid':True,'sampledNotContinuousPeak':True,'oldOomPreserved':old['exit']!=0,'priorFunctionalChecksNotRequalifiedOrCountedAgain':True}
 save('B-peak-result.json',result)
except BaseException as error:
 save('B-peak-invalid.json',{'error':repr(error),'ownerTreeConclusionAccepted':False});raise
