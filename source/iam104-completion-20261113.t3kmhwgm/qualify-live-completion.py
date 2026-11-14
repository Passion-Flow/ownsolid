import hashlib,io,json,os,pathlib,secrets,shutil,signal,subprocess,tarfile,tempfile,time,uuid
os.umask(0o077)
def stop(signum,frame):raise KeyboardInterrupt
signal.signal(signal.SIGTERM,stop)
p=pathlib.Path(__file__).parent;repo=pathlib.Path((p/'repository-path').read_text().strip())
# This entry runs only after the exact SQL candidate has qualified in both topologies.
qualified=pathlib.Path((p/'active-qualification').read_text().strip())
prior=json.loads((qualified/'success.json').read_text())
assert len(prior['results'])==2 and all(x['roots']==34 for x in prior['results'])
expected=json.loads((qualified/'source-hashes.json').read_text())
for name,digest in expected.items():
 f=repo/name[len('owner/'):] if name.startswith('owner/') else p/name
 assert hashlib.sha256(f.read_bytes()).hexdigest()==digest,'QUALIFIED_SOURCE_CHANGED: '+name
if shutil.disk_usage(p).free<15*1024**3:raise RuntimeError('disk resource refusal')
stage=pathlib.Path(tempfile.mkdtemp(prefix='live-completion-',dir=p));(p/'active-live-completion').write_text(str(stage)+'\n')
shutil.copy2(__file__,stage/'driver.py');shutil.copy2(qualified/'source-hashes.json',stage/'qualified-source-hashes.json')
binary=qualified/'saml-claim-candidate.test'
(stage/'source-reference.json').write_text(json.dumps({'sql_qualification':str(qualified),'input_base':(qualified/'input-sha').read_text().strip(),'binary_sha256':hashlib.sha256(binary.read_bytes()).hexdigest()},indent=2)+'\n')
deadline=time.monotonic()+420
pg=None;idp=None
def run(args,name,stdin=None,timeout=30):
 remaining=deadline-time.monotonic()-90
 if remaining<=0:raise RuntimeError('live qualification operation budget exhausted before cleanup reserve')
 with (stage/(name+'.log')).open('xb') as out:r=subprocess.run(args,cwd=repo,stdin=stdin,stdout=out,stderr=subprocess.STDOUT,timeout=min(timeout,remaining))
 print(name,r.returncode,flush=True)
 if r.returncode:raise RuntimeError(name+' failed')
 return (stage/(name+'.log')).read_text()
def archive(files,directories):
 out=io.BytesIO()
 with tarfile.open(fileobj=out,mode='w') as tar:
  for name,uid in directories:
   item=tarfile.TarInfo(name);item.type=tarfile.DIRTYPE;item.mode=0o700;item.uid=uid;tar.addfile(item)
  for path,name,uid,mode in files:
   raw=path.read_bytes();item=tarfile.TarInfo(name);item.size=len(raw);item.uid=uid;item.mode=mode;tar.addfile(item,io.BytesIO(raw))
 out.seek(0);return out
