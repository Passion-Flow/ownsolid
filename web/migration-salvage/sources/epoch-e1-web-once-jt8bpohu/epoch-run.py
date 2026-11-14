import time
from pathlib import Path
import hashlib,json,os,resource,signal,subprocess,sys
assert sys.flags.isolated and not sys.flags.optimize
root=Path(sys.argv[1]);work_deadline=float(sys.argv[2]);hard_deadline=float(sys.argv[3]);selection=json.loads((root/'selected-scope.json').read_text());info=json.loads((root/'preparation.json').read_text());work=Path(info['worktree'])
def budget(cap=1800):
 left=work_deadline-time.clock_gettime(time.CLOCK_MONOTONIC)
 if left<=0:raise TimeoutError('Absolute work cutoff reached')
 return min(left,cap)
def expire(s,f):raise TimeoutError('Absolute work backstop reached')
signal.signal(signal.SIGALRM,expire);signal.setitimer(signal.ITIMER_REAL,budget());signal.signal(signal.SIGTERM,lambda s,f:sys.exit(143));resource.setrlimit(resource.RLIMIT_CORE,(0,0))
assert subprocess.check_output(['git','status','--porcelain'],cwd=work,timeout=budget(5))==b''
subprocess.run(['git','switch','--detach',selection['source']],cwd=work,check=True,timeout=budget(10))
assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=work,text=True,timeout=budget(5)).strip()==selection['source']
assert subprocess.check_output(['git','rev-parse','HEAD^{tree}'],cwd=work,text=True,timeout=budget(5)).strip()==selection['tree']
for path,expected in json.loads((root/selection['manifest']).read_text()).items():
 budget();assert hashlib.sha256((work/path).read_bytes()).hexdigest()==expected,path
assert subprocess.check_output(['git','status','--porcelain'],cwd=work,timeout=budget(5))==b''
(root/'tested-source.json').write_text(json.dumps(selection,indent=2)+'\n')
childfile='full-run.py' if selection['scope']=='A' else 'bench-run.py'
# The runner inherits the same open lock description from the admission parent.
os.execv(sys.executable,[sys.executable,'-I',str(root/childfile),str(root),str(work_deadline),str(hard_deadline)])
