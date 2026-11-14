import time
from pathlib import Path
import hashlib,importlib.util,json,os,re,resource,selectors,shlex,signal,subprocess,sys,shutil,tarfile
root=Path(sys.argv[1]);deadline=float(sys.argv[2]);hard=float(sys.argv[3]);info=json.loads((root/'preparation.json').read_text());work=Path(info['worktree']);web=work/'web';fd=int(os.environ['OWNSOLID_GATE_FD']);guard=int(os.environ['OWNSOLID_GATE_PARENT']);resource.setrlimit(resource.RLIMIT_CORE,(0,0))
def now():return time.clock_gettime(time.CLOCK_MONOTONIC)
def budget(cap=600):
 left=deadline-now()
 if left<=0:raise TimeoutError('Directed work cutoff reached')
 return min(cap,left)
def expired(s,f):raise TimeoutError('Directed work cutoff reached')
signal.signal(signal.SIGALRM,expired);signal.setitimer(signal.ITIMER_REAL,budget());signal.signal(signal.SIGTERM,lambda s,f:sys.exit(143))
def save(name,data):(root/name).write_text(json.dumps(data,indent=2)+'\n')
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def source():
 assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=work,text=True,timeout=budget(3)).strip()==info['source']
 for n,h in json.loads((root/'source-file-sha256.json').read_text()).items():budget();assert sha(work/n)==h,n
 if not (root/'owned-resources.json').exists():assert subprocess.check_output(['git','status','--porcelain'],cwd=work,timeout=budget(3))==b''
source()
for app,files in json.loads((root/'asset-inputs.json').read_text())['assetFiles'].items():
 for n,h in files.items():budget();assert sha(work/n)==h,n
tokens=shlex.split(os.environ.get('NODE_OPTIONS',''));cap=512
for i,t in enumerate(tokens):
 key,sep,value=t.partition('=');key=key.replace('_','-')
 if key in ['--max-heap-size','--max-old-space-size-percentage']:raise ValueError('Absolute heap cap required')
 if key=='--max-old-space-size':value=value if sep else tokens[i+1];assert value.isascii() and value.isdecimal() and int(value)>0;cap=min(cap,int(value))
env=os.environ.copy();env.update(NODE_OPTIONS=os.environ.get('NODE_OPTIONS','')+' --max-old-space-size='+str(cap),npm_config_offline='true',CI='1',VITEST_MAX_WORKERS='1',OWNSOLID_PLAYWRIGHT_PORT_OFFSET='26726',DEBUG='pw:webserver',PYTHONDONTWRITEBYTECODE='1')
save('effective-controls.json',{'nodeMaximumOldSpaceMiB':cap,'workers':1,'retries':0,'offlineDependencies':True,'noInstallOrBuild':True,'exactSourceAssetsVerified':1233,'privacy':'Network trace off; collector records no headers/body/query/fragment. Preview stderr content is hashed with allowlisted error codes only.','testAssertionsUnchanged':True,'samplerMemoryFields':False})
spec=importlib.util.spec_from_file_location('preview_identity',root/'owned-preview-observer.py');native=importlib.util.module_from_spec(spec);spec.loader.exec_module(native);observer=native.Observer(root,guard,deadline)
results=[]
urlpattern=re.compile(r'https?://[^\s\"\'<>\)]+')
def clean_line(line):
 def cleanurl(m):
  from urllib.parse import urlsplit,urlunsplit
  try:
   u=urlsplit(m.group(0));host=u.hostname or '';host=('['+host+']') if ':' in host else host;return urlunsplit((u.scheme,host+(':'+str(u.port) if u.port else ''),u.path,'',''))
  except Exception:return '[redacted-url]'
 line=urlpattern.sub(cleanurl,line)
 line=re.sub(r'(?i)\b(?:authorization|proxy-authorization|cookie|set-cookie|password|secret|token)\s*[:=]\s*[^\r\n]*','[redacted-sensitive-field]',line)
 return line
