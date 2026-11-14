import hashlib,io,json,os,pathlib,subprocess,sys,tarfile,tempfile,time
os.umask(0o077)
root=pathlib.Path(sys.argv[1]);proto=root/'prototype';run=pathlib.Path(tempfile.mkdtemp(prefix='qualification-',dir=root));(root/'latest-qualification-path').write_text(str(run)+'\n');receipts=[]
env=os.environ.copy();env.update({'GOWORK':'off','GOTOOLCHAIN':'local','GOFLAGS':'-p=2 -mod=readonly','GOPROXY':'off'})
container=None
image='quay.io/keycloak/keycloak@sha256:923a431c08971d49727a857e64870d8d63b5bf294a730e5dde729b5138c26c9e'
def execute(cmd,name,timeout=180,environ=None,stdin=None):
 started=time.time()
 with (run/(name+'.log')).open('wb') as out:r=subprocess.run(cmd,cwd=proto,env=environ or env,stdin=stdin,stdout=out,stderr=subprocess.STDOUT,timeout=timeout)
 receipts.append({'name':name,'command':cmd,'exit_code':r.returncode,'seconds':round(time.time()-started,3)});(run/'results.json').write_text(json.dumps(receipts,indent=2)+'\n');print(name,r.returncode,flush=True)
 if r.returncode:raise RuntimeError(name+' failed; original log retained')
 return (run/(name+'.log')).read_text()
(run/'disk.log').write_bytes(subprocess.check_output(['df','-h','/']))
execute(['gofmt','-w','adapter.go','adapter_test.go','live_test.go','tls_test.go'],'format')
tidy=env.copy();tidy['GOFLAGS']='-p=2';execute(['go','mod','tidy'],'module-resolution',environ=tidy)
(run/'source-hashes.json').write_text(json.dumps({p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in proto.iterdir() if p.is_file()},indent=2)+'\n')
execute(['go','test','-race','-count=1','-v','./...'],'adversarial-native',timeout=180)
if not (root/'fixture').exists():execute(['python3',str(root/'prepare.py'),str(root)],'prepare-private-files')
execute(['docker','pull',image],'provider-image-pull',timeout=300)
(run/'image-inspect.json').write_bytes(subprocess.check_output(['docker','image','inspect',image]))
linux=env.copy();linux.update({'GOOS':'linux','GOARCH':'arm64','CGO_ENABLED':'0'})
execute(['go','test','-c','-o',str(run/'probe.test'),'./'],'compile-private-probe',environ=linux)
(run/'probe-sha256').write_text(hashlib.sha256((run/'probe.test').read_bytes()).hexdigest()+'\n')
try:
 name='iam102-prototype-'+run.name.rsplit('-',1)[-1]
 created=execute(['docker','create','--name',name,'--label','ownsolid.lane=iam','--label','ownsolid.item=IAM-102','--network','none','--memory','1536m','--cpus','2','--pids-limit','256',image,'start','--db=dev-file','--cache=local','--http-enabled=false','--hostname=https://keycloak.iam102.test:8443','--https-certificate-file=/tmp/iam102/server.pem','--https-certificate-key-file=/tmp/iam102/server-key.pem','--import-realm','--log-level=warn'],'create-private-provider')
 container=created.strip()
 if len(container)!=64 or any(c not in '0123456789abcdef' for c in container):raise RuntimeError('owned container ID missing')
 (run/'container-id').write_text(container+'\n')
 execute(['docker','cp',container+':/usr/share/licenses',str(run/'image-licenses')],'collect-image-licenses')
 archive=run/'fixture.tar'
 with tarfile.open(archive,'w') as tar:
  for directory in ['tmp/iam102','opt/keycloak/data/import']:
   info=tarfile.TarInfo(directory);info.type=tarfile.DIRTYPE;info.mode=0o700;info.uid=1000;info.gid=0;tar.addfile(info)
  for source,target,mode in [(root/'fixture'/filename,'tmp/iam102/'+filename,0o400) for filename in ['ca.pem','server.pem','server-key.pem','probe.json']]+[(root/'fixture/iam102-realm.json','opt/keycloak/data/import/iam102-realm.json',0o400),(run/'probe.test','tmp/iam102/probe.test',0o500)]:
   info=tarfile.TarInfo(target);info.uid=1000;info.gid=0;info.mode=mode;info.size=source.stat().st_size
   with source.open('rb') as data:tar.addfile(info,data)
 with archive.open('rb') as data:execute(['docker','cp','-a','-',container+':/'],'copy-private-fixture',stdin=data)
 execute(['docker','start',container],'start-private-provider')
 common=['docker','exec','-e','IAM102_FIXTURE_CONFIG=/tmp/iam102/probe.json','-e','IAM102_FIXTURE_CA=/tmp/iam102/ca.pem',container,'/tmp/iam102/probe.test']
 execute(['docker','exec',container,'java','-version'],'provider-java-version')
 execute(common+['-test.run','^TestLiveKeycloakReady$','-test.timeout','100s','-test.v'],'actual-provider-ready',timeout=110)
 execute(common+['-test.run','^TestLiveKeycloak(AuthorizationCode|ExchangeRefusals)$','-test.timeout','75s','-test.v'],'actual-provider-protocol',timeout=85)
 (run/'success.json').write_text(json.dumps({'image':image,'provider_version':'26.8.2','network':'none','host_ports':[],'actual_provider_tests':2,'adversarial_roots':8,'product_code_changed':False,'installed_amd64_acceptance':False},indent=2)+'\n')
finally:
 if container:
  with (run/'provider.log').open('wb') as out:subprocess.run(['docker','logs',container],stdout=out,stderr=subprocess.STDOUT,timeout=20)
  stop=subprocess.run(['docker','stop','--time','20',container],capture_output=True,timeout=30);(run/'stop-provider.log').write_bytes(stop.stdout+stop.stderr)
  removed=subprocess.run(['docker','rm','--force','--volumes',container],capture_output=True,timeout=30);(run/'remove-provider.log').write_bytes(removed.stdout+removed.stderr)
  if removed.returncode:raise RuntimeError('owned provider cleanup failed')
print('IAM102_PRIVATE_PROTOTYPE_PASSED',run,flush=True)
