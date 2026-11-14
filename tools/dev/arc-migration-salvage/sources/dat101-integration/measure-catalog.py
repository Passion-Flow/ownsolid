from pathlib import Path
import fcntl,hashlib,json,os,signal,stat,subprocess,tempfile,shutil
base=Path(__file__).resolve().parent;repo=base.parent.parent/'lane-arc-dat101';os.chdir(repo)
carrier=Path('/private/tmp/ownsolid-workstation-full-gate.lock');fd=os.open(carrier,os.O_RDWR|os.O_NOFOLLOW);info=os.fstat(fd)
assert stat.S_ISREG(info.st_mode) and info.st_uid==os.getuid()
try:fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
except BlockingIOError:os.close(fd);raise SystemExit(75)
child=None
try:
 assert (info.st_dev,info.st_ino)==(carrier.lstat().st_dev,carrier.lstat().st_ino)
 assert subprocess.run(['pgrep','-x','make|cargo|rustc|compile|link'],capture_output=True).returncode!=0
 disk=os.statvfs(repo);assert disk.f_bavail*disk.f_frsize>15*1024**3
 run=Path(tempfile.mkdtemp(prefix='prospective-catalog.',dir=str(base)))
 (base/'latest-prospective-catalog').write_text(str(run)+'\n');(run/'runner-pid').write_text(str(os.getpid())+'\n')
 (run/'input').write_bytes(subprocess.check_output(['git','rev-parse','HEAD','HEAD^{tree}','origin/main']))
 assert subprocess.check_output(['git','diff','--name-only','a446ec580cf6dffc63df5d8269518f1ef8175d40','HEAD','--','services/control/internal/platform/database','api/release'],text=True).strip()==''
 original=base.parent/'gw120-schema59.9jT6tZsI/mac-catalog-matrix.zem9ye4q'
 files=['database.test','predecessor.test','audit-verify','catalog.sql','prefix.jsonl']+['schema'+str(v)+'.dump' for v in range(55,60)]
 hashes={}
 for name in files:
  p=original/name;hashes[name]=hashlib.sha256(p.read_bytes()).hexdigest();shutil.copy2(p,run/name)
 (run/'reused-inputs.json').write_text(json.dumps({'original':str(original),'hashes':hashes,'accepted_migrator_source':'a446ec580cf6dffc63df5d8269518f1ef8175d40','scope':'accepted schema59 migrator and retained historical dumps reused; new receipt DDL measured independently; no schema60 production acceptance claimed'},indent=2)+'\n')
 (run/'receipt.up.sql').write_bytes((repo/'services/control/internal/modules/datasets/testdata/draft_import_candidate.up.sql').read_bytes())
 (run/'receipt.down.sql').write_bytes((base/'proposed-formal.down.sql').read_bytes())
 (run/'replay.sh').write_bytes((base/'measure-catalog.sh').read_bytes())
 def stop(signum,frame):
  if child is not None and child.poll() is None:
   os.killpg(child.pid,signal.SIGTERM)
   try:child.wait(timeout=15)
   except subprocess.TimeoutExpired:os.killpg(child.pid,signal.SIGKILL);child.wait()
  raise SystemExit(143)
 signal.signal(signal.SIGTERM,stop);signal.signal(signal.SIGINT,stop)
 print('DAT-101 prospective catalog measurement '+str(run),flush=True)
 with (run/'run.log').open('wb') as out:
  child=subprocess.Popen(['caffeinate','-i','bash',str(run/'replay.sh'),str(repo)],stdout=out,stderr=subprocess.STDOUT,start_new_session=True)
  code=child.wait(timeout=300)
 (run/'exit').write_text(str(code)+'\n')
 for name,digest in hashes.items():assert hashlib.sha256((original/name).read_bytes()).hexdigest()==digest and hashlib.sha256((run/name).read_bytes()).hexdigest()==digest,'Retained predecessor input changed: '+name
 print((run/'run.log').read_text()[-7000:],flush=True)
 raise SystemExit(code)
finally:
 if child is not None and child.poll() is None:
  os.killpg(child.pid,signal.SIGTERM)
  try:child.wait(timeout=15)
  except subprocess.TimeoutExpired:os.killpg(child.pid,signal.SIGKILL);child.wait()
 fcntl.flock(fd,fcntl.LOCK_UN);os.close(fd)
