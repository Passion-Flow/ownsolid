import time
from pathlib import Path
import hashlib,json,os,re,resource,signal,subprocess,sys
assert sys.flags.isolated and not sys.flags.optimize
root=Path(sys.argv[1]);initial_work=float(sys.argv[2]);global_hard=float(sys.argv[3]);info=json.loads((root/'preparation.json').read_text());work=Path(info['worktree']);selected=json.loads((root/'selected-scope.json').read_text());fd=int(os.environ['OWNSOLID_GATE_FD']);resource.setrlimit(resource.RLIMIT_CORE,(0,0));scopes=json.loads((root/'scope-inputs.json').read_text())
def now():return time.clock_gettime(time.CLOCK_MONOTONIC)
def remaining(cap,deadline):
 value=deadline-now()
 if value<=0:raise TimeoutError('Controller phase deadline reached')
 return min(cap,value)
def capture_groups():
 raw=subprocess.check_output(['ps','-axo','pid=,ppid=,pgid='],text=True,timeout=remaining(3,global_hard-120));rows=[tuple(map(int,l.split())) for l in raw.splitlines() if l.strip()];pids={os.getpid()}
 while True:
  added={pid for pid,parent,group in rows if parent in pids}-pids
  if not added:break
  pids.update(added)
 groups={group for pid,parent,group in rows if pid in pids}
 path=root/'owned-stage-groups.json'
 if path.exists():groups.update(json.loads(path.read_text()))
 path.write_text(json.dumps(sorted(groups))+'\n')
def source(name,deadline):
 target=scopes[name];assert subprocess.check_output(['git','status','--porcelain'],cwd=work,timeout=remaining(5,deadline))==b''
 subprocess.run(['git','switch','--detach',target['source']],cwd=work,check=True,timeout=remaining(10,deadline))
 assert subprocess.check_output(['git','rev-parse','HEAD^{tree}'],cwd=work,text=True,timeout=remaining(3,deadline)).strip()==target['tree']
 for path,h in json.loads((root/target['manifest']).read_text()).items():remaining(5,deadline);assert hashlib.sha256((work/path).read_bytes()).hexdigest()==h,path
 (root/('tested-source-'+name+'.json')).write_text(json.dumps(target,indent=2)+'\n')
def execute(name,script,deadline,hard):
 source(name,deadline);print('AUTHORIZED_PHASE',name,flush=True)
 args=[sys.executable,'-I',str(root/script),str(root),str(deadline)]
 if name=='A':args.append(str(hard))
 child=subprocess.Popen(args,pass_fds=(fd,));code=child.wait(timeout=remaining(2400,deadline));(root/(name+'-worker-exit')).write_text(str(code)+'\n')
 if code:
  capture_groups();raise RuntimeError(name+' worker exited '+str(code))
 # Do not carry stale stage PGIDs into a different phase. An extant foreign or
 # unknown group prevents phase advancement rather than being killed here.
 groups=json.loads((root/'owned-stage-groups.json').read_text()) if (root/'owned-stage-groups.json').exists() else []
 live=subprocess.check_output(['ps','-axo','pgid=,stat='],text=True,timeout=remaining(3,hard-5));left=[]
 for line in live.splitlines():
  group,status=line.split()
  if int(group) in groups and int(group)!=os.getpgrp() and not status.startswith('Z'):left.append(line)
 assert not left,'Phase process group remains'
 (root/'owned-stage-groups.json').write_text(json.dumps([os.getpgrp()])+'\n')
 assert subprocess.check_output(['git','status','--porcelain'],cwd=work,timeout=remaining(5,hard-5))==b''
 (root/(name+'-phase-cleanup-confirmed.json')).write_text(json.dumps({'workerExit':0,'extraLiveGroups':left,'sourceClean':True,'sameCarrierFdStillHeldByParent':True})+'\n')
try:
 if selected['scope']=='A':execute('A','full-run.py',initial_work,selected['hardDeadlineMonotonic'])
 else:
  execute('B','measure-b.py',initial_work,selected['hardDeadlineMonotonic'])
  result=json.loads((root/'B-peak-result.json').read_text());assert result['measurementValid']
  memory=subprocess.check_output(['vm_stat'],timeout=remaining(2,selected['hardDeadlineMonotonic']-10));(root/'after-B-A-memory.log').write_bytes(memory);page=int(re.search(rb'page size of (\d+)',memory).group(1));pages=int(re.search(rb'Pages free:\s+(\d+)',memory).group(1))
  import shutil
  disk=shutil.disk_usage(work).free;decision={'rawFreeBytes':pages*page,'diskFreeBytes':disk,'BComplete':True,'AStarted':False,'sameFdNoSecondNb':True}
  if pages*page>=6*1024**3 and disk>=30*1024**3:
   start=now();hard=min(global_hard,start+1800);cutoff=hard-120
   decision.update(AStarted=True,phaseStartMonotonic=start,hardDeadlineMonotonic=hard,workCutoffMonotonic=cutoff)
   budget={'phase':'A','source':scopes['A']['source'],'phaseStartMonotonic':start,'hardDeadlineMonotonic':hard,'workCutoffMonotonic':cutoff,'BComplete':True,'rawFreeBytes':pages*page,'diskFreeBytes':disk};(root/'phase-budget.json.tmp').write_text(json.dumps(budget)+'\n');(root/'phase-budget.json.tmp').replace(root/'phase-budget.json');(root/'A-activation.json').write_text(json.dumps(decision,indent=2)+'\n');execute('A','full-run.py',cutoff,hard)
  else:
   decision['A']='NOTRUN: fresh original 6GiB/30GiB floors not met; no waiting or A attempt';(root/'A-after-B-notrun.json').write_text(json.dumps(decision,indent=2)+'\n');print('A_NOTRUN_ORIGINAL_RESOURCE_FLOOR',flush=True)
except BaseException as error:
 try:capture_groups()
 except BaseException as capture_error:(root/'controller-cleanup-unknown.json').write_text(repr(capture_error)+'\n')
 (root/'controller-failure.json').write_text(json.dumps({'error':repr(error),'retry':False})+'\n');raise
