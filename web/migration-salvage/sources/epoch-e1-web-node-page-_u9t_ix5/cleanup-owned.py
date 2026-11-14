import time
from pathlib import Path
import hashlib,json,os,stat,subprocess,sys
root=Path(sys.argv[1]);deadline=float(sys.argv[2]);info=json.loads((root/'preparation.json').read_text());work=Path(info['worktree']);proof={'confirmed':False,'errors':[]}
def budget(cap):
 left=deadline-time.clock_gettime(time.CLOCK_MONOTONIC)
 if left<=0:raise TimeoutError('Cleanup budget exhausted')
 return min(left,cap)
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
try:
 resources=root/'owned-resources.json'
 if resources.exists():
  own=json.loads(resources.read_text());mount=Path(own['mount']);image=Path(own['image'])
  assert mount.parent==Path('/private/tmp') and mount.name.startswith('ownsolid-por112-joint-') and image==root/'scratch.sparseimage'
  mounts=subprocess.check_output(['mount'],text=True,timeout=budget(3))
  if ' on '+str(mount)+' (' in mounts:
   r=subprocess.run(['hdiutil','detach',str(mount)],capture_output=True,text=True,timeout=budget(35));(root/'scratch-detach.log').write_text(r.stdout+r.stderr);assert r.returncode==0
  mounts=subprocess.check_output(['mount'],text=True,timeout=budget(3));assert ' on '+str(mount)+' (' not in mounts
  if mount.exists():mount.rmdir()
  if image.exists():assert stat.S_ISREG(image.lstat().st_mode);image.unlink()
  proof['scratchDetached']=True;proof['scratchRemoved']=not mount.exists() and not image.exists()
 audit=work/'tools/audit-verify/audit-verify'
 if resources.exists() and audit.exists():
  assert stat.S_ISREG(audit.lstat().st_mode) and audit.stat().st_uid==os.geteuid()
  with audit.open('rb') as stream:assert stream.read(4)==bytes.fromhex('cffaedfe')
  proof['generatedAuditBinarySha256']=sha(audit);audit.unlink()
 sumfile=work/'go.work.sum'
 if resources.exists() and os.path.lexists(sumfile):
  assert stat.S_ISREG(sumfile.lstat().st_mode) and sumfile.stat().st_uid==os.geteuid()
  with (root/'generated-go.work.sum').open('xb') as stream:stream.write(sumfile.read_bytes())
  proof['generatedWorkspaceSumSha256']=sha(sumfile);sumfile.unlink()
 clean=subprocess.check_output(['git','status','--porcelain'],cwd=work,text=True,timeout=budget(5));(root/'source-cleanup-status.log').write_text(clean);assert clean==''
 proof['sourceClean']=True;proof['confirmed']=True
except BaseException as error:proof['errors'].append(repr(error))
(root/'owned-resources-cleanup.json').write_text(json.dumps(proof,indent=2)+'\n')
raise SystemExit(0 if proof['confirmed'] else 1)
