from pathlib import Path
import fcntl,hashlib,json,os,secrets,shutil,signal,stat,subprocess,sys,tarfile,time
root=Path(sys.argv[1]);baseline=Path('/root/lane-arc107-installed.149a9501.c92cc7f9/source');os.umask(0o077)
assert root.name.startswith('lane-arc111-composed-prototype.') and not (root/'exit').exists()
archive=root/'prototype-input.tar.gz';assert archive.stat().st_size==7805 and hashlib.sha256(archive.read_bytes()).hexdigest()=='eac225cd29a957cf8afcb525b3255476ead4316d16b190575568a8781f8b2d87'
locks=[];child=None;container=None
try:
 for name in ['/run/lock/ownsolid-heavy.lock','/root/lane-sci-tools/heavy-test.lock']:
  p=Path(name);fd=os.open(p,os.O_RDWR|os.O_NOFOLLOW);s=os.fstat(fd);assert stat.S_ISREG(s.st_mode) and s.st_uid==os.getuid()
  try:fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
  except BlockingIOError:os.close(fd);raise SystemExit(75)
  assert (s.st_dev,s.st_ino)==(p.lstat().st_dev,p.lstat().st_ino);locks.append(fd)
 memory={x.split(':')[0]:int(x.split(':')[1].split()[0]) for x in Path('/proc/meminfo').read_text().splitlines() if x.split(':')[0] in ['MemFree','MemAvailable']};psi=float(Path('/proc/pressure/io').read_text().split('full avg10=')[1].split()[0]);disk=shutil.disk_usage(root)
 preflight={'memory_kib':memory,'full_io_psi_avg10':psi,'disk_free_bytes':disk.free,'baseline_source':'149a95013db6c6d1ca9dbadd09b05fff9b4c98de','scope':'external node SQL overlay and current schema60 fixture; restricted composed observations, liveness, wakes and aggregate role limit; no product activation or TLS/server/complete-profile acceptance'};(root/'preflight.json').write_text(json.dumps(preflight,indent=2)+'\n');print(json.dumps(preflight),flush=True)
 if memory['MemFree']<2097152 or memory['MemAvailable']<8388608 or psi>5 or disk.free<30*1024**3:raise SystemExit(75)
 commands=subprocess.check_output(['ps','-e','-o','comm='],text=True).splitlines();assert not any(Path(x.strip()).name in {'go','compile','link','gcc','cc1','rustc','cargo','make'} for x in commands)
 with tarfile.open(archive) as t:
  assert set(t.getnames())=={'gateway-authority-proto_test.go','preparation_source_repository.go'}
  for m in t:
   assert m.isfile();(root/m.name).write_bytes(t.extractfile(m).read())
 originals={'preparation_source_repository.go':'services/control/internal/modules/nodes/preparation_source_repository.go'}
 original=(baseline/originals['preparation_source_repository.go']).read_text();candidate=(root/'preparation_source_repository.go').read_text()
 old="""WITH installed AS MATERIALIZED (
		SELECT i.tenant_id FROM installation_state i JOIN tenants t ON t.id=i.tenant_id
		WHERE i.id=1 AND i.bootstrap_complete AND t.status='active' AND i.tenant_id=$3
		FOR SHARE OF i,t
	)""";new="""WITH installed AS MATERIALIZED (
		SELECT prototype_lock_installation($3::uuid) AS tenant_id
	)"""
 assert original.count(old)==1 and original.replace(old,new)==candidate,'Overlay changes more than the researched authority-lock read'
 proof=json.loads((baseline.parent/'installed-source-verified.json').read_text());assert proof['source']=='149a95013db6c6d1ca9dbadd09b05fff9b4c98de' and proof['every_source_byte_and_mode_verified']
 overlay={'Replace':{str(baseline/originals['preparation_source_repository.go']):str(root/'preparation_source_repository.go'),str(baseline/'services/control/internal/modules/nodes/gateway_authority_proto_test.go'):str(root/'gateway-authority-proto_test.go')}};(root/'overlay.json').write_text(json.dumps(overlay)+'\n')
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
 code=execute(['taskset','-c',cpus,go,'-C',str(baseline/'services/control'),'test','-c','-overlay',str(root/'overlay.json'),'-o',str(root/'node-authority.test'),'./internal/modules/nodes'],'compile.log',300);assert code==0,('compile',code)
 print('Actual node-path prototype binary compiled',flush=True)
 image='pgvector/pgvector:0.8.7-pg18-bookworm';image_info=json.loads(subprocess.check_output(['docker','image','inspect',image]))[0];assert image_info['Id']=='sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47'
 password=secrets.token_urlsafe(32);secret=root/'postgres-password';secret.write_text(password+'\n');uid=int(subprocess.check_output(['docker','run','--rm','--network=none','--entrypoint=id',image,'-u','postgres'],text=True).strip());assert uid>0;os.chown(secret,uid,uid);os.chmod(secret,0o600)
 dsn=root/'database-input';dsn.write_text('postgres://postgres:'+password+'@127.0.0.1:5432/arc111_node_test?sslmode=disable\n');os.chmod(dsn,0o600)
 container='ownsolid-arc111-composed-'+root.name.rsplit('.',1)[1]
 subprocess.run(['docker','run','--detach','--name',container,'--network=none','--cpus=2','--memory=1g','--pids-limit=256','--env','POSTGRES_DB=arc111_node_test','--env','POSTGRES_USER=postgres','--env','POSTGRES_PASSWORD_FILE=/run/secrets/postgres-password','--env','POSTGRES_INITDB_ARGS=--auth-host=scram-sha-256 --auth-local=trust','--volume',str(secret)+':/run/secrets/postgres-password:ro','--volume',str(root)+':'+str(root),'--volume',str(baseline)+':'+str(baseline)+':ro',image,'postgres','-c','log_statement=none','-c','log_parameter_max_length=0','-c','log_min_error_statement=panic'],check=True,stdout=subprocess.DEVNULL)
 deadline=time.monotonic()+60
 while subprocess.run(['docker','exec',container,'pg_isready','-U','postgres','-d','arc111_node_test'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode:
  assert time.monotonic()<deadline;time.sleep(.5)
 with (root/'vector-extension.log').open('wb') as log:
  subprocess.run(['docker','exec',container,'psql','-X','-qAt','-v','ON_ERROR_STOP=1','-U','postgres','-d','arc111_node_test','-c','CREATE EXTENSION vector;','-c',"SELECT extversion FROM pg_extension WHERE extname='vector' AND extnamespace='public'::regnamespace;"],stdout=log,stderr=subprocess.STDOUT,check=True,timeout=20)
 assert (root/'vector-extension.log').read_text().strip()=='0.8.7','Approved fixture vector extension differs'
 (root/'stage').write_text('actual-product-sql\n')
 code=execute(['docker','exec','--user','0','--workdir',str(baseline/'services/control/internal/modules/nodes'),'--env','GOMAXPROCS=2','--env','GOMEMLIMIT=512MiB','--env','PROJECT_ONE_TEST_DSN_FILE='+str(dsn),'--env','TMPDIR='+str(tmp),container,str(root/'node-authority.test'),'-test.v','-test.timeout=120s','-test.run=^TestRestrictedComposedGatewayAuthorityPrototype$'],'actual-node.log',150)
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
  for name in ['postgres-password','database-input']:
   if (root/name).exists():(root/name).unlink()
 for fd in reversed(locks):fcntl.flock(fd,fcntl.LOCK_UN);os.close(fd)