fixture=stage/'fixture';fixture.mkdir(mode=0o700)
try:
 pg_image='pgvector/pgvector@sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47'
 idp_image='quay.io/keycloak/keycloak@sha256:923a431c08971d49727a857e64870d8d63b5bf294a730e5dde729b5138c26c9e'
 for name,image in [('postgres',pg_image),('provider',idp_image)]:
  (stage/(name+'-image.json')).write_bytes(subprocess.check_output(['docker','image','inspect',image],timeout=15))
 for name,args in [('ca',['req','-x509','-newkey','rsa:2048','-nodes','-keyout',str(fixture/'ca-key.pem'),'-out',str(fixture/'ca.pem'),'-days','2','-subj','/CN=IAM104 Private Completion CA']),('server',['req','-new','-newkey','rsa:2048','-nodes','-keyout',str(fixture/'server-key.pem'),'-out',str(fixture/'server.csr'),'-subj','/CN=keycloak.iam104.test'])]:run(['openssl',*args],'certificate-'+name)
 (fixture/'extensions.cnf').write_text('subjectAltName=DNS:keycloak.iam104.test\nbasicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\n')
 run(['openssl','x509','-req','-in',str(fixture/'server.csr'),'-CA',str(fixture/'ca.pem'),'-CAkey',str(fixture/'ca-key.pem'),'-set_serial','0x'+secrets.token_hex(16),'-out',str(fixture/'server.pem'),'-days','2','-extfile',str(fixture/'extensions.cnf')],'certificate-sign')
 connector=str(uuid.uuid4());portal='https://portal.iam104.test';sp=portal+'/sso/saml/metadata';acs=portal+'/api/v1/sso/saml/connectors/'+connector+'/acs';issuer='https://keycloak.iam104.test:8443/realms/iam104'
 password=secrets.token_urlsafe(40);database_password=secrets.token_urlsafe(40)
 (fixture/'user-password').write_text(password);(fixture/'database-password').write_text(database_password+'\n')
 (fixture/'database.dsn').write_text('postgresql://postgres:'+database_password+'@127.0.0.1:5432/iam_saml_completion_test?sslmode=disable\n')
 realm={'realm':'iam104','enabled':True,'sslRequired':'all','registrationAllowed':False,'resetPasswordAllowed':False,'loginWithEmailAllowed':False,'clients':[{'id':str(uuid.uuid4()),'clientId':sp,'name':'Actual private completion source','enabled':True,'protocol':'saml','publicClient':True,'redirectUris':[acs],'attributes':{'saml.assertion.signature':'true','saml.server.signature':'true','saml.client.signature':'false','saml.signature.algorithm':'RSA_SHA256','saml_signature_canonicalization_method':'http://www.w3.org/2001/10/xml-exc-c14n#','saml.force.post.binding':'true','saml_name_id_format':'persistent','saml_force_name_id_format':'true','saml.authnstatement':'true','saml.encrypt':'false','saml.assertion.lifespan':'60'}}],'users':[{'id':str(uuid.uuid4()),'username':'iam104-user','enabled':True,'email':'iam104@example.test','emailVerified':True,'firstName':'Completion','lastName':'Probe','requiredActions':[],'credentials':[{'type':'password','value':password,'temporary':False}]}]}
 (fixture/'realm.json').write_text(json.dumps(realm))
 (fixture/'probe.json').write_text(json.dumps({'Issuer':issuer,'Endpoint':issuer+'/protocol/saml','Metadata':issuer+'/protocol/saml/descriptor','SP':sp,'ACS':acs,'Username':'iam104-user','PasswordFile':'/tmp/fixture/user-password','CAFile':'/tmp/fixture/ca.pem','ConnectorID':connector,'PortalOrigin':portal}))
 (fixture/'setup.json').write_text(json.dumps({'format_version':1,'setup_seconds':120,'mysql':{'lock_wait_seconds':1,'step_seconds':5,'total_seconds':20,'cleanup_seconds':2}}))
 entry='set -eu; chown -R postgres:postgres /tmp/fixture; chmod 700 /tmp/fixture; exec /usr/local/bin/docker-entrypoint.sh postgres -c max_wal_size=64MB -c min_wal_size=32MB -c checkpoint_timeout=30s -c checkpoint_completion_target=0.1'
 pg=run(['docker','create','--label','ownsolid.lane=iam','--label','ownsolid.item=IAM-104','--network','none','--memory','768m','--cpus','1','--pids-limit','192','--tmpfs','/var/lib/postgresql:rw,size=512m','-e','POSTGRES_DB=iam_saml_completion_test','-e','POSTGRES_PASSWORD_FILE=/tmp/fixture/database-password','-e','POSTGRES_INITDB_ARGS=--auth-local=peer --auth-host=scram-sha-256','--entrypoint','/bin/sh',pg_image,'-c',entry],'create-postgres').strip();assert len(pg)==64
 (stage/'postgres-id').write_text(pg+'\n')
 files=[(fixture/n,'tmp/fixture/'+n,999,0o400) for n in ['database-password','database.dsn','probe.json','ca.pem','user-password','setup.json']]+[(binary,'tmp/fixture/probe.test',999,0o500),(p/'candidate.sql','tmp/fixture/candidate.sql',999,0o400)]
 run(['docker','cp','-a','-',pg+':/'],'copy-postgres-fixture',stdin=archive(files,[('tmp/fixture',999)]))
 run(['docker','start',pg],'start-postgres')
 for attempt in range(40):
  if subprocess.run(['docker','exec','--user','postgres',pg,'pg_isready','-h','127.0.0.1','-d','iam_saml_completion_test'],capture_output=True,timeout=5).returncode==0:break
  time.sleep(.1)
 else:raise RuntimeError('owned PostgreSQL readiness refused')
 run(['docker','exec','--user','postgres',pg,'psql','-X','-v','ON_ERROR_STOP=1','-d','iam_saml_completion_test','-c','CREATE EXTENSION vector VERSION "0.8.7"','-c','SHOW server_version'],'postgres-version')
 idp=run(['docker','create','--label','ownsolid.lane=iam','--label','ownsolid.item=IAM-104','--network','container:'+pg,'--memory','1536m','--cpus','1','--pids-limit','256',idp_image,'start','--db=dev-file','--cache=local','--http-enabled=false','--hostname=https://keycloak.iam104.test:8443','--https-certificate-file=/tmp/iam104/server.pem','--https-certificate-key-file=/tmp/iam104/server-key.pem','--import-realm','--log-level=warn'],'create-provider').strip();assert len(idp)==64
 (stage/'provider-id').write_text(idp+'\n')
 files=[(fixture/n,'tmp/iam104/'+n,1000,0o400) for n in ['server.pem','server-key.pem']]+[(fixture/'realm.json','opt/keycloak/data/import/iam104-realm.json',1000,0o400)]
 run(['docker','cp','-a','-',idp+':/'],'copy-provider-fixture',stdin=archive(files,[('tmp/iam104',1000),('opt/keycloak/data/import',1000)]))
 run(['docker','start',idp],'start-provider')
 common=['docker','exec','--user','postgres','-e','GOMAXPROCS=2','-e','GOMEMLIMIT=512MiB','-e','IAM104_FIXTURE_CONFIG=/tmp/fixture/probe.json','-e','PROJECT_ONE_REPOSITORY_ENGINE=postgresql','-e','PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE=/tmp/fixture/database.dsn','-e','PROJECT_ONE_REPOSITORY_POSTGRES_SCHEMA=m20_repository_'+uuid.uuid4().hex,'-e','PROJECT_ONE_TEST_REPOSITORY_SETUP_FILE=/tmp/fixture/setup.json','-e','IAM_SAML_CLAIM_SQL_FILE=/tmp/fixture/candidate.sql',pg,'/tmp/fixture/probe.test']
 run(common+['-test.run','^TestSAMLCompletionLiveProviderReady$','-test.v','-test.count=1','-test.timeout=100s'],'provider-readiness',timeout=110)
 raw=run(common+['-test.run','^TestSAMLCompletionLiveProvider$','-test.v','-test.count=1','-test.timeout=75s'],'provider-completion',timeout=85)
 assert raw.count('--- PASS: TestSAMLCompletionLiveProvider ')==1 and '--- FAIL:' not in raw and '--- SKIP:' not in raw
 for name,cid in [('postgres',pg),('provider',idp)]:
  info=json.loads(subprocess.check_output(['docker','inspect',cid],timeout=15))[0];(stage/(name+'-inspect.json')).write_text(json.dumps(info,indent=2)+'\n')
  assert not info['HostConfig']['PortBindings']
  assert info['HostConfig']['NetworkMode']==('none' if name=='postgres' else 'container:'+pg)
