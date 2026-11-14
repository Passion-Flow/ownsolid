from pathlib import Path
import fcntl,hashlib,json,os,secrets,select,shutil,signal,stat,subprocess,sys,tempfile,time,uuid
root=Path(sys.argv[1]);assert root.name.startswith('lane-arc111-lock.');os.umask(0o077);root.mkdir(mode=0o700,exist_ok=True)
assert not (root/'exit').exists(),'Inspect the retained attempt instead of repeating it'
locks=[];container=None;holder=None
try:
 for name in ['/run/lock/ownsolid-heavy.lock','/root/lane-sci-tools/heavy-test.lock']:
  p=Path(name);fd=os.open(p,os.O_RDWR|os.O_NOFOLLOW);s=os.fstat(fd);assert stat.S_ISREG(s.st_mode) and s.st_uid==os.getuid()
  try:fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
  except BlockingIOError:os.close(fd);raise SystemExit(75)
  assert (s.st_dev,s.st_ino)==(p.lstat().st_dev,p.lstat().st_ino);locks.append(fd)
 memory={x.split(':')[0]:int(x.split(':')[1].split()[0]) for x in Path('/proc/meminfo').read_text().splitlines() if x.split(':')[0] in ['MemFree','MemAvailable']};psi=float(Path('/proc/pressure/io').read_text().split('full avg10=')[1].split()[0]);disk=shutil.disk_usage(root)
 preflight={'memory_kib':memory,'full_io_psi_avg10':psi,'disk_free_bytes':disk.free,'scope':'synthetic PostgreSQL row-lock/function permission prototype; SET ROLE only, no production schema/Go startup or network/SCRAM acceptance'};(root/'preflight.json').write_text(json.dumps(preflight,indent=2)+'\n');print(json.dumps(preflight),flush=True)
 if memory['MemFree']<2097152 or memory['MemAvailable']<8388608 or psi>5 or disk.free<30*1024**3:raise SystemExit(75)
 commands=subprocess.check_output(['ps','-e','-o','comm='],text=True).splitlines();assert not any(Path(x.strip()).name in {'go','compile','link','gcc','cc1','rustc','cargo','make'} for x in commands)
 image='pgvector/pgvector:0.8.7-pg18-bookworm';metadata=json.loads(subprocess.check_output(['docker','image','inspect',image]))[0];assert metadata['Id']=='sha256:c29f30688a9ad961691bdf0ec1650717cc132a5814619d80f80463d6e4c42e47' and metadata['Architecture']=='amd64'
 uid=int(subprocess.check_output(['docker','run','--rm','--network=none','--entrypoint=id',image,'-u','postgres'],text=True).strip());assert uid>0
 password=root/'postgres-password';password.write_text(secrets.token_urlsafe(32)+'\n');os.chown(password,uid,uid);os.chmod(password,0o600)
 container='ownsolid-arc111-lock-'+root.name.rsplit('.',1)[1]
 subprocess.run(['docker','run','--detach','--name',container,'--network=none','--cpus=2','--memory=512m','--pids-limit=128','--env','POSTGRES_DB=arc111_lock_test','--env','POSTGRES_USER=postgres','--env','POSTGRES_PASSWORD_FILE=/run/secrets/postgres-password','--volume',str(password)+':/run/secrets/postgres-password:ro',image],check=True,stdout=subprocess.DEVNULL)
 deadline=time.monotonic()+60
 while subprocess.run(['docker','exec',container,'pg_isready','-U','postgres','-d','arc111_lock_test'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode:
  assert time.monotonic()<deadline;time.sleep(.5)
 psql=['docker','exec','-i',container,'psql','-X','-At','-v','ON_ERROR_STOP=1','-v','VERBOSITY=verbose','-U','postgres','-d','arc111_lock_test']
 def sql(text,name,expected=0,state=None):
  r=subprocess.run(psql,input=text,text=True,capture_output=True,timeout=15);(root/(name+'.log')).write_text(r.stdout+r.stderr)
  assert r.returncode==expected,(name,r.returncode)
  if state:assert state in r.stderr,(name,'missing SQLSTATE')
  print(name+' passed',flush=True);return r.stdout
 schema='arc111_scope';role='arc111_gateway';other='arc111_foreign';tenant='11111111-1111-4111-8111-111111111111';unreferenced='22222222-2222-4222-8222-222222222222';changed='33333333-3333-4333-8333-333333333333'
 sql(f'''CREATE SCHEMA {schema};
 CREATE ROLE {role} NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
 CREATE ROLE {other} NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
 CREATE TABLE {schema}.tenants(id uuid PRIMARY KEY,status text NOT NULL);
 CREATE TABLE {schema}.installation_state(id integer PRIMARY KEY CHECK(id=1),tenant_id uuid NOT NULL REFERENCES {schema}.tenants(id),bootstrap_complete boolean NOT NULL);
 INSERT INTO {schema}.tenants VALUES('{tenant}','active'),('{unreferenced}','active');
 INSERT INTO {schema}.installation_state VALUES(1,'{tenant}',true);
 GRANT USAGE ON SCHEMA {schema} TO {role},{other};
 GRANT SELECT ON {schema}.tenants,{schema}.installation_state TO {role};
 SHOW server_version;''','setup')
 locking_select=f"SELECT i.tenant_id FROM {schema}.installation_state i JOIN {schema}.tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND t.status='active' FOR SHARE OF i,t;"
 sql(f'SET ROLE {role};'+locking_select,'select-only-lock-refused',3,'42501')
 sql(f'GRANT UPDATE(id) ON {schema}.tenants,{schema}.installation_state TO {role};SET ROLE {role};'+locking_select,'column-grant-lock')
 sql(f"SET ROLE {role};UPDATE {schema}.tenants SET status='disabled' WHERE id='{tenant}';",'column-grant-status-refused',3,'42501')
 output=sql(f"BEGIN;SET LOCAL ROLE {role};UPDATE {schema}.tenants SET id='{changed}' WHERE id='{unreferenced}' RETURNING id;ROLLBACK;",'column-grant-can-change-unreferenced-identity');assert changed in output
 sql(f'''REVOKE UPDATE(id) ON {schema}.tenants,{schema}.installation_state FROM {role};
 BEGIN;
 CREATE FUNCTION {schema}.lock_installation(expected uuid) RETURNS boolean LANGUAGE SQL SECURITY DEFINER SET search_path=pg_catalog,pg_temp
 BEGIN ATOMIC
 SELECT true FROM {schema}.installation_state i JOIN {schema}.tenants t ON t.id=i.tenant_id
 WHERE i.id=1 AND i.tenant_id=expected AND i.bootstrap_complete AND t.status='active' FOR SHARE OF i,t;
 END;
 REVOKE ALL ON FUNCTION {schema}.lock_installation(uuid) FROM PUBLIC;
 GRANT EXECUTE ON FUNCTION {schema}.lock_installation(uuid) TO {role};
 COMMIT;''','closed-definer-created')
 sql(f'SET ROLE {role};'+locking_select,'direct-lock-still-refused',3,'42501')
 output=sql(f"SET ROLE {role};SELECT current_user,COALESCE({schema}.lock_installation('{tenant}'),false),COALESCE({schema}.lock_installation('{unreferenced}'),false);",'definer-valid-and-foreign-scope');assert f'{role}|t|f' in output
 sql(f"SET ROLE {other};SELECT {schema}.lock_installation('{tenant}');",'foreign-execute-refused',3,'42501')
 output=sql(f"SET ROLE {role};CREATE TEMP TABLE tenants(id uuid,status text);CREATE TEMP TABLE installation_state(id integer,tenant_id uuid,bootstrap_complete boolean);SET search_path=pg_temp,public;SELECT COALESCE({schema}.lock_installation('{tenant}'),false);",'temporary-shadow-refused');assert '\nt\n' in '\n'+output
 holder=subprocess.Popen(psql,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=(root/'holder-stderr.log').open('wb'),bufsize=0)
 holder.stdin.write(f"BEGIN;SET LOCAL ROLE {role};SELECT {schema}.lock_installation('{tenant}');\\echo LOCK_HELD\n".encode());holder.stdin.flush()
 deadline=time.monotonic()+10;observed=bytearray()
 while not observed.endswith(b'LOCK_HELD\n'):
  wait=deadline-time.monotonic()
  if wait<=0 or not select.select([holder.stdout],[],[],wait)[0]:raise TimeoutError('The function lock was not observed')
  byte=os.read(holder.stdout.fileno(),1);assert byte,'The holder exited before confirming its lock';observed+=byte
 (root/'holder-stdout.log').write_bytes(observed)
 sql(f"SET lock_timeout='150ms';UPDATE {schema}.tenants SET status='disabled' WHERE id='{tenant}';",'tenant-writer-fenced',3,'55P03')
 sql(f"SET lock_timeout='150ms';UPDATE {schema}.installation_state SET bootstrap_complete=false WHERE id=1;",'installation-writer-fenced',3,'55P03')
 holder.stdin.write(b'ROLLBACK;\n');holder.stdin.close();assert holder.wait(timeout=10)==0;holder=None
 output=sql(f"SET lock_timeout='150ms';UPDATE {schema}.tenants SET status='disabled' WHERE id='{tenant}';SET ROLE {role};SELECT COALESCE({schema}.lock_installation('{tenant}'),false);",'rollback-releases-and-disabled-refuses');assert output.rstrip().endswith('f')
 output=sql(f"SELECT has_any_column_privilege('{role}','{schema}.tenants','UPDATE'),has_any_column_privilege('{role}','{schema}.installation_state','UPDATE');",'no-direct-update-remains');assert 'f|f' in output
 (root/'exit').write_text('0\n');print('Synthetic authority-lock prototype passed; no product role activated',flush=True)
finally:
 if holder is not None and holder.poll() is None:
  holder.terminate()
  try:holder.wait(timeout=5)
  except subprocess.TimeoutExpired:holder.kill();holder.wait()
 if container is not None:
  with (root/'postgres.log').open('wb') as out:subprocess.run(['docker','logs',container],stdout=out,stderr=subprocess.STDOUT,timeout=10)
  subprocess.run(['docker','rm','--force','--volumes',container],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=30)
  if (root/'postgres-password').exists():(root/'postgres-password').unlink()
 for fd in reversed(locks):fcntl.flock(fd,fcntl.LOCK_UN);os.close(fd)
