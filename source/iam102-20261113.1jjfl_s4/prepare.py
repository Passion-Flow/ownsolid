import datetime,hashlib,json,os,pathlib,secrets,subprocess,sys,uuid
os.umask(0o077)
root=pathlib.Path(sys.argv[1]);fixture=root/'fixture';fixture.mkdir(mode=0o700)
def openssl(args):
 with (root/'certificate-generation.log').open('ab') as out:subprocess.run(['openssl',*args],check=True,stdout=out,stderr=subprocess.STDOUT)
openssl(['req','-x509','-newkey','rsa:2048','-nodes','-keyout',str(fixture/'ca-key.pem'),'-out',str(fixture/'ca.pem'),'-days','2','-subj','/CN=IAM102 private prototype CA'])
openssl(['req','-new','-newkey','rsa:2048','-nodes','-keyout',str(fixture/'server-key.pem'),'-out',str(fixture/'server.csr'),'-subj','/CN=keycloak.iam102.test'])
(fixture/'extensions.cnf').write_text('subjectAltName=DNS:keycloak.iam102.test\nbasicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\n')
openssl(['x509','-req','-in',str(fixture/'server.csr'),'-CA',str(fixture/'ca.pem'),'-CAkey',str(fixture/'ca-key.pem'),'-set_serial','0x'+secrets.token_hex(16),'-out',str(fixture/'server.pem'),'-days','2','-extfile',str(fixture/'extensions.cnf')])
client='ownsolid:iam102+private';password=secrets.token_urlsafe(36);secret=secrets.token_urlsafe(48)+':%+';subject=str(uuid.uuid4())
realm={'realm':'iam102','enabled':True,'sslRequired':'all','registrationAllowed':False,'resetPasswordAllowed':False,'loginWithEmailAllowed':False,'clients':[{'id':str(uuid.uuid4()),'clientId':client,'name':'Ownsolid private protocol prototype','enabled':True,'protocol':'openid-connect','publicClient':False,'secret':secret,'standardFlowEnabled':True,'implicitFlowEnabled':False,'directAccessGrantsEnabled':False,'serviceAccountsEnabled':False,'redirectUris':['https://console.iam102.test/callback'],'attributes':{'pkce.code.challenge.method':'S256'}}], 'users':[{'id':subject,'username':'iam102-user','enabled':True,'email':'iam102@example.test','emailVerified':True,'firstName':'Protocol','lastName':'Probe','requiredActions':[],'credentials':[{'type':'password','value':password,'temporary':False}]}]}
(fixture/'iam102-realm.json').write_text(json.dumps(realm))
(fixture/'probe.json').write_text(json.dumps({'issuer':'https://keycloak.iam102.test:8443/realms/iam102','client_id':client,'client_secret':secret,'username':'iam102-user','password':password,'subject':subject,'callback':'https://console.iam102.test/callback'}))
print('PRIVATE_FIXTURE_FILES_PREPARED')
