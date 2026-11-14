import time
from pathlib import Path
import hashlib,importlib.util,json,os,re,resource,selectors,shlex,signal,subprocess,sys
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
 assert subprocess.check_output(['git','status','--porcelain'],cwd=work,timeout=budget(3))==b''
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
try:
 original=Path(info['originalObserverDirectory']);assert sha(original/'preview-lifecycle-observer.cjs')=='ca777d7173d0ce88efde51f93c77c1b64362041153a2903da91ae5f073311ad4';assert sha(original/'preview-lifecycle-observer.test.cjs')=='1fbac1bcc315b56ffc04d0168225e4ec6b873f647d0046e1068149d0d95de296'
 code=execute('01-original-six',['node','--test','--test-reporter=tap','--test-concurrency=1',str(original/'preview-lifecycle-observer.test.cjs')],work,env)
 if code:raise RuntimeError('Original observer controls failed; browser NOTRUN')
 code=execute('02-current-observer-controls',['node','--test','--test-reporter=tap','--test-concurrency=1',str(root/'preview-lifecycle-observer.test.cjs'),str(root/'observer-metadata.test.cjs')],work,env)
 if code:raise RuntimeError('Current observer controls failed; browser NOTRUN')
 focused=env.copy();focused['NODE_OPTIONS']+=' --require '+str(root/'preview-lifecycle-observer.cjs');focused['OWNSOLID_PREVIEW_OBSERVER_DIRECTORY']=str(root/'events');focused['OWNSOLID_PREVIEW_OBSERVER_METADATA_ONLY']='1'
 code=execute('03-four-original-browser-files',['pnpm','exec','playwright','test','models.spec.ts','navigation.spec.ts','nodes.spec.ts','overview.spec.ts','--workers=1','--retries=0','--trace=off','--output',str(root/'test-results')],web/'apps/console',focused)
 save('focused-result.json',{'source':info['source'],'productTestExit':code,'observationErrors':observer.errors,'identitySamples':observer.samples,'fullGateQualified':False,'originalFullFailurePreserved':True,'newFixturesCreated':False});source();raise SystemExit(code if code>=0 else 128-code)
except BaseException as e:
 save('runner-terminal.json',{'errorClass':type(e).__name__,'detail':str(e),'stagesCompleted':len(results),'productExit':next((r['exit'] for r in results if r['name']=='03-four-original-browser-files'),None),'diagnosticErrors':observer.errors});raise