finally:
 cleanup_errors=[]
 for name,cid in [('provider',idp),('postgres',pg)]:
  if not cid:continue
  try:
   with (stage/(name+'.log')).open('xb') as out:subprocess.run(['docker','logs',cid],stdout=out,stderr=subprocess.STDOUT,timeout=10)
  except Exception as e:cleanup_errors.append(name+' log retention '+type(e).__name__)
  try:subprocess.run(['docker','stop','--time','10',cid],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=15)
  except Exception as e:cleanup_errors.append(name+' stop '+type(e).__name__)
  try:
   result=subprocess.run(['docker','rm','--force','--volumes',cid],capture_output=True,timeout=15);(stage/(name+'-cleanup.log')).write_bytes(result.stdout+result.stderr)
   if result.returncode:cleanup_errors.append(name+' removal refused')
   remains=subprocess.run(['docker','inspect',cid],capture_output=True,timeout=5)
   if remains.returncode==0:cleanup_errors.append(name+' remained after removal')
  except Exception as e:cleanup_errors.append(name+' removal '+type(e).__name__)
 (stage/'cleanup-result.json').write_text(json.dumps({'errors':cleanup_errors},indent=2)+'\n')
 if cleanup_errors:raise RuntimeError('owned live fixture cleanup did not close: '+','.join(cleanup_errors))
(stage/'success.json').write_text(json.dumps({'sql_qualification':str(qualified),'actual_provider_completion_roots':1,'readiness_roots':1,'network':'none shared namespace','browser_acceptance':False,'http_acs':False,'formal_schema_changed':False,'product_routes_installed':False,'cleanup_confirmed':True},indent=2)+'\n')
print('SAML_REAL_PROVIDER_DOMAIN_COMPLETION_PASSED',stage,flush=True)