def execute(name,args,cwd,childenv):
 source();(root/'stage').write_text(name+'\n');started=now();print('DIRECTED_STAGE',name,flush=True)
 child=subprocess.Popen(args,cwd=cwd,env=childenv,stdin=subprocess.DEVNULL,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,pass_fds=(fd,),umask=0o077);sel=selectors.DefaultSelector();sel.register(child.stdout,selectors.EVENT_READ);pending=b'';digest=hashlib.sha256();next_sample=0.;rawbytes=0
 with (root/(name+'.log')).open('x') as out:
  while True:
   budget()
   if now()>=next_sample:
    try:observer.tick()
    except Exception as e:observer.event('observer-tick-failed',errorClass=type(e).__name__);observer.errors+=1
    next_sample=now()+1
   for key,mask in sel.select(timeout=budget(.1)):
    block=os.read(key.fileobj.fileno(),65536)
    if not block:sel.unregister(key.fileobj);continue
    digest.update(block);rawbytes+=len(block);pending+=block
    while b'\n' in pending:
     line,pending=pending.split(b'\n',1);out.write(clean_line(line.decode(errors='replace'))+'\n');out.flush()
   if child.poll() is not None and not sel.get_map():break
  if pending:out.write(clean_line(pending.decode(errors='replace')))
 code=child.wait(timeout=budget(2));child.stdout.close();sel.close();result={'name':name,'argv':args,'exit':code,'seconds':now()-started,'capturedBytes':rawbytes,'rawStreamSha256':digest.hexdigest(),'outputRedactedBeforePersistence':True};results.append(result);save('stage-results.json',results);print(json.dumps(result),flush=True);return code

def clean_targeted():
 started=now();guard_group=os.getpgid(guard);attempts=[]
 def live():
  found=[]
  for pid,birth in list(observer.known.items()):
   try:
    row=native.identity(pid)
    if row['startTime']==birth and row['status']!=5 and row['pgid'] not in [guard_group,os.getpgrp()]:found.append(row)
   except RuntimeError:pass
  return found
 for signum in [signal.SIGTERM,signal.SIGKILL]:
  for row in live():
   budget()
   try:
    current=native.identity(row['pid'])
    if current['startTime']!=row['startTime']:continue
    os.kill(row['pid'],signum);attempts.append({'pid':row['pid'],'startTime':row['startTime'],'requestedSignal':signum,'utc':native.utc()})
   except (ProcessLookupError,RuntimeError):pass
  until=min(deadline,now()+1)
  while live() and now()<until:time.sleep(min(.05,budget(.05)))
 remaining=live();ports=subprocess.run(['lsof','-nP','-iTCP:31036','-iTCP:31037','-sTCP:LISTEN'],capture_output=True,text=True,timeout=budget(2));save('targeted-cleanup-confirmed.json',{'confirmed':not remaining and ports.returncode==1,'remaining':remaining,'signalsRequestedNotExitStatuses':attempts,'portsExit':ports.returncode,'seconds':now()-started})
 (root/'owned-stage-groups.json').write_text(json.dumps([os.getpgrp()])+'\n')
 if remaining or ports.returncode!=1:raise RuntimeError('Targeted owned cleanup not proven; full NOTRUN')

def tools_unchanged():
 for name,item in json.loads((root/'current-executable-sha256.json').read_text()).items():budget();assert sha(Path(item['path']))==item['sha256'],name

