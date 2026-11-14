import hashlib,io,json,os,pathlib,secrets,shutil,signal,subprocess,tarfile,tempfile,time,uuid
os.umask(0o077)
def stop(signum,frame):raise KeyboardInterrupt
signal.signal(signal.SIGTERM,stop)
p=pathlib.Path(__file__).parent;repo=pathlib.Path("/Users/passion./Documents/Passion's WorkSpace/Development/lane-iam")
if shutil.disk_usage(p).free<15*1024**3:raise RuntimeError('disk resource refusal')
base=json.loads((p/'base-source.json').read_text());notes=repo/'services/control/internal/modules/workspaces/notes.go';assert hashlib.sha256(notes.read_bytes()).hexdigest()==base['notes_go_sha256'],'REBASE_SOURCE_REQUIRED'
stage=pathlib.Path(tempfile.mkdtemp(prefix='qualification-',dir=p));(p/'active-qualification').write_text(str(stage)+'\n');shutil.copy2(__file__,stage/'driver.py')
env=os.environ|{'GOFLAGS':'-p=2 -mod=readonly','GOTOOLCHAIN':'local','GOMAXPROCS':'2','GOPROXY':'off','GOSUMDB':'off'}
def run(args,name,environment=env,stdin=None,timeout=240):
 with (stage/(name+'.log')).open('wb') as out:r=subprocess.run(args,cwd=repo,env=environment,stdin=stdin,stdout=out,stderr=subprocess.STDOUT,timeout=timeout)
 print(name,r.returncode,flush=True)
 if r.returncode:raise RuntimeError(name+' failed: '+str(stage/(name+'.log')))
 return (stage/(name+'.log')).read_text()
