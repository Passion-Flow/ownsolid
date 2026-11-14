from pathlib import Path
import os,subprocess,tempfile,time
base=Path(__file__).resolve().parent;repo=Path.cwd().resolve()
run=Path(tempfile.mkdtemp(prefix='mac-snapshot-probe.',dir=str(base)));(base/'queued-mac-probe').write_text(str(run)+'\n')
(run/'runner-pid').write_text(str(os.getpid())+'\n');lock=Path('/private/tmp/ownsolid-workstation-full-gate.lock')
last=None;deadline=time.monotonic()+1800
while True:
 try: lock.mkdir()
 except FileExistsError:
  try: owner=(lock/'owner').read_text().strip()
  except FileNotFoundError: owner='initializing'
  if owner!=last: print('Snapshot prototype queued behind '+owner,flush=True);last=owner
 else:
  (lock/'owner').write_text(str(run)+'\n')
  if subprocess.run(['pgrep','-x','make|cargo|rustc|compile|link'],capture_output=True).returncode!=0: break
  (lock/'owner').unlink();lock.rmdir()
 if time.monotonic()>deadline: raise SystemExit('Bounded queue expired; no prototype started')
 time.sleep(15)
print('Acquired snapshot prototype gate '+str(run),flush=True)
with (run/'run.log').open('wb') as log:
 r=subprocess.run(['caffeinate','-i','bash',str(base/'mac-snapshot-probe.sh'),str(run),str(repo)],stdout=log,stderr=subprocess.STDOUT)
(run/'exit').write_text(str(r.returncode)+'\n');print((run/'run.log').read_text()[-3000:],flush=True)
raise SystemExit(r.returncode)
