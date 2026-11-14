import hashlib,json,os,pathlib,shutil,stat,subprocess,sys,tempfile,time
os.umask(0o077)
repo=pathlib.Path(sys.argv[1]);evidence=pathlib.Path(__file__).parent
run=pathlib.Path(tempfile.mkdtemp(prefix='current-stage-',dir=evidence))
(evidence/'latest-current-stage-path').write_text(str(run)+'\n')
records=[];attached=False;mount=pathlib.Path('/private/tmp/iam102-gate-'+str(os.getpid()));image=run/'scratch.sparseimage'
paths=['services/control/internal/modules/sso/README.md','services/control/internal/modules/sso/oidc_profile_schema_repository_integration_test.go','services/control/internal/modules/sso/testdata/oidc_profile.up.sql','services/control/internal/modules/sso/testdata/oidc_profile.down.sql']
def git(*args):return subprocess.check_output(['git',*args],cwd=repo,text=True).strip()
def hashes():return {p:hashlib.sha256((repo/p).read_bytes()).hexdigest() for p in paths}
if git('status','--porcelain'):raise RuntimeError('clean source required before synchronization')
for label,args in [('preflight-fetch',['git','fetch','origin']),('preflight-rebase',['git','rebase','origin/main'])]:
 with (run/(label+'.log')).open('wb') as out:result=subprocess.run(args,cwd=repo,stdout=out,stderr=subprocess.STDOUT,timeout=120)
 if result.returncode:raise RuntimeError(label+' failed before qualification')
head=git('rev-parse','HEAD');before=hashes();status=git('status','--porcelain')
(run/'input.json').write_text(json.dumps({'head':head,'status':status,'candidate_hashes':before},indent=2)+'\n')
for path in paths:
 target=run/'source'/path;target.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(repo/path,target)
(run/'disk.log').write_bytes(subprocess.check_output(['df','-h','/']))
def execute(cmd,name,env=None,standard=False,timeout=1800):
 started=time.time()
 with (run/(name+'.log')).open('wb') as out:
  r=subprocess.run(cmd,cwd=repo,env=env,stdout=out,stderr=subprocess.STDOUT,timeout=timeout,preexec_fn=(lambda:os.umask(0o022)) if standard else None)
 records.append({'name':name,'command':cmd,'exit_code':r.returncode,'seconds':round(time.time()-started,3)})
 (run/'results.json').write_text(json.dumps(records,indent=2)+'\n');print(name,r.returncode,flush=True)
 if r.returncode:raise RuntimeError(name+' failed; original log retained')
execute(['python3',str(evidence/'storage-prototype/qualify-full.py'),str(repo)],'full-schema-candidate',timeout=450)
execute(['python3',str(evidence/'qualify-identity.py'),str(repo)],'identity-consumers',timeout=650)
execute(['python3',str(evidence/'qualify-prototype-current.py'),str(evidence)],'current-protocol',timeout=600)
previous=pathlib.Path((evidence.parent/'iam118-active-path').read_text().strip());reference=(previous/'gate-source59/input-sha').read_text().strip()
reference_log=str(previous/'gate-source59/test.log')
if (evidence/'last-qualified-stage-path').exists():
 prior=pathlib.Path((evidence/'last-qualified-stage-path').read_text().strip());receipt=json.loads((prior/'success.json').read_text());reference=receipt['input_head']
 reference_log=json.loads((prior/'browser-inputs.json').read_text())['reference_log'] if receipt['browser_reused'] else str(prior/'test.log')
browser_paths=['web','api/openapi','tools/codegen','pkg/capabilities','Makefile']
if (evidence/'browser-reference.json').exists():
 external=json.loads((evidence/'browser-reference.json').read_text())
 if subprocess.run(['git','diff','--quiet',external['source'],head,'--',*browser_paths],cwd=repo).returncode==0:
  if hashlib.sha256(pathlib.Path(external['log']).read_bytes()).hexdigest()!=external['log_sha256']:raise RuntimeError('browser reference changed')
  reference=external['source'];reference_log=external['log']
reuse=subprocess.run(['git','diff','--quiet',reference,head,'--',*browser_paths],cwd=repo).returncode==0
(run/'browser-inputs.json').write_text(json.dumps({'reference_sha':reference,'current_sha':head,'paths':browser_paths,'reused':reuse,'reference_log':reference_log},indent=2)+'\n')
mount.mkdir(mode=0o700)
try:
 execute(['hdiutil','create','-size','8g','-fs','APFS','-type','SPARSE','-volname',mount.name,str(image)],'scratch-create',timeout=90)
 execute(['hdiutil','attach','-nobrowse','-mountpoint',str(mount),str(image)],'scratch-attach',timeout=90);attached=True
 env=os.environ.copy();env.update({'GOFLAGS':'-p=2 -mod=readonly','CARGO_BUILD_JOBS':'2','TMPDIR':str(mount)+'/', 'CI':'true','npm_config_offline':'true','OWNSOLID_PLAYWRIGHT_PORT_OFFSET':'20906'})
 env['PATH']=str(previous/'gate-bin')+os.pathsep+env['PATH'];python=subprocess.check_output(['uv','python','find','3.12'],text=True).strip()
 for target in ['check','build']:execute(['make',target,'PYTHON='+python],target,env,True)
 execute(['make']+(['-o','test-web-browser'] if reuse else [])+['test','PYTHON='+python],'test',env,True)
finally:
 if attached:
  detached=subprocess.run(['hdiutil','detach',str(mount)],capture_output=True,timeout=60);(run/'scratch-detach.log').write_bytes(detached.stdout+detached.stderr)
  if detached.returncode==0:mount.rmdir();image.unlink()
  else:raise RuntimeError('owned scratch volume still mounted')
 elif mount.exists():mount.rmdir()
artifact=repo/'tools/audit-verify/audit-verify'
if artifact.exists():
 state=artifact.lstat();tracked=subprocess.run(['git','ls-files','--error-unmatch',str(artifact.relative_to(repo))],cwd=repo,capture_output=True)
 if not stat.S_ISREG(state.st_mode) or state.st_uid!=os.geteuid() or tracked.returncode==0:raise RuntimeError('unrecognized build artifact')
 artifact.unlink()
if git('rev-parse','HEAD')!=head or hashes()!=before or git('status','--porcelain')!=status:raise RuntimeError('source changed during qualification')
(run/'success.json').write_text(json.dumps({'input_head':head,'candidate_hashes':before,'full_schema_run':(evidence/'storage-prototype/latest-full-schema-path').read_text().strip(),'protocol_run':(evidence/'latest-qualification-path').read_text().strip(),'identity_run':(evidence/'latest-identity-consumer-path').read_text().strip(),'browser_reused':reuse,'gate_commands':'make check; make build; make -o test-web-browser test when exact browser inputs match','scope':'PostgreSQL storage candidate and external protocol prototype; no generic serving route, UI, formal migration or dependency admission'},indent=2)+'\n')
(evidence/'last-qualified-stage-path').write_text(str(run)+'\n')
print('IAM102_CURRENT_STAGE_QUALIFIED',run,flush=True)