run(['gofmt','-w',str(p/'notes.go'),str(p/'scoped_role_candidate.go'),str(p/'scoped_role_candidate_test.go')],'format')
source=stage/'source';source.mkdir()
for f in ['notes.go','scoped_role_candidate.go','scoped_role_candidate_test.go','candidate.sql','overlay.json','base-source.json']:shutil.copy2(p/f,source/f)
hashes={f.name:hashlib.sha256(f.read_bytes()).hexdigest() for f in source.iterdir()};(stage/'source-hashes.json').write_text(json.dumps(hashes,indent=2)+'\n');(stage/'input-sha').write_bytes(subprocess.check_output(['git','rev-parse','HEAD'],cwd=repo));run(['go','version'],'go-version')
common=['go','-C','services/control','test','-tags','repository_integration','-overlay='+str(p/'overlay.json')]
run(common+['-c','-o',str(stage/'workspace-candidate.test'),'./internal/modules/workspaces'],'crosscompile',environment=env|{'GOOS':'linux','GOARCH':'arm64','CGO_ENABLED':'0'})
image='pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47';(stage/'image-inspect.json').write_bytes(subprocess.check_output(['docker','image','inspect',image]));results=[]
for topology in ['native-race-loopback','disconnected-linux-arm64']:
 folder=stage/topology;folder.mkdir();cid=None;network=None
 try:
  flags=['--network','none']
  if topology=='native-race-loopback':
   network=run(['docker','network','create','--label','ownsolid.lane=iam','iam107-role-'+uuid.uuid4().hex[:10]],topology+'-network').strip();flags=['--network',network,'-p','127.0.0.1::5432']
  entry='set -eu; chown -R postgres:postgres /tmp/fixture; chmod 700 /tmp/fixture; exec /usr/local/bin/docker-entrypoint.sh postgres'
  cid=run(['docker','create','--label','ownsolid.lane=iam','--label','ownsolid.item=IAM-107',*flags,'--memory','768m','--cpus','1','--pids-limit','192','--tmpfs','/var/lib/postgresql:rw,size=512m','-e','POSTGRES_DB=iam_role_scope_test','-e','POSTGRES_PASSWORD_FILE=/tmp/fixture/password','-e','POSTGRES_INITDB_ARGS=--auth-local=peer --auth-host=scram-sha-256','--entrypoint','/bin/sh',image,'-c',entry],topology+'-create').strip()
  assert len(cid)==64;(folder/'container-id').write_text(cid+'\n');password=secrets.token_urlsafe(40);schema='m20_repository_'+uuid.uuid4().hex
  setup=json.dumps({'format_version':1,'setup_seconds':120,'mysql':{'lock_wait_seconds':1,'step_seconds':5,'total_seconds':20,'cleanup_seconds':2}})
  payload={'tmp/fixture/password':password+'\n','tmp/fixture/database.dsn':'postgresql://postgres:'+password+'@127.0.0.1:5432/iam_role_scope_test?sslmode=disable\n','tmp/fixture/setup.json':setup}
  archive=folder/'inputs.tar'
  with tarfile.open(archive,'w') as tar:
   d=tarfile.TarInfo('tmp/fixture');d.type=tarfile.DIRTYPE;d.mode=0o700;tar.addfile(d)
   for target,value in payload.items():
    b=value.encode();item=tarfile.TarInfo(target);item.mode=0o600;item.size=len(b);tar.addfile(item,io.BytesIO(b))
   for file,target,mode in [(stage/'workspace-candidate.test','tmp/fixture/workspace-candidate.test',0o500),(p/'candidate.sql','tmp/fixture/candidate.sql',0o600)]:
    item=tarfile.TarInfo(target);item.mode=mode;item.size=file.stat().st_size
    with file.open('rb') as f:tar.addfile(item,f)
  with archive.open('rb') as f:run(['docker','cp','-',cid+':/'],topology+'-copy',stdin=f)
  run(['docker','start',cid],topology+'-start')
  for attempt in range(40):
   ready=subprocess.run(['docker','exec','--user','postgres',cid,'pg_isready','-h','127.0.0.1','-d','iam_role_scope_test'],capture_output=True)
   if ready.returncode==0:break
   time.sleep(.1)
  else:raise RuntimeError('owned PostgreSQL not ready')
  run(['docker','exec','--user','postgres',cid,'psql','-X','-v','ON_ERROR_STOP=1','-d','iam_role_scope_test','-c','CREATE EXTENSION vector VERSION "0.8.7"','-c','SHOW server_version'],topology+'-database-version')
  if topology=='native-race-loopback':
   info=json.loads(subprocess.check_output(['docker','inspect',cid],text=True))[0];(folder/'native-port-inspect.json').write_text(json.dumps(info,indent=2)+'\n');ports=info['NetworkSettings']['Ports']['5432/tcp'];assert len(ports)==1 and ports[0]['HostIp']=='127.0.0.1';port=int(ports[0]['HostPort']);assert port>1024
   dsn=folder/'database.dsn';dsn.write_text('postgresql://postgres:'+password+'@127.0.0.1:'+str(port)+'/iam_role_scope_test?sslmode=disable\n');dsn.chmod(0o600);(folder/'setup.json').write_text(setup)
   native=env|{'PROJECT_ONE_REPOSITORY_ENGINE':'postgresql','PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE':str(dsn),'PROJECT_ONE_REPOSITORY_POSTGRES_SCHEMA':schema,'PROJECT_ONE_TEST_REPOSITORY_SETUP_FILE':str(folder/'setup.json'),'IAM_ROLE_CANDIDATE_SQL_FILE':str(p/'candidate.sql')}
   raw=run(common+['-race','-count=1','-json','./internal/modules/workspaces','-run','^TestScopedRoleCandidate'],topology+'-tests',environment=native,timeout=300)
   rows=[json.loads(x) for x in raw.splitlines()];roots=[x['Test'] for x in rows if x.get('Action')=='pass' and x.get('Test') and '/' not in x['Test']];assert len(roots)==4 and not any(x.get('Action') in ['fail','skip'] for x in rows)
  else:
   raw=run(['docker','exec','--user','postgres','-e','PROJECT_ONE_REPOSITORY_ENGINE=postgresql','-e','PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE=/tmp/fixture/database.dsn','-e','PROJECT_ONE_REPOSITORY_POSTGRES_SCHEMA='+schema,'-e','PROJECT_ONE_TEST_REPOSITORY_SETUP_FILE=/tmp/fixture/setup.json','-e','IAM_ROLE_CANDIDATE_SQL_FILE=/tmp/fixture/candidate.sql',cid,'/tmp/fixture/workspace-candidate.test','-test.v','-test.run','^TestScopedRoleCandidate','-test.count=1','-test.timeout=240s'],topology+'-tests',timeout=250)
   roots=[line for line in raw.splitlines() if line.startswith('--- PASS: TestScopedRoleCandidate')];assert len(roots)==4 and '--- FAIL:' not in raw and '--- SKIP:' not in raw
  (folder/'container-inspect.json').write_bytes(subprocess.check_output(['docker','inspect',cid]));results.append({'topology':topology,'roots':len(roots),'race':topology=='native-race-loopback','client_egress_isolated':topology=='disconnected-linux-arm64'})
 finally:
  if cid:
   with (folder/'postgres.log').open('wb') as out:subprocess.run(['docker','logs',cid],stdout=out,stderr=subprocess.STDOUT,timeout=20)
   subprocess.run(['docker','stop','--time','15',cid],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=25)
   r=subprocess.run(['docker','rm','--force','--volumes',cid],capture_output=True,timeout=30);(folder/'cleanup.log').write_bytes(r.stdout+r.stderr)
   if r.returncode:raise RuntimeError('owned database cleanup failed')
  if network:subprocess.run(['docker','network','rm',network],check=True,capture_output=True,timeout=20)
(stage/'success.json').write_text(json.dumps({'results':results,'source_hashes':'source-hashes.json','product_source_changed':False,'formal_schema_changed':False,'role_routes_installed':False},indent=2)+'\n');print('SCOPED_ROLE_CANDIDATE_PASSED',stage,flush=True)
