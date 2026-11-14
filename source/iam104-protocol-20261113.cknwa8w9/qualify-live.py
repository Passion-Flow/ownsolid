import hashlib,json,os,pathlib,secrets,shutil,subprocess,tarfile,tempfile,time,uuid,signal
os.umask(0o077)
def terminate(signum,frame): raise KeyboardInterrupt
signal.signal(signal.SIGTERM,terminate)
p=pathlib.Path(__file__).parent;proto=p/'prototype';native=pathlib.Path((p/'active-native-path').read_text().strip())
assert (native/'success.json').exists(),'qualified native source required'
expected=json.loads((native/'source-hashes.json').read_text());actual={f.name:hashlib.sha256(f.read_bytes()).hexdigest() for f in proto.iterdir() if f.is_file()};assert expected==actual,'native source changed'
if shutil.disk_usage(p).free<15*1024**3:raise RuntimeError('disk resource refusal')
stage=pathlib.Path(tempfile.mkdtemp(prefix='live-',dir=p));shutil.copy2(__file__,stage/'driver.py');(p/'active-live-path').write_text(str(stage)+'\n');shutil.copytree(proto,stage/'source');(stage/'source-hashes.json').write_text(json.dumps(actual,indent=2)+'\n');(stage/'native-reference.json').write_text(json.dumps({'path':native.name,'hashes':expected},indent=2)+'\n')
env=os.environ|{'GOWORK':'off','GOTOOLCHAIN':'local','GOFLAGS':'-p=2 -mod=readonly','GOMAXPROCS':'2','GOPROXY':'off','GOSUMDB':'off'}
def run(args,name,timeout=180,environ=env,stdin=None):
 with (stage/(name+'.log')).open('wb') as out:r=subprocess.run(args,cwd=proto,env=environ,stdin=stdin,stdout=out,stderr=subprocess.STDOUT,timeout=timeout)
 if r.returncode:raise RuntimeError(name+' failed: '+str(stage/(name+'.log')))
 return (stage/(name+'.log')).read_text()
