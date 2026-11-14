import signal
import base64,hashlib,json,os,pathlib,secrets,shutil,subprocess,tempfile,time,uuid
def terminate(signum, frame): raise SystemExit(128+signum)
signal.signal(signal.SIGTERM,terminate)
p=pathlib.Path(__file__).parent
for inherited in json.loads((p/'inherited-inputs.json').read_text()):
 if hashlib.sha256(pathlib.Path(inherited['path']).read_bytes()).hexdigest()!=inherited['sha256']:raise RuntimeError('inherited evidence source changed')
parent=pathlib.Path(json.loads((p/'parent-evidence.json').read_text())['parent'])
for name in ['go.mod','go.sum']:
 if (p/'prototype'/name).read_bytes()!=(parent/'prototype'/name).read_bytes():raise RuntimeError('unreviewed dependency graph change')
stage=pathlib.Path(tempfile.mkdtemp(prefix='qualification-',dir=p));os.chmod(stage,0o700)
(p/'active-qualification').write_text(str(stage)+'\n')
proto=p/'prototype';env=os.environ.copy();env.update({'GOWORK':'off','GOTOOLCHAIN':'local','GOFLAGS':'-p=2 -mod=readonly','GOMAXPROCS':'2','GOPROXY':'off','GOSUMDB':'off'})
def run(args,name,cwd=None,env=env,timeout=180):
 with (stage/name).open('w') as f:
  r=subprocess.run(args,cwd=cwd,env=env,stdout=f,stderr=subprocess.STDOUT,timeout=timeout)
 if r.returncode: raise RuntimeError(name+' failed, see '+str(stage/name))
 return r
