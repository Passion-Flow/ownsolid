import os,pathlib,subprocess,sys
p=pathlib.Path(__file__).parent;repo=pathlib.Path((p/'repository-path').read_text().strip())
env=os.environ|{'GOWORK':'off','GOTOOLCHAIN':'local','GOPROXY':'off','GOSUMDB':'off','GOFLAGS':'-p=1 -mod=readonly','GOMAXPROCS':'2','GOMEMLIMIT':'512MiB'}
with (p/'live-module-sum-admission.log').open('xb') as out:
 r=subprocess.run(['go','-C','services/control','mod','download','-json','-modfile='+str(p/'control.mod'),'golang.org/x/net@v0.61.0'],cwd=repo,env=env,stdout=out,stderr=subprocess.STDOUT,timeout=30)
if r.returncode:raise SystemExit(r.returncode)
raise SystemExit(subprocess.run([sys.executable,str(p/'check-current-owner.py')],cwd=repo).returncode)
