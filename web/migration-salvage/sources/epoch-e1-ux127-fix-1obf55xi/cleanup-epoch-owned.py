import time,json,subprocess,sys
from pathlib import Path
root=Path(sys.argv[1]);deadline=float(sys.argv[2]);info=json.loads((root/'preparation.json').read_text());errors=[]
for key in ['worktree','oldWorktree']:
 try:
  left=deadline-time.clock_gettime(time.CLOCK_MONOTONIC)
  if left<=0:raise TimeoutError('Cleanup deadline')
  clean=subprocess.check_output(['git','status','--porcelain'],cwd=info[key],text=True,timeout=min(5,left));assert clean=='',key
 except BaseException as e:errors.append(type(e).__name__+':'+str(e))
(root/'epoch-owned-cleanup.json').write_text(json.dumps({'confirmed':not errors,'errors':errors,'fixtureFilesRemoved':[]},indent=2)+'\n')
raise SystemExit(1 if errors else 0)
