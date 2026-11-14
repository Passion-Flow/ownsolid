import hashlib,io,json,os,pathlib,secrets,subprocess,sys,tarfile,tempfile,time
os.umask(0o077)
repo=pathlib.Path(sys.argv[1]);evidence=pathlib.Path(__file__).parent
run=pathlib.Path(tempfile.mkdtemp(prefix='identity-consumer-',dir=evidence));(evidence/'latest-identity-consumer-path').write_text(str(run)+'\n');steps=[];container=None
def execute(cmd,name,env=None,stdin=None,timeout=180,allow=False):
 with (run/(name+'.log')).open('wb') as out:r=subprocess.run(cmd,cwd=repo,env=env,stdin=stdin,stdout=out,stderr=subprocess.STDOUT,timeout=timeout)
 steps.append({'name':name,'command':cmd,'exit_code':r.returncode});(run/'results.json').write_text(json.dumps(steps,indent=2)+'\n');print(name,r.returncode,flush=True)
 if r.returncode and not allow:raise RuntimeError(name+' failed; original log retained')
 return (run/(name+'.log')).read_text()
paths=['services/control/internal/modules/identity/audit_read.go','services/control/internal/modules/identity/lists.go','services/control/internal/modules/identity/audit_list.go','services/control/internal/modules/identity/audit_list_integration_test.go','services/control/internal/modules/identity/audit_workspace_read_integration_test.go','services/control/internal/modules/identity/audit_workspace_read_repository_integration_test.go','services/control/internal/modules/audit/scoped.go','api/openapi/v1/identity/openapi.json','api/openapi/v1/sso/openapi.json']
(run/'source-hashes.json').write_text(json.dumps({p:hashlib.sha256((repo/p).read_bytes()).hexdigest() for p in paths},indent=2)+'\n')
(run/'input-sha').write_bytes(subprocess.check_output(['git','rev-parse','HEAD'],cwd=repo))
env=os.environ.copy();env.update({'GOOS':'linux','GOARCH':'arm64','CGO_ENABLED':'0','GOFLAGS':'-p=2 -mod=readonly','GOTOOLCHAIN':'local'})
for name,package in [('identity-domain.test','identity'),('identity-http.test','identity/http')]:
 execute(['go','-C','services/control','test','-c','-tags','repository_integration','-o',str(run/name),'./internal/modules/'+package],'compile-'+name,env,timeout=240)
try:
 image='pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47'
 entry='set -eu; chown -R postgres:postgres /tmp/fixture /tmp/tree; chmod 700 /tmp/fixture; exec /usr/local/bin/docker-entrypoint.sh postgres'
 container=execute(['docker','create','--network','none','--memory','768m','--cpus','1','--pids-limit','192','--tmpfs','/var/lib/postgresql:rw,size=512m','-e','POSTGRES_DB=identity_audit_test','-e','POSTGRES_PASSWORD_FILE=/tmp/fixture/password','-e','POSTGRES_INITDB_ARGS=--auth-local=peer --auth-host=scram-sha-256','--entrypoint','/bin/sh',image,'-c',entry],'create').strip()
 assert len(container)==64;(run/'container-id').write_text(container+'\n');secret=secrets.token_urlsafe(40)
 files={'tmp/fixture/password':secret+'\n','tmp/fixture/database.dsn':'postgresql://postgres:'+secret+'@127.0.0.1:5432/identity_audit_test?sslmode=disable\n'}
 archive=run/'inputs.tar'
 with tarfile.open(archive,'w') as tar:
  for directory in ['tmp/fixture','tmp/tree/services/control/internal/modules/identity/http','tmp/tree/api/openapi/v1/identity','tmp/tree/api/openapi/v1/sso']:
   info=tarfile.TarInfo(directory);info.type=tarfile.DIRTYPE;info.mode=0o700;tar.addfile(info)
  for target,value in files.items():
   raw=value.encode();info=tarfile.TarInfo(target);info.mode=0o600;info.size=len(raw);tar.addfile(info,io.BytesIO(raw))
  source_files={run/name:'tmp/fixture/'+name for name in ['identity-domain.test','identity-http.test']}
  for path in paths:
   if path.endswith('.json'):source_files[repo/path]='tmp/tree/'+path
  for source,target in source_files.items():
   info=tarfile.TarInfo(target);info.mode=0o500 if source.name.endswith('.test') else 0o600;info.size=source.stat().st_size
   with source.open('rb') as data:tar.addfile(info,data)
 with archive.open('rb') as data:execute(['docker','cp','-',container+':/'],'copy-inputs',stdin=data)
 execute(['docker','start',container],'start')
 for attempt in range(30):
  if subprocess.run(['docker','exec','--user','postgres',container,'pg_isready','-h','127.0.0.1','-d','identity_audit_test'],capture_output=True).returncode==0:break
  time.sleep(.2)
 else:raise RuntimeError('owned PostgreSQL did not start')
 execute(['docker','exec','--user','postgres',container,'psql','-X','-v','ON_ERROR_STOP=1','-d','identity_audit_test','-c','CREATE EXTENSION vector VERSION "0.8.7"','-c','SHOW server_version'],'extension')
 common=['docker','exec','--user','postgres','-e','PROJECT_ONE_TEST_DSN_FILE=/tmp/fixture/database.dsn','-w','/tmp/tree/services/control/internal/modules/identity/http',container]
 for name,selector,count in [('identity-domain.test','^TestWorkspaceAudit',5),('identity-http.test','^(TestAdminAuditRead.*HTTPS|TestAdminAuditHTTPAssistantAttribution)$',7)]:
  raw=execute(common+['/tmp/fixture/'+name,'-test.v','-test.timeout','180s','-test.run',selector,'-test.count','1'],'native-'+name,timeout=190)
  roots=sum(1 for line in raw.splitlines() if line.startswith('--- PASS: Test'))
  assert roots==count and '--- FAIL:' not in raw and '--- SKIP:' not in raw
 (run/'success.json').write_text(json.dumps({'domain_roots':5,'http_roots':7,'image':image,'network':'none','host_ports':[],'source_hashes':'source-hashes.json','scope':'actual workspace identity read pairing plus installation-wide HTTPS regression; no new workspace HTTP route'},indent=2)+'\n')
 print('AUD108_IDENTITY_CONSUMERS_PASSED',run,flush=True)
finally:
 if container:
  execute(['docker','logs',container],'server-log',allow=True)
  execute(['docker','stop','--timeout','15',container],'stop',allow=True)
  execute(['docker','rm','--force','--volumes',container],'remove')
