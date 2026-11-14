import hashlib,json,os,pathlib,shutil,signal,subprocess,tempfile,uuid
os.umask(0o077)
p=pathlib.Path(__file__).parent;proto=p/'prototype'
if shutil.disk_usage(p).free<15*1024**3:raise RuntimeError('disk resource refusal')
for source in json.loads((p/'inherited-inputs.json').read_text()):
 if hashlib.sha256(pathlib.Path(source['path']).read_bytes()).hexdigest()!=source['sha256']:raise RuntimeError('inherited legal/module input changed')
for name in ['go.mod','go.sum']:
 if (proto/name).read_bytes()!=pathlib.Path(json.loads((p/'inherited-inputs.json').read_text())[0]['path']).with_name(name).read_bytes():raise RuntimeError('unreviewed module graph change')
stage=pathlib.Path(tempfile.mkdtemp(prefix='qualification-',dir=p));(p/'active-qualification').write_text(str(stage)+'\n')
env=os.environ|{'GOWORK':'off','GOTOOLCHAIN':'local','GOFLAGS':'-p=2 -mod=readonly','GOMAXPROCS':'2','GOPROXY':'off','GOSUMDB':'off','TMPDIR':str(pathlib.Path(tempfile.gettempdir()).resolve())}
def run(args,name,environment=env,timeout=180):
 with (stage/name).open('w') as f:r=subprocess.run(args,cwd=proto,env=environment,stdout=f,stderr=subprocess.STDOUT,timeout=timeout)
 print(name,r.returncode,flush=True)
 if r.returncode:raise RuntimeError(str(stage/name))
run(['gofmt','-w',*map(str,proto.glob('*.go'))],'format.log')
shutil.copytree(proto,stage/'source');shutil.copy2(__file__,stage/'driver.py');shutil.copy2(p/'inherited-inputs.json',stage/'inherited-inputs.json')
manifest={f.name:hashlib.sha256(f.read_bytes()).hexdigest() for f in sorted(proto.iterdir()) if f.is_file()};(stage/'source-hashes.json').write_text(json.dumps(manifest,indent=2)+'\n')
run(['go','test','-race','-count=1','-json','./...'],'native-race.jsonl')
run(['go','test','-c','-o',str(stage/'boundary.test'),'.'],'crosscompile.log',environment=env|{'GOOS':'linux','GOARCH':'arm64','CGO_ENABLED':'0'})
image='golang@sha256:cfa27959837144bf4649802ba38e59daffd0dd2a7994aa83daaf113285b0f551'
cid=subprocess.check_output(['docker','create','--label','ownsolid.lane=iam','--label','ownsolid.item=IAM-106','--name','iam106-boundary-'+uuid.uuid4().hex[:10],'--network','none','--memory','384m','--cpus','2','--pids-limit','64','--security-opt','no-new-privileges',image,'/tmp/boundary.test','-test.v','-test.timeout=45s'],text=True).strip();(stage/'container-id').write_text(cid+'\n')
def terminate(signum,frame):raise KeyboardInterrupt
signal.signal(signal.SIGTERM,terminate)
try:
 run(['docker','cp',str(stage/'boundary.test'),cid+':/tmp/boundary.test'],'copy.log')
 run(['docker','start','-a',cid],'disconnected.log',timeout=60)
 (stage/'container-inspect.json').write_bytes(subprocess.check_output(['docker','inspect',cid]));(stage/'success.json').write_text(json.dumps({'native_race':True,'disconnected_linux_arm64':True,'product_changes':False,'sdk': 'zitadel/oidc/v3 v3.52.0','protocol_server_qualified':False},indent=2)+'\n')
finally:
 subprocess.run(['docker','rm','--force','--volumes',cid],check=True,capture_output=True,timeout=20)
print('IAM106_BOUNDARY_PASSED',stage,flush=True)
