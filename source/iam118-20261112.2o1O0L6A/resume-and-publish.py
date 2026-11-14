import hashlib,json,os,pathlib,re,subprocess,sys,time,stat
os.umask(0o077)
root=pathlib.Path(sys.argv[1]);evidence=pathlib.Path(sys.argv[2]);run=evidence/'gate-resume-current3';run.mkdir()
receipts=[];attached=False;mount=pathlib.Path('/private/tmp/iam118-resume-'+str(os.getpid()));image=run/'scratch.sparseimage'
def git(*args):return subprocess.check_output(['git',*args],cwd=root,text=True).strip()
def execute(args,name,env=None,standard=False,timeout=1800):
    started=time.time()
    with (run/(name+'.log')).open('wb') as out:
        result=subprocess.run(args,cwd=root,env=env,stdout=out,stderr=subprocess.STDOUT,timeout=timeout,preexec_fn=(lambda:os.umask(0o022)) if standard else None)
    receipts.append({'name':name,'command':args,'exit_code':result.returncode,'seconds':round(time.time()-started,3),'toolchain_umask':'022' if standard else '077'})
    (run/'results.json').write_text(json.dumps(receipts,indent=2)+'\n');print(name,result.returncode,flush=True)
    if result.returncode:raise RuntimeError(name+' failed; retained raw log')
q=evidence/'qualification-source59'
if git('status','--porcelain'):raise SystemExit('IAM_WORKTREE_NOT_CLEAN')
q_results=json.loads((q/'results.json').read_text())
for name in ['install-umask022-control','sso-unit','compile','native']:
    if not any(x['name']==name and x['exit_code']==0 for x in q_results):raise SystemExit('IAM_NATIVE_PREREQUISITE_MISSING')
native=(q/'native.log').read_text();actual=(q/'actual-provider-retry2.log').read_text()
if len(re.findall(r'^--- PASS: TestConnectorSignInTest',native,re.M))!=9 or re.search(r'^--- (FAIL|SKIP):',native,re.M):raise SystemExit('IAM_NATIVE_CASES_INCOMPLETE')
if len(re.findall(r'^--- PASS: Test',actual,re.M))!=2 or re.search(r'^--- (FAIL|SKIP):',actual,re.M):raise SystemExit('IAM_ACTUAL_PROVIDER_CASES_INCOMPLETE')
execute(['git','fetch','origin'],'preflight-fetch',timeout=90)
execute(['git','rebase','origin/main'],'preflight-rebase',standard=True,timeout=90)
head=git('rev-parse','HEAD');(run/'input-sha').write_text(head+'\n')
browser=evidence/'gate-source59';original=(browser/'input-sha').read_text().strip();browser_paths=['web','api/openapi','tools/codegen','pkg/capabilities','Makefile']
execute(['git','diff','--exit-code',original,head,'--',*browser_paths],'browser-input-equality')
(run/'browser-reuse.json').write_text(json.dumps({'input_sha':original,'paths':browser_paths,'equal_to':head,'console_passed':1075,'portal_passed':389,'skipped':37,'log':str(browser/'test.log')},indent=2)+'\n')
qualified=(q/'input-sha').read_text().strip()
changed=subprocess.run(['git','diff','--quiet',qualified,head,'--','services/control','api','pkg/license','pkg/capabilities'],cwd=root).returncode
if changed:
    import shutil
    proof=run/'native-requalification';proof.mkdir()
    for file in ['provision-provider.py','run-live.sh','run-native-live.py','run-offline.sh']:
        shutil.copy2(evidence/file,proof/file)
    native_env=os.environ.copy();native_env.update({'GOOS':'linux','GOARCH':'arm64','CGO_ENABLED':'0','GOFLAGS':'-p=2 -mod=readonly','TMPDIR':'/private/tmp/'})
    previous=evidence/'gate-resume-current'
    previous_sha=(previous/'input-sha').read_text().strip()
    same_binary_input=subprocess.run(['git','diff','--quiet',previous_sha,head,'--','services/control','api','pkg/license','pkg/capabilities'],cwd=root).returncode==0
    if same_binary_input:
        shutil.copy2(previous/'native-requalification/identity-http-live.test',proof/'identity-http-live.test')
        (run/'compiler-reuse.json').write_text(json.dumps({'qualified_compile_sha':previous_sha,'current_sha':head,'sha256':hashlib.sha256((proof/'identity-http-live.test').read_bytes()).hexdigest()},indent=2)+'\n')
    else:
        execute(['go','-C','services/control','test','-c','-tags','sso_provider_integration','-o',str(proof/'identity-http-live.test'),'./internal/modules/identity/http'],'compile-native-requalification',native_env,timeout=180)
    shutil.copy2(proof/'identity-http-live.test',proof/'identity-http.test')
    execute([str(proof/'run-offline.sh'),str(root),str(proof),'^TestConnectorSignInTest(HTTPS|BoundaryHTTPS|FailureRetryHTTPS|AuditRollbackHTTPS|ParallelCallbackHTTPS|RevokedActorHTTPS|ExpiryAndCleanupHTTPS|StaleConnectorHTTPS|CanceledExchangeHTTPS)$','1'],'native-requalification',timeout=180)
    execute([sys.executable,str(proof/'run-native-live.py'),str(root),str(proof)],'actual-provider-requalification',timeout=360)
    n=(run/'native-requalification.log').read_text();a=(run/'actual-provider-requalification.log').read_text()
    if len(re.findall(r'^--- PASS: TestConnectorSignInTest',n,re.M))!=9 or len(re.findall(r'^--- PASS: Test',a,re.M))!=2 or re.search(r'^--- (FAIL|SKIP):',n+'\n'+a,re.M):raise SystemExit('IAM_REQUALIFICATION_INCOMPLETE')
