import json,os,pathlib,subprocess,sys,time
root=pathlib.Path(sys.argv[1]);proto=root/'prototype';receipts=[]
env=os.environ.copy();env.update({'GOWORK':'off','GOTOOLCHAIN':'local','GOFLAGS':'-p=2','GOPROXY':'off'})
def run(cmd,name,timeout=240):
 started=time.time()
 with (root/(name+'.log')).open('wb') as out:r=subprocess.run(cmd,cwd=proto,env=env,stdout=out,stderr=subprocess.STDOUT,timeout=timeout)
 receipts.append({'name':name,'command':cmd,'exit_code':r.returncode,'seconds':round(time.time()-started,3)});(root/'initial-results.json').write_text(json.dumps(receipts,indent=2)+'\n');print(name,r.returncode,flush=True)
 if r.returncode:raise SystemExit(r.returncode)
run(['gofmt','-w','adapter.go','adapter_test.go','live_test.go'],'format')
run(['go','mod','tidy'],'module-resolution')
run(['go','test','-race','-count=1','-v','./...'],'adversarial-native')
run(['docker','pull','quay.io/keycloak/keycloak@sha256:923a431c08971d49727a857e64870d8d63b5bf294a730e5dde729b5138c26c9e'],'image-pull',timeout=300)