if shutil.disk_usage(p).free<15*1024**3:raise RuntimeError('disk resource refusal')
run(['gofmt','-w',*map(str,proto.glob('*.go'))],'gofmt.log')
manifest=[{'path':f.name,'sha256':hashlib.sha256(f.read_bytes()).hexdigest()} for f in sorted(proto.iterdir()) if f.is_file()]
(stage/'source-manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
shutil.copytree(proto,stage/'source')
for name in ['qualify-protocol.py','parent-evidence.json','inherited-inputs.json']:
 shutil.copy2(p/name,stage/name)
run(['go','test','-race','-count=1','-json','./...'],'native-race.jsonl',cwd=proto)
print(json.dumps({'phase':'native-race-passed','stage':str(stage)}),flush=True)
cross=env|{'GOOS':'linux','GOARCH':'arm64','CGO_ENABLED':'0'}
run(['go','test','-c','-o',str(stage/'directory.test'),'.'],'crosscompile.log',cwd=proto,env=cross)
fixture=stage/'fixture';fixture.mkdir(mode=0o700)
def secretfile(name):
 value=secrets.token_urlsafe(32);f=fixture/name;f.write_text(value);f.chmod(0o600);return value
def ssha(value):
 salt=secrets.token_bytes(16);return '{SSHA}'+base64.b64encode(hashlib.sha1(value.encode()+salt).digest()+salt).decode()
reader=secretfile('reader-password');user=secretfile('user-password');admin=secretfile('admin-password')
base='dc=fixture,dc=test';readerDN='uid=reader,ou=system,'+base;adminDN='cn=fixtureadmin,'+base
config={'Config':{'Origin':'ldaps://ldap.iam103.test:1636','DialAddress':'127.0.0.1:1636','CAFile':'/tmp/fixture/ca.pem','SearchDN':readerDN,'SearchPasswordFile':'/tmp/fixture/reader-password','UserBase':'ou=people,'+base,'GroupBase':'ou=groups,'+base,'Timeout':5000000000,'FrameBytes':65536,'TotalBytes':1048576,'Depth':16,'Nodes':1024,'PageSize':2,'MaxPages':10,'MaxEntries':20,'CookieBytes':256},'UserPasswordFile':'/tmp/fixture/user-password','AdminDN':adminDN,'AdminPasswordFile':'/tmp/fixture/admin-password'}
(fixture/'config.json').write_text(json.dumps(config,indent=2)+'\n')
(fixture/'ca.cnf').write_text('''[req]
distinguished_name=dn
prompt=no
x509_extensions=ca
[dn]
CN=IAM103 Private Test CA
[ca]
basicConstraints=critical,CA:TRUE
keyUsage=critical,keyCertSign,cRLSign
subjectKeyIdentifier=hash
''')
(fixture/'server.cnf').write_text('''[req]
distinguished_name=dn
prompt=no
[dn]
CN=ldap.iam103.test
[server]
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature
extendedKeyUsage=serverAuth
subjectAltName=DNS:ldap.iam103.test,IP:127.0.0.1
''')
conf='''include /tmp/fixture/openldap/etc/openldap/schema/core.schema
include /tmp/fixture/openldap/etc/openldap/schema/cosine.schema
include /tmp/fixture/openldap/etc/openldap/schema/inetorgperson.schema
pidfile /tmp/fixture/slapd.pid
argsfile /tmp/fixture/slapd.args
TLSCACertificateFile /tmp/fixture/ca.pem
TLSCertificateFile /tmp/fixture/server.pem
TLSCertificateKeyFile /tmp/fixture/server-key.pem
TLSProtocolMin 3.3
threads 2
tool-threads 1
sizelimit 100
timelimit 3
database mdb
maxsize 16777216
directory /tmp/fixture/db
suffix "BASE"
rootdn "ADMIN"
rootpw HASH
access to attrs=userPassword
 by anonymous auth
 by self auth
 by * none
access to *
 by dn.exact="READER" read
 by self read
 by * none
'''.replace('BASE',base).replace('ADMIN',adminDN).replace('HASH',ssha(admin)).replace('READER',readerDN)
(fixture/'slapd.conf').write_text(conf)
ldif=[f'dn: {base}\nobjectClass: domain\ndc: fixture\n']
for ou in ['system','people','groups']:ldif.append(f'dn: ou={ou},{base}\nobjectClass: organizationalUnit\nou: {ou}\n')
for dn,uid,cn,pw in [(readerDN,'reader','Reader',reader),('uid=alice,ou=people,'+base,'alice','Alice',user),('cn=duplicate-one,ou=people,'+base,'duplicate','duplicate-one',user),('cn=duplicate-two,ou=people,'+base,'duplicate','duplicate-two',user)]:
 ldif.append(f'dn: {dn}\nobjectClass: inetOrgPerson\nuid: {uid}\ncn: {cn}\nsn: Fixture\nuserPassword: {ssha(pw)}\n')
for i in range(5):ldif.append(f'dn: cn=group-{i},ou=groups,{base}\nobjectClass: groupOfNames\ncn: group-{i}\nmember: uid=alice,ou=people,{base}\n')
(fixture/'seed.ldif').write_text('\n'.join(ldif))
for f in fixture.iterdir():f.chmod(0o600)
script='''set -eu
umask 077
mkdir /tmp/fixture/db /tmp/fixture/artifacts
dpkg -i /tmp/libssl-dev.deb
cp /usr/share/doc/libssl-dev/copyright /tmp/fixture/artifacts/libssl-dev-copyright
dpkg-query -W -f='${Package}\\t${Version}\\t${Architecture}\\n' > /tmp/fixture/artifacts/packages.tsv
cd /tmp
tar xzf openldap-2.6.16.tgz
cd /tmp/openldap-2.6.16
./configure --prefix=/tmp/fixture/openldap --disable-backends --enable-mdb --disable-overlays --without-cyrus-sasl --with-tls=openssl --disable-shared --disable-modules --disable-balancer --disable-syslog CFLAGS=-O2 > /tmp/fixture/artifacts/configure.log 2>&1
make depend > /tmp/fixture/artifacts/depend.log 2>&1
for component in include libraries clients servers; do
 make -j2 -C "$component" >> /tmp/fixture/artifacts/make.log 2>&1
done
for component in include libraries clients servers; do
 make -C "$component" install >> /tmp/fixture/artifacts/install.log 2>&1
done
cp config.log /tmp/fixture/artifacts/config.log
/tmp/fixture/openldap/libexec/slapd -VV > /tmp/fixture/artifacts/version.log 2>&1
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 2 -config /tmp/fixture/ca.cnf -keyout /tmp/fixture/ca-key.pem -out /tmp/fixture/ca.pem > /tmp/fixture/artifacts/ca-generation.log 2>&1
openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -config /tmp/fixture/server.cnf -keyout /tmp/fixture/server-key.pem -out /tmp/fixture/server.csr > /tmp/fixture/artifacts/leaf-generation.log 2>&1
openssl x509 -req -in /tmp/fixture/server.csr -CA /tmp/fixture/ca.pem -CAkey /tmp/fixture/ca-key.pem -set_serial 2 -days 2 -extfile /tmp/fixture/server.cnf -extensions server -out /tmp/fixture/server.pem > /tmp/fixture/artifacts/leaf-signing.log 2>&1
/tmp/fixture/openldap/sbin/slapadd -f /tmp/fixture/slapd.conf -l /tmp/fixture/seed.ldif > /tmp/fixture/artifacts/seed.log 2>&1
/tmp/fixture/openldap/libexec/slapd -f /tmp/fixture/slapd.conf -h ldaps://127.0.0.1:1636 -d 0 > /tmp/fixture/artifacts/server.log 2>&1 &
server_pid=$!
trap 'kill "$server_pid" 2>/dev/null || true; wait "$server_pid" 2>/dev/null || true' EXIT HUP INT TERM
ready=no
for attempt in $(seq 1 40); do
 if LDAPTLS_CACERT=/tmp/fixture/ca.pem LDAPTLS_REQCERT=demand /tmp/fixture/openldap/bin/ldapwhoami -x -H ldaps://127.0.0.1:1636 -D "cn=fixtureadmin,dc=fixture,dc=test" -y /tmp/fixture/admin-password > /tmp/fixture/artifacts/readiness.log 2>&1; then ready=yes; break; fi
 sleep 0.1
done
test "$ready" = yes
IAM103_GROUP_GRAPH_OUTPUT_FILE=/tmp/fixture/artifacts/group-graph.json IAM103_FIXTURE_CONFIG=/tmp/fixture/config.json /tmp/directory.test -test.v -test.timeout=45s -test.run='^TestLive' > /tmp/fixture/artifacts/live-tests.log 2>&1
printf 'IAM103_LIVE_PASSED\\n'
'''
(stage/'container-script.sh').write_text(script)
img='golang@sha256:cfa27959837144bf4649802ba38e59daffd0dd2a7994aa83daaf113285b0f551'
cid=subprocess.check_output(['docker','create','--name','iam103-groups-'+uuid.uuid4().hex[:10],'--network','none','--memory','1536m','--cpus','2','--pids-limit','128','--security-opt','no-new-privileges',img,'sh','/tmp/run.sh'],text=True).strip()
(stage/'container-id').write_text(cid+'\n')
try:
 for local,remote in [(stage/'directory.test','/tmp/directory.test'),(stage/'container-script.sh','/tmp/run.sh'),(fixture,'/tmp/fixture'),('/Users/passion./.codex/ownsolid-evidence/iam103-protocol-20261113.oogmig8q/source/openldap-2.6.16.tgz','/tmp/openldap-2.6.16.tgz'),('/Users/passion./.codex/ownsolid-evidence/iam103-protocol-20261113.oogmig8q/source/acquired/libssl-dev_3.5.7-1~deb13u3_arm64.deb','/tmp/libssl-dev.deb')]:
  run(['docker','cp',str(local),cid+':'+remote],'copy-'+pathlib.Path(local).name+'.log')
 try:run(['docker','start','-a',cid],'container.log',timeout=480)
 finally:
  info=json.loads(subprocess.check_output(['docker','inspect',cid],text=True))[0]
  (stage/'container-inspect.json').write_text(json.dumps(info,indent=2)+'\n')
  subprocess.run(['docker','cp',cid+':/tmp/fixture/artifacts',str(stage/'artifacts')],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 if info['State']['ExitCode']!=0:raise RuntimeError('private provider qualification failed')
 (stage/'success.json').write_text(json.dumps({'native_race':True,'live':True,'image':img,'network':'none','host_ports':[],'at':time.time()},indent=2)+'\n')
 print(json.dumps({'phase':'live-passed','stage':str(stage)}),flush=True)
finally:
 subprocess.run(['docker','rm','-f',cid],check=True,capture_output=True)
