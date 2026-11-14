import json,os,pathlib,shutil,subprocess,sys,time,signal
os.umask(0o077)
root=pathlib.Path(sys.argv[1]); evidence=pathlib.Path(sys.argv[2])
run=evidence/'gate-source59';run.mkdir()
image=run/'scratch.sparseimage';mount=pathlib.Path('/private/tmp/iam118-gate-'+str(os.getpid()))
mount.mkdir(mode=0o700);attached=False
receipts=[]
def execute(args,name,env=None,timeout=1200):
    started=time.time()
    with (run/(name+'.log')).open('wb') as out:
        result=subprocess.run(args,cwd=root,env=env,stdout=out,stderr=subprocess.STDOUT,timeout=timeout)
    receipts.append({'name':name,'command':args,'exit_code':result.returncode,'duration_seconds':round(time.time()-started,2)})
    (run/'results.json').write_text(json.dumps(receipts,indent=2)+'\n')
    print(name,result.returncode,flush=True)
    if result.returncode: raise RuntimeError(name+' failed; retained log at '+str(run/(name+'.log')))
try:
    signal.signal(signal.SIGTERM,lambda *_:sys.exit(143))
    (run/'input-sha').write_bytes(subprocess.check_output(['git','rev-parse','HEAD'],cwd=root))
    (run/'input.patch').write_bytes(subprocess.check_output(['git','diff','--binary','HEAD'],cwd=root))
    (run/'input-status').write_bytes(subprocess.check_output(['git','status','--short'],cwd=root))
    (run/'resource-preflight.log').write_bytes(subprocess.check_output(['df','-h','/']))
    execute(['hdiutil','create','-size','8g','-fs','APFS','-type','SPARSE','-volname',mount.name,str(image)],'scratch-create',timeout=90)
    execute(['hdiutil','attach','-nobrowse','-mountpoint',str(mount),str(image)],'scratch-attach',timeout=90);attached=True
    env=os.environ.copy();env.update({'GOFLAGS':'-p=2 -mod=readonly','CARGO_BUILD_JOBS':'2','TMPDIR':str(mount)+'/', 'CI':'true','npm_config_offline':'true','OWNSOLID_PLAYWRIGHT_PORT_OFFSET':'20906'})
    env['PATH']=str(evidence/'gate-bin')+os.pathsep+env['PATH']
    py=subprocess.check_output(['uv','python','find','3.12'],text=True).strip()
    for target in ['check','build','test']:
        execute(['make',target,'PYTHON='+py],target,env,timeout=1800)
    # Only the changed parent schema requires renewed native qualification.
    execute([str(evidence/'compile-live.sh'),str(root),str(evidence)],'compile-native',env,timeout=180)
    execute([str(evidence/'run-offline.sh'),str(root),str(evidence),'^TestConnectorSignInTest(HTTPS|BoundaryHTTPS|FailureRetryHTTPS|AuditRollbackHTTPS|ParallelCallbackHTTPS|RevokedActorHTTPS|ExpiryAndCleanupHTTPS|StaleConnectorHTTPS|CanceledExchangeHTTPS)$','1'],'native',env,timeout=180)
    live=evidence/'gitea-source59';live.mkdir()
    for file in ['provision-provider.py','run-live.sh','run-native-live.py','identity-http-live.test']:
        shutil.copy2(evidence/file,live/file)
    execute([sys.executable,str(live/'run-native-live.py'),str(root),str(live)],'actual-provider',env,timeout=360)
    print('IAM_FULL_GATE_PASSED',flush=True)
finally:
    if attached:
        result=subprocess.run(['hdiutil','detach',str(mount)],capture_output=True,timeout=60)
        (run/'scratch-detach.log').write_bytes(result.stdout+result.stderr)
        if result.returncode==0:
            mount.rmdir();image.unlink()
    elif mount.exists(): mount.rmdir()
