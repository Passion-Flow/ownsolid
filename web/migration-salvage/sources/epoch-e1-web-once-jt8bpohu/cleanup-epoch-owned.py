import time
from pathlib import Path
import hashlib,json,os,stat,subprocess,sys
root=Path(sys.argv[1]);deadline=float(sys.argv[2]);info=json.loads((root/'preparation.json').read_text());work=Path(info['worktree']);proof={'confirmed':False,'negativeFilesRemoved':[],'errors':[]}
def budget(cap):
 left=deadline-time.clock_gettime(time.CLOCK_MONOTONIC)
 if left<=0:raise TimeoutError('Cleanup budget exhausted')
 return min(cap,left)
try:
 owned=root/'owned-negative-files.json'
 if owned.exists():
  for row in json.loads(owned.read_text()):
   p=Path(row['path']);assert p.is_relative_to(work/'web') if hasattr(p,'is_relative_to') else str(p).startswith(str(work/'web')+'/')
   s=p.lstat();assert stat.S_ISREG(s.st_mode) and (s.st_dev,s.st_ino,s.st_uid)==(row['device'],row['inode'],row['uid']);p.unlink();proof['negativeFilesRemoved'].append(str(p))
 if (root/'selected-scope.json').exists() and json.loads((root/'selected-scope.json').read_text())['scope']=='A':
  p=subprocess.run([sys.executable,'-I',str(root/'cleanup-owned.py'),str(root),str(deadline)],capture_output=True,text=True,timeout=budget(45));(root/'full-resource-cleanup.log').write_text(p.stdout+p.stderr);assert p.returncode==0
 else:
  clean=subprocess.check_output(['git','status','--porcelain'],cwd=work,text=True,timeout=budget(5));(root/'source-cleanup-status.log').write_text(clean);assert clean==''
 proof['confirmed']=True
except BaseException as error:proof['errors'].append(repr(error))
(root/'epoch-owned-cleanup.json').write_text(json.dumps(proof,indent=2)+'\n')
raise SystemExit(0 if proof['confirmed'] else 1)
