from pathlib import Path
import fcntl,hashlib,json,os,secrets,shutil,signal,stat,subprocess,sys,tarfile,time
root=Path(sys.argv[1]);baseline=Path('/root/lane-arc110-native.274d771c.1a00a1e0/source');os.umask(0o077)
assert root.name.startswith('lane-arc111-tls-prototype.') and not (root/'exit').exists()
archive=root/'prototype-input.tar.gz';assert archive.stat().st_size==1791 and hashlib.sha256(archive.read_bytes()).hexdigest()=='07e0a60271977cc2412025d3a557f22b671ea076a226b44cd0ba4bb2275d0b0c'
locks=[];child=None;container=None
try:
 for name in ['/run/lock/ownsolid-heavy.lock','/root/lane-sci-tools/heavy-test.lock']:
  p=Path(name);fd=os.open(p,os.O_RDWR|os.O_NOFOLLOW);s=os.fstat(fd);assert stat.S_ISREG(s.st_mode) and s.st_uid==os.getuid()
  try:fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
  except BlockingIOError:os.close(fd);raise SystemExit(75)
  assert (s.st_dev,s.st_ino)==(p.lstat().st_dev,p.lstat().st_ino);locks.append(fd)
 memory={x.split(':')[0]:int(x.split(':')[1].split()[0]) for x in Path('/proc/meminfo').read_text().splitlines() if x.split(':')[0] in ['MemFree','MemAvailable']};psi=float(Path('/proc/pressure/io').read_text().split('full avg10=')[1].split()[0]);disk=shutil.disk_usage(root)
 preflight={'memory_kib':memory,'full_io_psi_avg10':psi,'disk_free_bytes':disk.free,'baseline_source':'274d771c8d3a97e3163d3dcb3b21630fccfac57f','scope':'external actual training claim composition diagnostic with normal control principal; source unchanged; no restricted-role permission or successful gateway-claim acceptance'};(root/'preflight.json').write_text(json.dumps(preflight,indent=2)+'\n');print(json.dumps(preflight),flush=True)
 if memory['MemFree']<2097152 or memory['MemAvailable']<8388608 or psi>5 or disk.free<30*1024**3:raise SystemExit(75)
 commands=subprocess.check_output(['ps','-e','-o','comm='],text=True).splitlines();assert not any(Path(x.strip()).name in {'go','compile','link','gcc','cc1','rustc','cargo','make'} for x in commands)
 with tarfile.open(archive) as ar:
  assert ar.getnames()==['gateway-training-store-proto_test.go']
  m=ar.next() if False else ar.getmembers()[0];assert m.isfile();(root/m.name).write_bytes(ar.extractfile(m).read())
 proof=json.loads((baseline.parent/'verified-source.json').read_text());assert proof['source']=='274d771c8d3a97e3163d3dcb3b21630fccfac57f' and proof['every_source_byte_and_mode_verified']
 manifest=json.loads((baseline.parent/'manifest.json').read_text());entries={e['path']:e for e in manifest['files']};actual=set()
 for p in baseline.rglob('*'):
  if p.is_symlink():data=os.readlink(p).encode();kind='symlink'
  elif p.is_file():data=p.read_bytes();kind='file'
  else:continue
  name=p.relative_to(baseline).as_posix();actual.add(name);e=entries[name];assert hashlib.sha256(data).hexdigest()==e['sha256'] and kind==e['kind'] and bool(p.lstat().st_mode&0o111)==e['executable'],name
 assert actual==set(entries)
 overlay={'Replace':{str(baseline/'services/control/internal/modules/training/gateway_training_store_proto_test.go'):str(root/'gateway-training-store-proto_test.go')}};(root/'overlay.json').write_text(json.dumps(overlay)+'\n')
 (root/'source-verified.json').write_text(json.dumps({'source':proof['source'],'all_5040_source_paths_reverified':True,'only_external_diagnostic_added':True})+'\n')
 env=os.environ.copy();env.update({'GOTOOLCHAIN':'local','GOPROXY':'off','GOSUMDB':'off','GOMAXPROCS':'2','GOFLAGS':'-p=1','GOMEMLIMIT':'512MiB','CGO_ENABLED':'0','GOENV':'off','GOCACHE':'/root/lane-sci-tools/go-cache','GOMODCACHE':'/root/lane-sci-tools/go-mod'});tmp=root/'tmp';tmp.mkdir(mode=0o700);env['TMPDIR']=str(tmp)
 cpus=','.join(str(n) for n in sorted(os.sched_getaffinity(0))[:2]);go='/root/lane-sci-tools/go/bin/go';assert subprocess.check_output([go,'version'],text=True).strip()=='go version go1.27.3 linux/amd64'
 def stop(sig,frame):raise SystemExit(143)
 signal.signal(signal.SIGTERM,stop);signal.signal(signal.SIGINT,stop);signal.signal(signal.SIGHUP,stop)
 def execute(command,name,budget):
  global child
  with (root/name).open('wb') as log:
   child=subprocess.Popen(command,env=env,stdout=log,stderr=subprocess.STDOUT,start_new_session=True)
   try:code=child.wait(timeout=budget)
   except subprocess.TimeoutExpired:
    os.killpg(child.pid,signal.SIGTERM)
    try:child.wait(timeout=15)
    except subprocess.TimeoutExpired:os.killpg(child.pid,signal.SIGKILL);child.wait()
    code=124
  return code
 (root/'stage').write_text('compile\n');(root/'start').write_text(str(time.time())+'\n')
 code=execute(['taskset','-c',cpus,go,'-C',str(baseline/'services/control'),'test','-c','-overlay',str(root/'overlay.json'),'-o',str(root/'training-store.test'),'./internal/modules/training'],'compile.log',300);assert code==0,('compile',code)
 print('Actual node-path prototype binary compiled',flush=True)
 image='pgvector/pgvector:0.8.7-pg18-bookworm';image_info=json.loads(subprocess.check_output(['docker','image','inspect',image]))[0];assert image_info['Id']=='sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47'
 password=secrets.token_urlsafe(32);secret=root/'postgres-password';secret.write_text(password+'\n');uid=int(subprocess.check_output(['docker','run','--rm','--network=none','--entrypoint=id',image,'-u','postgres'],text=True).strip());assert uid>0;os.chown(secret,uid,uid);os.chmod(secret,0o600)
 env['ARC111_TLS_DIRECTORY']=str(root);env['ARC111_POSTGRES_UID']=str(uid)
 crypto=Path('/root/lane-arc111-tls-prototype.e000fb57/node-authority.test');assert hashlib.sha256(crypto.read_bytes()).hexdigest()=='609528233237457c4fe9de046c5349dfb0b01c47d7b7c1eaded4ca13d88176f8'
 code=execute([str(crypto),'-test.v','-test.run=^TestPrototypeDatabaseTLSMaterial$'],'tls-material.log',20);assert code==0,('TLS material',code)
 hba=root/'pg-hba.conf';hba.write_text('local all all trust\nhostssl all all 127.0.0.1/32 scram-sha-256\nhostnossl all all 127.0.0.1/32 reject\nhost all all ::1/128 reject\n');os.chmod(hba,0o644)
 dsn=root/'database-input';dsn.write_text('postgres://postgres:'+password+'@127.0.0.1:5432/arc111_node_test?sslmode=verify-full&sslrootcert='+str(root/'database-ca.pem')+'&channel_binding=require\n');os.chmod(dsn,0o600)
 container='ownsolid-arc111-tls-'+root.name.rsplit('.',1)[1]
 subprocess.run(['docker','run','--detach','--name',container,'--network=none','--cpus=2','--memory=1g','--pids-limit=256','--env','POSTGRES_DB=arc111_node_test','--env','POSTGRES_USER=postgres','--env','POSTGRES_PASSWORD_FILE=/run/secrets/postgres-password','--env','POSTGRES_INITDB_ARGS=--auth-host=scram-sha-256 --auth-local=trust','--volume',str(secret)+':/run/secrets/postgres-password:ro','--volume',str(root/'database-ca.pem')+':/run/secrets/database-ca.pem:ro','--volume',str(root/'database-key.pem')+':/run/secrets/database-key.pem:ro','--volume',str(hba)+':/run/secrets/pg-hba.conf:ro','--volume',str(root)+':'+str(root),'--volume',str(baseline)+':'+str(baseline)+':ro',image,'postgres','-c','ssl=on','-c','ssl_cert_file=/run/secrets/database-ca.pem','-c','ssl_key_file=/run/secrets/database-key.pem','-c','hba_file=/run/secrets/pg-hba.conf','-c','log_statement=none','-c','log_parameter_max_length=0','-c','log_min_error_statement=panic'],check=True,stdout=subprocess.DEVNULL)
 passfile=root/'readiness-password';passfile.write_text('127.0.0.1:5432:arc111_node_test:postgres:'+password+'\n');os.chmod(passfile,0o600)
 deadline=time.monotonic()+60
 while True:
  probe=subprocess.run(['docker','exec','--env','PGPASSFILE='+str(passfile),'--env','PGSSLMODE=verify-full','--env','PGSSLROOTCERT='+str(root/'database-ca.pem'),'--env','PGCHANNELBINDING=require',container,'psql','-X','-h','127.0.0.1','-qAt','-v','ON_ERROR_STOP=1','-U','postgres','-d','arc111_node_test','-c','SELECT current_database()'],capture_output=True,text=True,timeout=5)
  (root/'database-readiness.log').write_text(probe.stdout+probe.stderr)
  if probe.returncode==0 and probe.stdout.strip()=='arc111_node_test':break
  assert time.monotonic()<deadline,'Actual verified TLS TCP database readiness deadline exceeded'
  time.sleep(.5)
 with (root/'vector-extension.log').open('wb') as log:
  subprocess.run(['docker','exec',container,'psql','-X','-qAt','-v','ON_ERROR_STOP=1','-U','postgres','-d','arc111_node_test','-c','CREATE EXTENSION vector;','-c',"SELECT extversion FROM pg_extension WHERE extname='vector' AND extnamespace='public'::regnamespace;"],stdout=log,stderr=subprocess.STDOUT,check=True,timeout=20)
 assert (root/'vector-extension.log').read_text().strip()=='0.8.7','Approved fixture vector extension differs'
 (root/'stage').write_text('actual-product-sql\n')
 detail=json.loads(subprocess.check_output(['docker','inspect',container]))[0];assert detail['Name']=='/'+container and detail['HostConfig']['NetworkMode']=='none' and detail['State']['Running']
 target=int(detail['State']['Pid']);assert target>1
 namespace=os.readlink('/proc/'+str(target)+'/ns/net');env['ARC111_EXPECTED_NETWORK_NAMESPACE']=namespace;env['PROJECT_ONE_DATA_PYTHON']='/usr/bin/python3.12';env['PROJECT_ONE_TEST_DSN_FILE']=str(dsn)
 assert subprocess.check_output(['/usr/bin/python3.12','--version'],text=True).strip()=='Python 3.12.3';assert subprocess.check_output(['nsenter','--version'],text=True).strip()=='nsenter from util-linux 2.39.3'
 code=execute(['taskset','-c',cpus,'nsenter','--target',str(target),'--net','--',str(root/'training-store.test'),'-test.v','-test.timeout=120s','-test.run=^TestObserveGatewayTrainingClaimArtifactOwnership$'],'actual-node.log',150)
 (root/'exit').write_text(str(code)+'\n');(root/'finish').write_text(str(time.time())+'\n');print('Actual restricted node prototype exit '+str(code),flush=True);print((root/'actual-node.log').read_text()[-6000:],flush=True);assert code==0,('actual restricted node SQL',code)
except BaseException:
 if child is not None and child.poll() is None:
  os.killpg(child.pid,signal.SIGTERM)
  try:child.wait(timeout=15)
  except subprocess.TimeoutExpired:os.killpg(child.pid,signal.SIGKILL);child.wait()
 raise
finally:
 if container is not None:
  with (root/'postgres.log').open('wb') as log:subprocess.run(['docker','logs',container],stdout=log,stderr=subprocess.STDOUT,timeout=10)
  subprocess.run(['docker','rm','--force','--volumes',container],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=30)
 for name in ['postgres-password','database-input','database-key.pem','readiness-password']:
  if (root/name).exists():(root/name).unlink()
 for fd in reversed(locks):fcntl.flock(fd,fcntl.LOCK_UN);os.close(fd)