try:
 assert sha(root/'preview-lifecycle-observer.cjs')=='620136aeaea77198a5ea7e62f3dedfe57c0cd13b0247d5818236a1e21b859d3d'
 focused=env.copy();focused['NODE_OPTIONS']+=' --require '+str(root/'preview-lifecycle-observer.cjs');focused['OWNSOLID_PREVIEW_OBSERVER_DIRECTORY']=str(root/'events');focused['OWNSOLID_PREVIEW_OBSERVER_METADATA_ONLY']='1'
 code=execute('01-complete-node-page',['pnpm','exec','playwright','test',r'(?:^|/)e2e/node-page[.]spec[.]ts$','--workers=1','--retries=0','--trace=off','--output',str(root/'node-page-results')],web/'apps/console',focused)
 expected=json.loads((root/'expected-node-page-cases.json').read_text());cases=[]
 for line in (root/'01-complete-node-page.log').read_text().splitlines():
  match=re.match(r'^\s*([✓✘-])\s+(\d+)\s+\[chromium\]\s+›\s+(.*)$',line)
  if match:cases.append({'status':{'✓':'passed','✘':'failed','-':'skipped'}[match[1]],'case':re.sub(r' \([0-9.]+(?:ms|s)\)$','',match[3])})
 missing=[name for name in expected if not any(row['case']==name and row['status']=='passed' for row in cases)]
 save('node-page-pass-proof.json',{'source':info['source'],'exit':code,'actualCases':cases,'expectedOldFailedCases':expected,'missingOldFailedCases':missing,'onlyExactFile':all(row['case'].startswith('e2e/node-page.spec.ts:') for row in cases),'newSkipsOrRetries':False})
 if code:raise SystemExit(code if code>=0 else 128-code)
 if missing or len(cases)!=10 or any(row['status']!='passed' for row in cases):raise RuntimeError('Exact complete node-page coverage did not reconcile; full NOTRUN')
 clean_targeted();source();tools_unchanged()
 memory=subprocess.check_output(['vm_stat'],timeout=budget(2));(root/'full-fresh-memory.log').write_bytes(memory);pages=int(re.search(rb'Pages free:\s+(\d+)',memory).group(1));size=int(re.search(rb'page size of (\d+)',memory).group(1));free=pages*size;disk=shutil.disk_usage(root).free
 save('full-fresh-resource.json',{'rawFreeBytes':free,'diskFreeBytes':disk,'requiredRawFreeBytes':6*1024**3,'requiredDiskFreeBytes':30*1024**3,'singleImmediateSampleAfterOwnedTargetedCleanup':True})
 if free<6*1024**3 or disk<30*1024**3:
  save('full-stage-notrun.json',{'status':'NOTRUN','reason':'Fresh original full resource floor not met; no wait or full child','rawFreeBytes':free,'diskFreeBytes':disk,'directedExit':code});raise SystemExit(0)
 assert not (work/'tools/audit-verify/audit-verify').exists();assert not os.path.lexists(work/'go.work.sum');assert not [k for k,v in os.environ.items() if k.startswith('PROJECT_ONE_') and 'DSN' in k and v]
 ports=subprocess.run(['lsof','-nP','-iTCP:31036','-iTCP:31037','-sTCP:LISTEN'],capture_output=True,text=True,timeout=budget(2));assert ports.returncode==1
 entry=hard-600;phase_start=now();hard=min(entry+2400,phase_start+1800);deadline=hard-120
 phase={'phase':'F','phaseStart':phase_start,'hardDeadlineMonotonic':hard,'workCutoffMonotonic':deadline,'rawFreeBytes':free,'diskFreeBytes':disk,'targetedPassed':True};temp=root/'phase-budget.json.tmp';temp.write_text(json.dumps(phase)+'\n');temp.replace(root/'phase-budget.json');observer.deadline=deadline;observer.ports=[31036,31037];signal.setitimer(signal.ITIMER_REAL,budget(1800))
 full=focused.copy();fullcap=4096
 for i,t in enumerate(tokens):
  key,sep,value=t.partition('=');key=key.replace('_','-')
  if key=='--max-old-space-size':fullcap=min(fullcap,int(value if sep else tokens[i+1]))
 def positive(name,maximum):
  n=int(os.environ.get(name,str(maximum)));assert n>0;return str(min(n,maximum))
 full.update(NODE_OPTIONS=os.environ.get('NODE_OPTIONS','')+' --max-old-space-size='+str(fullcap)+' --require '+str(root/'preview-lifecycle-observer.cjs'),GOPROXY='off',GOSUMDB='off',GOTOOLCHAIN='local',GOMAXPROCS=positive('GOMAXPROCS',2),GOFLAGS=os.environ.get('GOFLAGS','')+' -p=1',CARGO_NET_OFFLINE='true',CARGO_BUILD_JOBS=positive('CARGO_BUILD_JOBS',1),RUST_TEST_THREADS=positive('RUST_TEST_THREADS',1),MAKEFLAGS='-j1',PATH=str(root/'bin')+':'+os.environ['PATH'],OWNSOLID_GATE_PACKET=str(root))
 full['PYTHON']=subprocess.check_output(['uv','python','find','3.12'],text=True,timeout=budget(5)).strip()
 save('full-effective-controls.json',{'nodeOldSpaceMiB':fullcap,'goP':1,'gomaxprocs':int(full['GOMAXPROCS']),'cargoJobs':int(full['CARGO_BUILD_JOBS']),'rustTestThreads':int(full['RUST_TEST_THREADS']),'workers':1,'retries':0,'networkMode':'Offline tool caches, not disconnected installed acceptance','originalTargets':'Original Makefile unchanged','traceArtifactPrivacyOnly':'off','oldThreeTargetReuse':'Not unconditionally transferred because historical complete executable byte hashes were not recorded; necessary original targets requalified here.'})
 mount=Path('/private/tmp/ownsolid-por112-joint-'+str(os.getpid()));assert not mount.exists();mount.mkdir(mode=0o700)
 save('owned-resources.json',{'mount':str(mount),'image':str(root/'scratch.sparseimage'),'source':info['source'],'auditBinaryCreatedOnlyAfterBuild':True,'workspaceSumAbsentAtEntry':True})
 for name,args in [('full-scratch-create',['hdiutil','create','-size','12g','-type','SPARSE','-fs','APFS','-volname','ownsolid-por112-joint',str(root/'scratch')]),('full-scratch-attach',['hdiutil','attach','-nobrowse','-owners','on','-mountpoint',str(mount),str(root/'scratch.sparseimage')])]:
  code=execute(name,args,work,full)
  if code:raise RuntimeError(name+' exited '+str(code))
 (mount/'tmp').mkdir(mode=0o700);full['TMPDIR']=str(mount/'tmp')+'/'
 for target in ['web-install','check','build','test']:
  source();tools_unchanged();code=execute('full-'+target,['make',target],work,full)
  if code:raise SystemExit(code if code>=0 else 128-code)
 source();tools_unchanged();assets={}
 for app in ['console','portal']:assets[app]={str(f.relative_to(work)):sha(f) for f in sorted((work/'web/apps'/app/'dist').rglob('*')) if f.is_file()}
 save('full-built-assets.json',{'source':info['source'],'tree':info['tree'],'webTree':info['webTree'],'files':assets})
 with tarfile.open(root/'full-built-assets.tar.gz','x:gz') as archive:
  for app,files in assets.items():
   for name in files:budget();archive.add(work/name,arcname=name,recursive=False)
 save('full-asset-archive.json',{'source':info['source'],'sha256':sha(root/'full-built-assets.tar.gz'),'files':sum(map(len,assets.values())),'originalFullTestActuallyPassed':True})
 save('full-stage-result.json',{'exit':0,'originalMakeTestExit':0,'source':info['source'],'oldFailurePreserved':True,'installedAcceptanceClaimed':False});(root/'stage').write_text('complete\n')
except BaseException as e:
 save('runner-terminal.json',{'errorClass':type(e).__name__,'detail':str(e),'stagesCompleted':len(results),'actualResults':results,'observationErrors':observer.errors});raise
