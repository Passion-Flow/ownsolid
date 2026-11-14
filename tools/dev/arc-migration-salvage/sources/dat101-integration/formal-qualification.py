from pathlib import Path
import fcntl,hashlib,json,os,signal,stat,subprocess,tempfile,shutil,time
base=Path(__file__).resolve().parent
repo=base.parent.parent/'lane-arc-dat101'
os.chdir(repo)
carrier=Path('/private/tmp/ownsolid-workstation-full-gate.lock')
fd=os.open(carrier,os.O_RDWR|os.O_NOFOLLOW)
info=os.fstat(fd)
assert stat.S_ISREG(info.st_mode) and info.st_uid==os.getuid()
try: fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
except BlockingIOError:
 os.close(fd);print('Workstation gate busy; no child started',flush=True);raise SystemExit(75)
child=None
try:
 assert (info.st_dev,info.st_ino)==(carrier.lstat().st_dev,carrier.lstat().st_ino)
 assert subprocess.run(['pgrep','-x','make|cargo|rustc|compile|link'],capture_output=True).returncode!=0
 assert subprocess.check_output(['git','status','--porcelain'],text=True)==''
 assert subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()=='f110757995e081544b836ec240474a700176571e'
 disk=os.statvfs(repo);assert disk.f_bavail*disk.f_frsize>=15*1024**3
 run=Path(tempfile.mkdtemp(prefix='formal-qualification.',dir=str(base)))
 (base/'latest-formal-qualification').write_text(str(run)+'\n')
 (run/'runner-pid').write_text(str(os.getpid())+'\n')
 (run/'resource-preflight').write_bytes(subprocess.check_output(['df','-h','/'])+subprocess.check_output(['memory_pressure','-Q']))
 assert subprocess.check_output(['git','diff','--name-only','64247b17c60217eec1325667ad90d27062926951','HEAD'],text=True).splitlines()==['services/control/internal/operations/databaseaccess/postgres_catalog_test.go']
 (run/'reused-race.json').write_text(json.dumps({'source':'64247b17c60217eec1325667ad90d27062926951','log':str(base/'formal-qualification.4nogol2y/race.log'),'sha256':hashlib.sha256((base/'formal-qualification.4nogol2y/race.log').read_bytes()).hexdigest(),'passed_unchanged_packages':['internal/platform/database','internal/modules/datasets'],'rerun_changed_package':'internal/operations/databaseaccess'},indent=2)+'\n')
 (run/'input').write_bytes(subprocess.check_output(['git','rev-parse','HEAD','HEAD^{tree}','origin/main']))
 original=base.parent/'gw120-schema59.9jT6tZsI/mac-catalog-matrix.zem9ye4q'
 files=['database.test','audit-verify','catalog.sql','prefix.jsonl']+['schema'+str(v)+'.dump' for v in range(55,60)]
 hashes={}
 for name in files:
  p=original/name;hashes[name]=hashlib.sha256(p.read_bytes()).hexdigest()
  shutil.copy2(p,run/('accepted59.test' if name=='database.test' else name))
 (run/'reused-inputs.json').write_text(json.dumps({'original':str(original),'hashes':hashes,'accepted59_migrator_source':'a446ec580cf6dffc63df5d8269518f1ef8175d40','scope':'immutable predecessor dumps and accepted59 binary reused to construct native59; all current migrations and runtime tests use the pinned current candidate'},indent=2)+'\n')
 for name in ['formal-qualification.sh','formal-overlay.json','formal_restore_matrix_test.go']:
  shutil.copy2(base/name,run/name)
 (run/'harness-hashes.json').write_text(json.dumps({n:hashlib.sha256((run/n).read_bytes()).hexdigest() for n in ['formal-qualification.sh','formal-overlay.json','formal_restore_matrix_test.go']},indent=2)+'\n')
 def stop(signum,frame):
  if child is not None and child.poll() is None:
   os.killpg(child.pid,signal.SIGTERM)
   try: child.wait(timeout=15)
   except subprocess.TimeoutExpired: os.killpg(child.pid,signal.SIGKILL);child.wait()
  raise SystemExit(143)
 signal.signal(signal.SIGTERM,stop);signal.signal(signal.SIGINT,stop)
 print('DAT-101 formal schema60 qualification inputf1107579 '+str(run),flush=True)
 started=time.time()
 with (run/'run.log').open('wb') as out:
  child=subprocess.Popen(['caffeinate','-i','bash',str(run/'formal-qualification.sh'),str(run),str(repo)],stdout=out,stderr=subprocess.STDOUT,start_new_session=True)
  code=child.wait(timeout=1200)
 (run/'exit').write_text(str(code)+'\n')
 for name,digest in hashes.items():
  assert hashlib.sha256((original/name).read_bytes()).hexdigest()==digest,'Original changed: '+name
  assert hashlib.sha256((run/('accepted59.test' if name=='database.test' else name)).read_bytes()).hexdigest()==digest,'Copied input changed: '+name
 (run/'output-hashes.json').write_text(json.dumps({p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in run.iterdir() if p.is_file() and (p.suffix in ['.log','.test','.dump','.jsonl'])},indent=2)+'\n')
 for name in ['run.log','race.log','compile-database.log','compile-access.log','compile-datasets.log','database.log','access.log','predecessor.log','matrix.log','lineage.log']:
  p=run/name
  if p.exists():print(name+':\n'+p.read_text()[-2200:],flush=True)
 print('Formal qualification exit '+str(code)+' elapsed '+str(round(time.time()-started,2)),flush=True)
 raise SystemExit(code)
finally:
 if child is not None and child.poll() is None:
  os.killpg(child.pid,signal.SIGTERM)
  try:child.wait(timeout=15)
  except subprocess.TimeoutExpired:os.killpg(child.pid,signal.SIGKILL);child.wait()
 fcntl.flock(fd,fcntl.LOCK_UN);os.close(fd)
