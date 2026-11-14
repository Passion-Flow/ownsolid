import json,os,pathlib,shutil,subprocess,sys,time
os.umask(0o077)
root=pathlib.Path(sys.argv[1]); evidence=pathlib.Path(sys.argv[2]); out=evidence/'qualification-source59';out.mkdir()
env=os.environ.copy();env['GOFLAGS']='-p=2 -mod=readonly';env['TMPDIR']='/private/tmp/'
results=[]
def run(args,name,command_env=None):
    started=time.time()
    with (out/(name+'.log')).open('wb') as log:
        result=subprocess.run(args,cwd=root,env=command_env or env,stdout=log,stderr=subprocess.STDOUT,timeout=300)
    results.append({'name':name,'command':args,'exit_code':result.returncode,'seconds':round(time.time()-started,3)})
    (out/'results.json').write_text(json.dumps(results,indent=2)+'\n');print(name,result.returncode,flush=True)
    if result.returncode: raise RuntimeError(name+' failed')
(out/'input-sha').write_bytes(subprocess.check_output(['git','rev-parse','HEAD'],cwd=root))
(out/'input.patch').write_bytes(subprocess.check_output(['git','diff','--binary','HEAD'],cwd=root))
# Change only the ambient umask to establish the unrelated install test's cause.
run(['/bin/zsh','-c','umask 022; go -C deploy/offline test ./internal/install -run "^TestRestorePutsBackAMissingModelBeforeTheDatabaseChanges$" -count=1 -v'],'install-umask022-control')
run(['go','-C','services/control','test','./internal/modules/sso','./internal/modules/sso/http','-count=1'],'sso-unit')
run([str(evidence/'compile-live.sh'),str(root),str(evidence)],'compile')
run([str(evidence/'run-offline.sh'),str(root),str(evidence),'^TestConnectorSignInTest(HTTPS|BoundaryHTTPS|FailureRetryHTTPS|AuditRollbackHTTPS|ParallelCallbackHTTPS|RevokedActorHTTPS|ExpiryAndCleanupHTTPS|StaleConnectorHTTPS|CanceledExchangeHTTPS)$','1'],'native')
live=evidence/'gitea-source59';live.mkdir()
for file in ['provision-provider.py','run-live.sh','run-native-live.py','identity-http-live.test']:
    shutil.copy2(evidence/file,live/file)
run([sys.executable,str(live/'run-native-live.py'),str(root),str(live)],'actual-provider')
print('IAM_SOURCE59_TARGETED_QUALIFICATION_PASSED',flush=True)