else:
    (run/'native-reuse.json').write_text(json.dumps({'qualified_sha':qualified,'current_sha':head,'equal_paths':['services/control','api','pkg/license','pkg/capabilities']},indent=2)+'\n')
mount.mkdir(mode=0o700)
try:
    execute(['hdiutil','create','-size','8g','-fs','APFS','-type','SPARSE','-volname',mount.name,str(image)],'scratch-create',timeout=90)
    execute(['hdiutil','attach','-nobrowse','-mountpoint',str(mount),str(image)],'scratch-attach',timeout=90);attached=True
    env=os.environ.copy();env.update({'GOFLAGS':'-p=2 -mod=readonly','CARGO_BUILD_JOBS':'2','TMPDIR':str(mount)+'/', 'CI':'true','npm_config_offline':'true','OWNSOLID_PLAYWRIGHT_PORT_OFFSET':'20906'})
    env['PATH']=str(evidence/'gate-bin')+os.pathsep+env['PATH'];py=subprocess.check_output(['uv','python','find','3.12'],text=True).strip()
    execute(['make','check','PYTHON='+py],'check',env,True)
    execute(['make','build','PYTHON='+py],'build',env,True)
    execute(['make','-o','test-web-browser','test','PYTHON='+py],'test-continuation',env,True)
finally:
    if attached:
        done=subprocess.run(['hdiutil','detach',str(mount)],capture_output=True,timeout=60);(run/'scratch-detach.log').write_bytes(done.stdout+done.stderr)
        if done.returncode==0:mount.rmdir();image.unlink()
        else:raise RuntimeError('owned scratch volume remains mounted')
    elif mount.exists():mount.rmdir()
artifact=root/'tools/audit-verify/audit-verify'
if artifact.exists():
    s=artifact.lstat();tracked=subprocess.run(['git','ls-files','--error-unmatch',str(artifact.relative_to(root))],cwd=root,capture_output=True)
    if not stat.S_ISREG(s.st_mode) or s.st_uid!=os.geteuid() or tracked.returncode==0:raise SystemExit('IAM_BUILD_ARTIFACT_NOT_OWNED')
    artifact.unlink()
if git('rev-parse','HEAD')!=head or git('status','--porcelain'):raise SystemExit('IAM_GATE_SOURCE_CHANGED')
note=root/'docs/research/sso-interactive-connector-tests.md';text=note.read_text();marker='## Qualification boundary';assert marker in text
note.write_text(text.split(marker)[0]+(evidence/'qualification-note-template.md').read_text().replace('@INPUT@',head))
execute(['python3','tools/lint/layout/check_layout.py','.'],'publication-layout')
execute(['python3','tools/dev/roadmap_progress.py','--check'],'publication-roadmap')
execute(['git','diff','--check'],'publication-diff')
execute(['git','add','--','docs/research/sso-interactive-connector-tests.md'],'publication-stage')
execute(['git','commit','-m','IAM-118: Record qualified schema 59 diagnostic sign-in candidate','-m','Verify nine native PostgreSQL HTTPS cases and two actual Gitea flows. Pass repository checks/build and the remaining test stages under standard umask 022, reusing byte-identical browser inputs already tested. Retain the earlier strict-umask fixture failure and all production admission boundaries.'],'publication-commit')
published=git('rev-parse','HEAD');receipt={'qualified_runtime_sha':head,'publication_sha':published,'browser_reused':True,'post_gate_edits':['docs/research/sso-interactive-connector-tests.md'],'pushed':False}
(evidence/'publication.json').write_text(json.dumps(receipt,indent=2)+'\n')
execute(['git','fetch','origin'],'publication-fetch',timeout=90)
if subprocess.run(['git','merge-base','--is-ancestor','origin/main','HEAD'],cwd=root).returncode:raise SystemExit('IAM_REBASE_REQUIRED_BEFORE_PUSH')
execute(['git','push','origin','HEAD:main'],'publication-push',timeout=90)
receipt['pushed']=True;(evidence/'publication.json').write_text(json.dumps(receipt,indent=2)+'\n')
state=json.loads((evidence/'resume-state.json').read_text());state.update(head=published,phase='IAM-118 backend candidate qualified and published; full item remains open',publication=str(evidence/'publication.json'),next='AUD-108 identity real read consumer; no new Linux environment')
(evidence/'resume-state.json').write_text(json.dumps(state,indent=2)+'\n');print('IAM_CANDIDATE_PUBLISHED',published,flush=True)
