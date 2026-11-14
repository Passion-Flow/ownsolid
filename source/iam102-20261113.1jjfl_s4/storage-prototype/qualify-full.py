import hashlib,io,json,os,pathlib,secrets,subprocess,sys,tarfile,tempfile,time,uuid
os.umask(0o077)
repo=pathlib.Path(sys.argv[1]);root=pathlib.Path(__file__).parent;run=pathlib.Path(tempfile.mkdtemp(prefix='full-schema-',dir=root));(root/'latest-full-schema-path').write_text(str(run)+'\n');container=None;steps=[]
def execute(cmd,name,env=None,stdin=None,timeout=180,allow=False):
 with (run/(name+'.log')).open('wb') as out:r=subprocess.run(cmd,cwd=repo,env=env,stdin=stdin,stdout=out,stderr=subprocess.STDOUT,timeout=timeout)
 steps.append({'name':name,'command':cmd,'exit_code':r.returncode});(run/'results.json').write_text(json.dumps(steps,indent=2)+'\n');print(name,r.returncode,flush=True)
 if r.returncode and not allow:raise RuntimeError(name+' failed; original log retained')
 return (run/(name+'.log')).read_text()
paths=['services/control/internal/modules/sso/oidc_profile_schema_repository_integration_test.go','services/control/internal/modules/sso/testdata/oidc_profile.up.sql','services/control/internal/modules/sso/testdata/oidc_profile.down.sql','services/control/internal/modules/sso/README.md']
(run/'source-hashes.json').write_text(json.dumps({p:hashlib.sha256((repo/p).read_bytes()).hexdigest() for p in paths},indent=2)+'\n')
(run/'input-sha').write_bytes(subprocess.check_output(['git','rev-parse','HEAD'],cwd=repo))
env=os.environ.copy();env.update({'GOOS':'linux','GOARCH':'arm64','CGO_ENABLED':'0','GOFLAGS':'-p=2 -mod=readonly','GOTOOLCHAIN':'local'})
execute(['go','-C','services/control','test','-c','-tags','repository_integration','-o',str(run/'sso-repository.test'),'./internal/modules/sso'],'compile',env,timeout=240)
try:
 image='pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47'
 entry='set -eu; chown -R postgres:postgres /tmp/fixture /tmp/tree; chmod 700 /tmp/fixture; exec /usr/local/bin/docker-entrypoint.sh postgres'
 container=execute(['docker','create','--network','none','--memory','768m','--cpus','1','--pids-limit','192','--tmpfs','/var/lib/postgresql:rw,size=512m','-e','POSTGRES_DB=oidc_profile_test','-e','POSTGRES_PASSWORD_FILE=/tmp/fixture/password','-e','POSTGRES_INITDB_ARGS=--auth-local=peer --auth-host=scram-sha-256','--entrypoint','/bin/sh',image,'-c',entry],'create').strip()
 assert len(container)==64;(run/'container-id').write_text(container+'\n')
 secret=secrets.token_urlsafe(40)
 files={'tmp/fixture/password':secret+'\n','tmp/fixture/database.dsn':'postgresql://postgres:'+secret+'@127.0.0.1:5432/oidc_profile_test?sslmode=disable\n','tmp/fixture/setup.json':json.dumps({'format_version':1,'setup_seconds':120,'mysql':{'lock_wait_seconds':1,'step_seconds':5,'total_seconds':20,'cleanup_seconds':2}})}
 archive=run/'inputs.tar'
 with tarfile.open(archive,'w') as tar:
  for directory in ['tmp/fixture','tmp/tree/services/control/internal/modules/sso/testdata']:
   info=tarfile.TarInfo(directory);info.type=tarfile.DIRTYPE;info.mode=0o700;tar.addfile(info)
  for target,text in files.items():
   raw=text.encode();info=tarfile.TarInfo(target);info.mode=0o600;info.size=len(raw);tar.addfile(info,io.BytesIO(raw))
  source_files={run/'sso-repository.test':'tmp/fixture/sso-repository.test'}
  for path in paths:
   if path.endswith('.sql'):source_files[repo/path]='tmp/tree/'+path
  for source,target in source_files.items():
   info=tarfile.TarInfo(target);info.mode=0o500 if source.name.endswith('.test') else 0o600;info.size=source.stat().st_size
   with source.open('rb') as data:tar.addfile(info,data)
 with archive.open('rb') as data:execute(['docker','cp','-',container+':/'],'copy-inputs',stdin=data)
 execute(['docker','start',container],'start')
 for attempt in range(30):
  result=subprocess.run(['docker','exec','--user','postgres',container,'pg_isready','-h','127.0.0.1','-d','oidc_profile_test'],capture_output=True)
  if result.returncode==0:break
  time.sleep(.2)
 else:raise RuntimeError('owned PostgreSQL did not start')
 execute(['docker','exec','--user','postgres',container,'psql','-X','-v','ON_ERROR_STOP=1','-d','oidc_profile_test','-c','CREATE EXTENSION vector VERSION "0.8.7"','-c','SHOW server_version'],'extension')
 schema='m20_repository_'+uuid.uuid4().hex
 execute(['docker','exec','--user','postgres','-e','PROJECT_ONE_REPOSITORY_ENGINE=postgresql','-e','PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE=/tmp/fixture/database.dsn','-e','PROJECT_ONE_REPOSITORY_POSTGRES_SCHEMA='+schema,'-e','PROJECT_ONE_TEST_REPOSITORY_SETUP_FILE=/tmp/fixture/setup.json','-w','/tmp/tree/services/control/internal/modules/sso',container,'/tmp/fixture/sso-repository.test','-test.v','-test.timeout','180s','-test.run','^TestPostgresOIDCProfile','-test.count','1'],'native-full-schema',timeout=190)
 raw=(run/'native-full-schema.log').read_text();assert sum(line.startswith('--- PASS: TestPostgresOIDCProfile') for line in raw.splitlines())==3 and '--- FAIL:' not in raw and '--- SKIP:' not in raw
 (run/'success.json').write_text(json.dumps({'image':image,'actual_roots':3,'source_hashes':'source-hashes.json','scope':'complete current repository fixture; legacy service continuity, new profile constraints and actual nonce wait/commit race','network':'none','host_ports':[],'formal_migration_changed':False,'product_provider_enabled':False},indent=2)+'\n')
 print('OIDC_FULL_SCHEMA_CANDIDATE_PASSED',run,flush=True)
finally:
 if container:
  execute(['docker','logs',container],'server-log',allow=True)
  execute(['docker','stop','--timeout','15',container],'stop',allow=True)
  execute(['docker','rm','--force','--volumes',container],'remove')
