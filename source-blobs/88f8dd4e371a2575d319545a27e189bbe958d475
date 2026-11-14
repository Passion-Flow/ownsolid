from pathlib import Path
import fcntl,hashlib,json,os,shutil,stat,subprocess,sys,tarfile,time
r=Path(sys.argv[1]);assert r.name.startswith('lane-arc107-handoff.cf93c92e.')
assert not(r/'idle-qualify-exit').exists();locks=[]
try:
 for name in ('/run/lock/ownsolid-heavy.lock','/root/lane-sci-tools/heavy-test.lock'):
  p=Path(name);fd=os.open(p,os.O_RDWR|os.O_NOFOLLOW);info=os.fstat(fd);assert stat.S_ISREG(info.st_mode)and info.st_uid==os.getuid()
  try:fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
  except BlockingIOError:os.close(fd);raise SystemExit(75)
  assert(info.st_dev,info.st_ino)==(p.lstat().st_dev,p.lstat().st_ino);locks.append(fd)
 memory={line.partition(':')[0]:int(line.partition(':')[2].split()[0])for line in Path('/proc/meminfo').read_text().splitlines()if line.partition(':')[0]in('MemFree','MemAvailable')}
 psi=float(Path('/proc/pressure/io').read_text().split('full avg10=')[1].split()[0]);assert shutil.disk_usage(r).free>=30*1024**3
 if memory['MemFree']<2097152 or memory['MemAvailable']<8388608 or psi>5:raise SystemExit(75)
 if any(Path(p.strip()).name in {'go','compile','link','gcc','cc1','rustc','cargo','make'}for p in subprocess.check_output(['ps','-e','-o','comm='],text=True).splitlines()):raise SystemExit(75)
 output=r/'idle-qualify';output.mkdir(mode=0o700)
 (output/'preflight.json').write_text(json.dumps({'memory_kib':memory,'io_full_psi_avg10':psi,'scope':'external immutable idle-owner candidate; complete native gateway race and vet'},indent=2)+'\n')
 archive=r/'native-idle-overlay.tar.gz';assert archive.stat().st_size==9394 and hashlib.sha256(archive.read_bytes()).hexdigest()=='095997d301860ea4afa816e00826f355ebd475ec1596730b4a5d222ee05d704a'
 with tarfile.open(archive)as bundle:
  assert set(bundle.getnames())=={'idle-gateway.go','idle-gateway-test.go'}
  for name in bundle.getnames():(output/name).write_bytes(bundle.extractfile(name).read())
 source=r/'source';package=source/'services/control/internal/platform/agentgateway'
 overlay={'Replace':{str(package/'gateway.go'):str(output/'idle-gateway.go'),str(package/'gateway_test.go'):str(output/'idle-gateway-test.go')}};(output/'overlay.json').write_text(json.dumps(overlay)+'\n')
 env=os.environ.copy();env.update(GOTOOLCHAIN='local',GOPROXY='off',GOSUMDB='off',GOMAXPROCS='2',GOFLAGS='-p=1 -mod=readonly',GOMEMLIMIT='512MiB',CGO_ENABLED='1',GOENV='off',GOCACHE='/root/lane-sci-tools/go-cache',GOMODCACHE='/root/lane-sci-tools/go-mod',TMPDIR=str(r/'build-tmp'))
 cpus=','.join(str(cpu)for cpu in sorted(os.sched_getaffinity(0))[:2]);go='/root/lane-sci-tools/go/bin/go';binary=output/'gateway-race.test';results={}
 commands=[('compile',['taskset','-c',cpus,go,'-C',str(source/'services/control'),'test','-c','-race','-overlay',str(output/'overlay.json'),'-o',str(binary),'./internal/platform/agentgateway'],300),('race',['unshare','--net','bash','-c','set -euo pipefail; ip link set lo up; cd "$3"; exec taskset -c "$1" "$2" -test.v -test.timeout=90s','arc107-idle',cpus,str(binary),str(package)],105),('vet',['taskset','-c',cpus,go,'-C',str(source/'services/control'),'vet','-overlay',str(output/'overlay.json'),'./internal/platform/agentgateway'],300)]
 for name,command,budget in commands:
  start=time.time()
  with(output/(name+'.log')).open('wb')as log:code=subprocess.run(command,env=env,stdout=log,stderr=subprocess.STDOUT,timeout=budget).returncode
  results[name]={'exit':code,'seconds':time.time()-start,'log_sha256':hashlib.sha256((output/(name+'.log')).read_bytes()).hexdigest()}
  if code:break
 code=max(item['exit']for item in results.values())
 if code==0:
  text=(output/'race.log').read_text();assert '\nPASS\n'in text and '--- SKIP:'not in text and '--- FAIL:'not in text
 (output/'qualification.json').write_text(json.dumps({'base_source':'cf93c92ee0a7f57ca74ad16d91e9ef3dad23f1ae','external_overlay_sha256':'095997d301860ea4afa816e00826f355ebd475ec1596730b4a5d222ee05d704a','results':results},indent=2)+'\n');(r/'idle-qualify-exit').write_text(str(code)+'\n');print(results,flush=True);raise SystemExit(code)
finally:
 for fd in reversed(locks):fcntl.flock(fd,fcntl.LOCK_UN);os.close(fd)