run(['go','test','-c','-o',str(stage/'probe.test'),'.'],'crosscompile',environ=env|{'GOOS':'linux','GOARCH':'arm64','CGO_ENABLED':'0'})
fixture=stage/'fixture';fixture.mkdir(mode=0o700)
def openssl(args):run(['openssl',*args],'certificate-'+str(len(list(stage.glob('certificate-*.log')))))
openssl(['req','-x509','-newkey','rsa:2048','-nodes','-keyout',str(fixture/'ca-key.pem'),'-out',str(fixture/'ca.pem'),'-days','2','-subj','/CN=IAM104 Private Protocol CA'])
openssl(['req','-new','-newkey','rsa:2048','-nodes','-keyout',str(fixture/'server-key.pem'),'-out',str(fixture/'server.csr'),'-subj','/CN=keycloak.iam104.test'])
(fixture/'extensions.cnf').write_text('subjectAltName=DNS:keycloak.iam104.test\nbasicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\n')
openssl(['x509','-req','-in',str(fixture/'server.csr'),'-CA',str(fixture/'ca.pem'),'-CAkey',str(fixture/'ca-key.pem'),'-set_serial','0x'+secrets.token_hex(16),'-out',str(fixture/'server.pem'),'-days','2','-extfile',str(fixture/'extensions.cnf')])
sp='https://console.iam104.test/metadata';acs='https://console.iam104.test/saml/acs';issuer='https://keycloak.iam104.test:8443/realms/iam104';password=secrets.token_urlsafe(36);(fixture/'user-password').write_text(password)
realm={'realm':'iam104','enabled':True,'sslRequired':'all','registrationAllowed':False,'resetPasswordAllowed':False,'loginWithEmailAllowed':False,'clients':[{'id':str(uuid.uuid4()),'clientId':sp,'name':'Private SAML protocol probe','enabled':True,'protocol':'saml','publicClient':True,'redirectUris':[acs],'attributes':{'saml.assertion.signature':'true','saml.server.signature':'true','saml.client.signature':'false','saml.signature.algorithm':'RSA_SHA256','saml_signature_canonicalization_method':'http://www.w3.org/2001/10/xml-exc-c14n#','saml.force.post.binding':'true','saml_name_id_format':'persistent','saml_force_name_id_format':'true','saml.authnstatement':'true','saml.encrypt':'false','saml.assertion.lifespan':'60'}}],'users':[{'id':str(uuid.uuid4()),'username':'iam104-user','enabled':True,'email':'iam104@example.test','emailVerified':True,'firstName':'Protocol','lastName':'Probe','requiredActions':[],'credentials':[{'type':'password','value':password,'temporary':False}]}]}
(fixture/'realm.json').write_text(json.dumps(realm));(fixture/'probe.json').write_text(json.dumps({'Issuer':issuer,'Endpoint':issuer+'/protocol/saml','Metadata':issuer+'/protocol/saml/descriptor','SP':sp,'ACS':acs,'Username':'iam104-user','PasswordFile':'/tmp/iam104/user-password','CAFile':'/tmp/iam104/ca.pem'}))
image='quay.io/keycloak/keycloak@sha256:923a431c08971d49727a857e64870d8d63b5bf294a730e5dde729b5138c26c9e'
(stage/'image-inspect.json').write_bytes(subprocess.check_output(['docker','image','inspect',image]));cid=None
try:
 cid=run(['docker','create','--name','iam104-protocol-'+uuid.uuid4().hex[:10],'--label','ownsolid.lane=iam','--label','ownsolid.item=IAM-104','--network','none','--memory','1536m','--cpus','2','--pids-limit','256',image,'start','--db=dev-file','--cache=local','--http-enabled=false','--hostname=https://keycloak.iam104.test:8443','--https-certificate-file=/tmp/iam104/server.pem','--https-certificate-key-file=/tmp/iam104/server-key.pem','--import-realm','--log-level=warn'],'create-provider').strip()
 assert len(cid)==64 and all(c in '0123456789abcdef' for c in cid)
 (stage/'container-id').write_text(cid+'\n');archive=stage/'fixture.tar'
 with tarfile.open(archive,'w') as tar:
  for directory in ['tmp/iam104','opt/keycloak/data/import']:
   item=tarfile.TarInfo(directory);item.type=tarfile.DIRTYPE;item.mode=0o700;item.uid=1000;item.gid=0;tar.addfile(item)
  for source,target,mode in [(fixture/f,'tmp/iam104/'+f,0o400) for f in ['ca.pem','server.pem','server-key.pem','probe.json','user-password']]+[(fixture/'realm.json','opt/keycloak/data/import/iam104-realm.json',0o400),(stage/'probe.test','tmp/iam104/probe.test',0o500)]:
   item=tarfile.TarInfo(target);item.uid=1000;item.gid=0;item.mode=mode;item.size=source.stat().st_size
   with source.open('rb') as data:tar.addfile(item,data)
 with archive.open('rb') as data:run(['docker','cp','-a','-',cid+':/'],'copy-fixture',stdin=data)
 run(['docker','start',cid],'start-provider');run(['docker','exec',cid,'java','-version'],'java-version')
 common=['docker','exec','-e','IAM104_FIXTURE_CONFIG=/tmp/iam104/probe.json',cid,'/tmp/iam104/probe.test']
 run(common+['-test.run','^TestLiveKeycloakReady$','-test.v','-test.timeout=100s'],'readiness',timeout=110)
 run(common+['-test.run','^TestLiveKeycloakSignedSAML$','-test.v','-test.timeout=45s'],'protocol',timeout=55)
 (stage/'container-inspect.json').write_bytes(subprocess.check_output(['docker','inspect',cid]));(stage/'success.json').write_text(json.dumps({'image':image,'native_reference':native.name,'live_protocol_roots':1,'readiness_roots':1,'network':'none','host_ports':[],'product_changes':False,'browser_acceptance':False},indent=2)+'\n');print(json.dumps({'phase':'real-saml-protocol-passed','stage':str(stage)}),flush=True)
finally:
 if cid:
  with (stage/'provider.log').open('wb') as out:subprocess.run(['docker','logs',cid],stdout=out,stderr=subprocess.STDOUT,timeout=20)
  subprocess.run(['docker','stop','--time','15',cid],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=25)
  removed=subprocess.run(['docker','rm','--force','--volumes',cid],capture_output=True,timeout=30);(stage/'cleanup.log').write_bytes(removed.stdout+removed.stderr)
  if removed.returncode:raise RuntimeError('owned provider cleanup failed')
